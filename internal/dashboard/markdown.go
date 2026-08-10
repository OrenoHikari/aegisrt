package dashboard

import (
	"fmt"
	stdhtml "html"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RenderMarkdownSafe renders the deliberately small report Markdown dialect.
// Raw HTML is always escaped and links are limited to HTTP(S) or local
// fragments so report content cannot execute script in the Dashboard origin.
func RenderMarkdownSafe(markdown []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(markdown), "\r\n", "\n"), "\n")
	var output strings.Builder
	for index := 0; index < len(lines); {
		line := lines[index]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			index++
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			language := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			index++
			var code []string
			for index < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[index]), "```") {
				code = append(code, lines[index])
				index++
			}
			if index < len(lines) {
				index++
			}
			class := ""
			if safeToken(language) != "" {
				class = ` class="language-` + safeToken(language) + `"`
			}
			fmt.Fprintf(&output, "<pre><code%s>%s</code></pre>\n", class, stdhtml.EscapeString(strings.Join(code, "\n")))
			continue
		}
		if level, title := markdownHeading(trimmed); level > 0 {
			fmt.Fprintf(&output, "<h%d>%s</h%d>\n", level, renderInline(title), level)
			index++
			continue
		}
		if index+1 < len(lines) && looksLikeTableRow(trimmed) && isTableSeparator(strings.TrimSpace(lines[index+1])) {
			headers := tableCells(trimmed)
			index += 2
			output.WriteString("<div class=\"report-table-wrap\"><table><thead><tr>")
			for _, cell := range headers {
				fmt.Fprintf(&output, "<th>%s</th>", renderInline(cell))
			}
			output.WriteString("</tr></thead><tbody>")
			for index < len(lines) && looksLikeTableRow(strings.TrimSpace(lines[index])) {
				output.WriteString("<tr>")
				for _, cell := range tableCells(strings.TrimSpace(lines[index])) {
					fmt.Fprintf(&output, "<td>%s</td>", renderInline(cell))
				}
				output.WriteString("</tr>")
				index++
			}
			output.WriteString("</tbody></table></div>\n")
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			var quoted []string
			for index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), ">") {
				quoted = append(quoted, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[index]), ">")))
				index++
			}
			fmt.Fprintf(&output, "<blockquote>%s</blockquote>\n", renderInline(strings.Join(quoted, " ")))
			continue
		}
		if unorderedItem(trimmed) != "" {
			output.WriteString("<ul>")
			for index < len(lines) {
				item := unorderedItem(strings.TrimSpace(lines[index]))
				if item == "" {
					break
				}
				fmt.Fprintf(&output, "<li>%s</li>", renderInline(item))
				index++
			}
			output.WriteString("</ul>\n")
			continue
		}
		if item, ok := orderedItem(trimmed); ok {
			_ = item
			output.WriteString("<ol>")
			for index < len(lines) {
				item, ok := orderedItem(strings.TrimSpace(lines[index]))
				if !ok {
					break
				}
				fmt.Fprintf(&output, "<li>%s</li>", renderInline(item))
				index++
			}
			output.WriteString("</ol>\n")
			continue
		}
		var paragraph []string
		for index < len(lines) {
			candidate := strings.TrimSpace(lines[index])
			if candidate == "" || (len(paragraph) > 0 && startsMarkdownBlock(lines, index)) {
				break
			}
			paragraph = append(paragraph, candidate)
			index++
		}
		fmt.Fprintf(&output, "<p>%s</p>\n", renderInline(strings.Join(paragraph, " ")))
	}
	return output.String()
}

func renderInline(value string) string {
	var output strings.Builder
	for index := 0; index < len(value); {
		switch {
		case value[index] == '`':
			if end := strings.IndexByte(value[index+1:], '`'); end >= 0 {
				end += index + 1
				output.WriteString("<code>")
				output.WriteString(stdhtml.EscapeString(value[index+1 : end]))
				output.WriteString("</code>")
				index = end + 1
				continue
			}
		case strings.HasPrefix(value[index:], "**"):
			if end := strings.Index(value[index+2:], "**"); end >= 0 {
				end += index + 2
				output.WriteString("<strong>")
				output.WriteString(stdhtml.EscapeString(value[index+2 : end]))
				output.WriteString("</strong>")
				index = end + 2
				continue
			}
		case value[index] == '*':
			if end := strings.IndexByte(value[index+1:], '*'); end >= 0 {
				end += index + 1
				output.WriteString("<em>")
				output.WriteString(stdhtml.EscapeString(value[index+1 : end]))
				output.WriteString("</em>")
				index = end + 1
				continue
			}
		case value[index] == '[':
			if closeBracket := strings.IndexByte(value[index+1:], ']'); closeBracket >= 0 {
				closeBracket += index + 1
				label := value[index+1 : closeBracket]
				if closeBracket+1 < len(value) && value[closeBracket+1] == '(' {
					if closeParen := strings.IndexByte(value[closeBracket+2:], ')'); closeParen >= 0 {
						closeParen += closeBracket + 2
						destination := strings.TrimSpace(value[closeBracket+2 : closeParen])
						if safeMarkdownLink(destination) {
							fmt.Fprintf(&output, `<a href="%s" rel="noopener noreferrer">%s</a>`, stdhtml.EscapeString(destination), stdhtml.EscapeString(label))
						} else {
							output.WriteString(stdhtml.EscapeString(label))
						}
						index = closeParen + 1
						continue
					}
				}
				if isPaperCitation(label) {
					fmt.Fprintf(&output, `<a class="citation" href="#paper-%s">[%s]</a>`, stdhtml.EscapeString(label), stdhtml.EscapeString(label))
					index = closeBracket + 1
					continue
				}
			}
		}
		_, size := utf8.DecodeRuneInString(value[index:])
		if size == 0 {
			size = 1
		}
		output.WriteString(stdhtml.EscapeString(value[index : index+size]))
		index += size
	}
	return output.String()
}

func startsMarkdownBlock(lines []string, index int) bool {
	trimmed := strings.TrimSpace(lines[index])
	if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, ">") || unorderedItem(trimmed) != "" {
		return true
	}
	if _, ok := orderedItem(trimmed); ok {
		return true
	}
	if level, _ := markdownHeading(trimmed); level > 0 {
		return true
	}
	return index+1 < len(lines) && looksLikeTableRow(trimmed) && isTableSeparator(strings.TrimSpace(lines[index+1]))
}

func markdownHeading(value string) (int, string) {
	level := 0
	for level < len(value) && level < 6 && value[level] == '#' {
		level++
	}
	if level == 0 || level >= len(value) || value[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(value[level+1:])
}

func unorderedItem(value string) string {
	if len(value) >= 2 && (value[0] == '-' || value[0] == '*') && value[1] == ' ' {
		return strings.TrimSpace(value[2:])
	}
	return ""
}

func orderedItem(value string) (string, bool) {
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	if index == 0 || index+1 >= len(value) || value[index] != '.' || value[index+1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(value[index+2:]), true
}

func looksLikeTableRow(value string) bool { return strings.Count(value, "|") >= 2 }

func isTableSeparator(value string) bool {
	cells := tableCells(value)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.Trim(strings.TrimSpace(cell), ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func tableCells(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "|")
	value = strings.TrimSuffix(value, "|")
	raw := strings.Split(value, "|")
	result := make([]string, 0, len(raw))
	for _, cell := range raw {
		result = append(result, strings.TrimSpace(cell))
	}
	return result
}

func safeMarkdownLink(destination string) bool {
	if strings.HasPrefix(destination, "#") && len(destination) > 1 {
		return true
	}
	parsed, err := url.Parse(destination)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func isPaperCitation(value string) bool {
	if len(value) < 2 || value[0] != 'P' {
		return false
	}
	for _, character := range value[1:] {
		if !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func safeToken(value string) string {
	var result strings.Builder
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '-' || character == '_' {
			result.WriteRune(character)
		}
	}
	return result.String()
}
