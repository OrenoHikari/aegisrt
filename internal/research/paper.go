package research

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maximumParsedCharacters  = 200_000
	maximumSectionCharacters = 40_000
	maximumSections          = 32
	maximumPages             = 64
	maximumParserInputBytes  = 20 * 1024 * 1024
)

var (
	pdfLiteral             = regexp.MustCompile(`\((\\.|[^\\)])*\)`)
	sectionNumberPrefix    = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*[.)]?\s+`)
	sentenceBoundaryRegexp = regexp.MustCompile(`[.!?]+\s+`)
)

// BasicGoParser is the retained dependency-free fallback parser.
type BasicGoParser struct {
	MaxInputBytes int
}

func (BasicGoParser) Name() string { return "basic-go" }

func (p BasicGoParser) Parse(_ context.Context, fetch FetchResult, data []byte) (PaperDocument, error) {
	started := time.Now()
	limit := p.MaxInputBytes
	if limit <= 0 {
		limit = maximumParserInputBytes
	}
	if len(data) > limit {
		return PaperDocument{}, fmt.Errorf("%w: parser input exceeds %d bytes", ErrDownloadTooLarge, limit)
	}
	var pages []string
	switch fetch.ContentType {
	case "text/plain", "text/markdown":
		pages = []string{string(data)}
	case "application/pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return PaperDocument{}, fmt.Errorf("%w: PDF signature is missing", ErrInvalidPaperContent)
		}
		pages = []string{extractPDFText(data)}
	default:
		return PaperDocument{}, fmt.Errorf("%w: content type %q", ErrInvalidPaperContent, fetch.ContentType)
	}
	document, err := buildPaperDocument(fetch, "basic-go", false, pages)
	if err == nil {
		finishParserDiagnostics(&document, []string{"basic-go"}, time.Since(started), nil)
	}
	return document, err
}

// ParseDocument preserves the Stage 3 API and selects the basic parser. The
// worker can select the Python-first fallback chain through PaperParser.
func ParseDocument(fetch FetchResult, data []byte) (ParsedPaper, error) {
	if !fetch.Available {
		return ParsedPaper{}, ErrFullTextUnavailable
	}
	return BasicGoParser{}.Parse(context.Background(), fetch, data)
}

type pageLine struct {
	text string
	page int
}

func buildPaperDocument(fetch FetchResult, parser string, fallback bool, pageTexts []string) (PaperDocument, error) {
	if !fetch.Available {
		return PaperDocument{}, ErrFullTextUnavailable
	}
	if len(pageTexts) == 0 {
		return PaperDocument{}, fmt.Errorf("%w: parser returned no pages", ErrInvalidPaperContent)
	}
	if len(pageTexts) > maximumPages {
		pageTexts = pageTexts[:maximumPages]
	}
	var pages []Page
	var lines []pageLine
	pageStreamOffset := 0
	truncated := false
	remaining := maximumParsedCharacters
	for index, raw := range pageTexts {
		text := normalizeExtractedText(raw)
		if len(text) > remaining {
			text = truncateUTF8Bytes(text, remaining)
			truncated = true
		}
		if text == "" {
			continue
		}
		if len(pages) > 0 {
			pageStreamOffset += 2
		}
		page := Page{Number: index + 1, Text: text, Start: pageStreamOffset, End: pageStreamOffset + len(text)}
		pages = append(pages, page)
		pageStreamOffset = page.End
		for _, line := range strings.Split(text, "\n") {
			lines = append(lines, pageLine{text: line, page: page.Number})
		}
		remaining -= len(text)
		if remaining <= 0 {
			truncated = true
			break
		}
	}
	if len(pages) == 0 || pageStreamOffset < 40 {
		return PaperDocument{}, fmt.Errorf("%w: no usable text extracted", ErrInvalidPaperContent)
	}
	sections, documentText := splitPaperSections(lines)
	var references []string
	abstract := strings.TrimSpace(fetch.Paper.Abstract)
	for _, section := range sections {
		if section.NormalizedHeading == "abstract" && section.Text != "" {
			abstract = section.Text
		}
		if section.NormalizedHeading == "references" {
			for _, line := range strings.Split(section.Text, "\n") {
				if value := strings.TrimSpace(line); value != "" {
					references = append(references, value)
				}
			}
		}
	}
	return PaperDocument{
		Paper: fetch.Paper, Query: fetch.Query, Abstract: abstract, Parser: parser, Fallback: fallback,
		Pages: pages, Sections: sections, Text: documentText, References: references,
		Characters: len(documentText), Truncated: truncated,
	}, nil
}

func normalizeExtractedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\x00", "")
	text = strings.ReplaceAll(text, "\u00a0", " ")
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = strings.TrimRightFunc(lines[index], unicode.IsSpace)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func splitPaperSections(lines []pageLine) ([]Section, string) {
	heading := "Unknown"
	normalizedHeading := "unknown"
	var body []pageLine
	sections := make([]Section, 0)
	var document strings.Builder
	flush := func() {
		textLines := make([]string, 0, len(body))
		for _, line := range body {
			textLines = append(textLines, line.text)
		}
		content := strings.TrimSpace(strings.Join(textLines, "\n"))
		if content == "" || len(sections) >= maximumSections {
			body = nil
			return
		}
		truncated := false
		if len(content) > maximumSectionCharacters {
			content = truncateUTF8Bytes(content, maximumSectionCharacters)
			truncated = true
		}
		if document.Len() > 0 {
			document.WriteString("\n\n")
		}
		start := document.Len()
		document.WriteString(content)
		pageStart, pageEnd := 1, 1
		if len(body) > 0 {
			pageStart, pageEnd = body[0].page, body[len(body)-1].page
		}
		sections = append(sections, Section{
			ID: fmt.Sprintf("section-%03d", len(sections)+1), Heading: heading,
			NormalizedHeading: normalizedHeading, Text: content,
			PageStart: pageStart, PageEnd: pageEnd, Start: start, End: document.Len(), Truncated: truncated,
		})
		body = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if candidate, ok := sectionHeading(trimmed); ok {
			flush()
			heading = candidate
			normalizedHeading = normalizeSectionHeading(candidate)
			continue
		}
		body = append(body, line)
	}
	flush()
	if len(sections) == 0 {
		return nil, ""
	}
	return sections, document.String()
}

func normalizeSectionHeading(heading string) string {
	value := strings.ToLower(strings.Join(strings.Fields(heading), " "))
	value = strings.Trim(value, "0123456789.:- ")
	switch {
	case value == "abstract":
		return "abstract"
	case strings.Contains(value, "introduction"):
		return "introduction"
	case strings.Contains(value, "related work") || strings.Contains(value, "background"):
		return "related_work"
	case strings.Contains(value, "method") || strings.Contains(value, "approach") || strings.Contains(value, "methodology"):
		return "method"
	case strings.Contains(value, "experiment") || strings.Contains(value, "evaluation"):
		return "experiments"
	case value == "results" || strings.Contains(value, "result"):
		return "results"
	case strings.Contains(value, "discussion"):
		return "discussion"
	case strings.Contains(value, "conclusion"):
		return "conclusion"
	case strings.Contains(value, "reference") || strings.Contains(value, "bibliography"):
		return "references"
	default:
		return "unknown"
	}
}

func sectionHeading(line string) (string, bool) {
	if strings.HasPrefix(line, "#") {
		value := strings.TrimSpace(strings.TrimLeft(line, "#"))
		return value, value != ""
	}
	if strings.Contains(line, ":") {
		return "", false
	}
	if strings.HasSuffix(line, ".") || strings.HasSuffix(line, "!") || strings.HasSuffix(line, "?") {
		return "", false
	}
	withoutNumber := sectionNumberPrefix.ReplaceAllString(strings.TrimSpace(line), "")
	hadNumber := withoutNumber != strings.TrimSpace(line)
	if len(withoutNumber) > 2 && len(withoutNumber) < 100 && len(strings.Fields(withoutNumber)) <= 8 &&
		(hadNumber || isCanonicalUnnumberedHeading(withoutNumber)) {
		return withoutNumber, true
	}
	if len(line) > 2 && len(line) < 80 {
		letters, upper := 0, 0
		for _, r := range line {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters >= 3 && upper == letters {
			lower := strings.ToLower(line)
			runes := []rune(lower)
			runes[0] = unicode.ToUpper(runes[0])
			return string(runes), true
		}
	}
	return "", false
}

func isCanonicalUnnumberedHeading(value string) bool {
	value = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	switch value {
	case "abstract", "introduction", "related work", "background", "method", "methods", "approach", "methodology",
		"experiments", "experimental setup", "evaluation", "results", "discussion", "conclusion", "conclusions",
		"references", "bibliography":
		return true
	default:
		return false
	}
}

func extractPDFText(data []byte) string {
	streams := [][]byte{data}
	searchFrom := 0
	for {
		streamOffset := bytes.Index(data[searchFrom:], []byte("stream"))
		if streamOffset < 0 {
			break
		}
		streamOffset += searchFrom
		start := streamOffset + len("stream")
		if start < len(data) && data[start] == '\r' {
			start++
		}
		if start < len(data) && data[start] == '\n' {
			start++
		}
		endOffset := bytes.Index(data[start:], []byte("endstream"))
		if endOffset < 0 {
			break
		}
		end := start + endOffset
		chunk := bytes.TrimSpace(data[start:end])
		dictionaryStart := streamOffset - 512
		if dictionaryStart < 0 {
			dictionaryStart = 0
		}
		if bytes.Contains(data[dictionaryStart:streamOffset], []byte("/FlateDecode")) {
			if reader, err := zlib.NewReader(bytes.NewReader(chunk)); err == nil {
				decoded, readErr := io.ReadAll(io.LimitReader(reader, maximumParsedCharacters+1))
				_ = reader.Close()
				if readErr == nil {
					streams = append(streams, decoded)
				}
			}
		} else {
			streams = append(streams, chunk)
		}
		searchFrom = end + len("endstream")
	}
	var values []string
	for _, stream := range streams {
		for _, match := range pdfLiteral.FindAll(stream, -1) {
			if len(match) < 2 {
				continue
			}
			value := decodePDFLiteral(string(match[1 : len(match)-1]))
			if strings.TrimSpace(value) != "" {
				values = append(values, value)
			}
		}
	}
	return strings.Join(values, "\n")
}

func decodePDFLiteral(value string) string {
	replacer := strings.NewReplacer(`\n`, "\n", `\r`, "\n", `\t`, "\t", `\(`, "(", `\)`, ")", `\\`, `\`)
	return replacer.Replace(value)
}

const maximumFindingsPerPaper = 32

// AnalyzePaper is the deterministic/basic mode. It creates candidates first,
// then uses the same EvidenceVerifier as LLM mode.
func AnalyzePaper(parsed ParsedPaper, question, taskID string) (PaperAnalysis, error) {
	return AnalyzePaperContext(context.Background(), parsed, question, taskID)
}

func AnalyzePaperContext(ctx context.Context, parsed ParsedPaper, question, taskID string) (PaperAnalysis, error) {
	var candidates []CandidateFinding
	seenClaims := make(map[string]struct{})
	add := func(section Section, claimType, claim, snippet string) {
		claim = strings.TrimSpace(claim)
		if claim == "" || len(candidates) >= maximumFindingsPerPaper {
			return
		}
		key := strings.ToLower(claim)
		if _, exists := seenClaims[key]; exists {
			return
		}
		seenClaims[key] = struct{}{}
		candidates = append(candidates, CandidateFinding{
			Claim: claim, ClaimType: claimType, PaperID: parsed.Paper.ID,
			SectionID: section.ID, EvidenceText: boundedSnippet(snippet), Importance: "basic-extraction",
		})
	}
	for _, section := range parsed.Sections {
		for _, line := range strings.Split(section.Text, "\n") {
			line = strings.TrimSpace(line)
			label, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch strings.ToUpper(strings.TrimSpace(label)) {
			case "PROBLEM":
				add(section, "problem", value, line)
			case "METHOD":
				add(section, "method", value, line)
			case "CONTRIBUTION":
				add(section, "contribution", value, line)
			case "DATASET":
				add(section, "dataset", value, line)
			case "METRIC":
				add(section, "metric", value, line)
			case "EXPERIMENT":
				add(section, "experiment", value, line)
			case "RESULT":
				add(section, "result", value, line)
			case "LIMITATION":
				add(section, "limitation", value, line)
			}
		}
	}
	abstractSentences := splitSentences(parsed.Abstract)
	if len(candidates) == 0 && len(abstractSentences) > 0 {
		if section, exists := sectionContaining(parsed.Sections, abstractSentences[0]); exists {
			add(section, "problem", abstractSentences[0], abstractSentences[0])
		}
	}
	method := firstMatchingSentence(abstractSentences, "we propose", "we present", "we introduce", "our method", "our approach", "our framework")
	if method != "" {
		if section, exists := sectionContaining(parsed.Sections, method); exists {
			add(section, "method", method, method)
		}
	}
	result := firstMatchingSentence(abstractSentences, "outperform", "improve", "achieve", "result", "demonstrate")
	if result != "" {
		if section, exists := sectionContaining(parsed.Sections, result); exists {
			add(section, "result", result, result)
		}
	}
	findings, evidence := (EvidenceVerifier{}).Verify(ctx, parsed, candidates, taskID)
	analysis := analysisFromVerified(parsed, question, candidates, findings, evidence, nil)
	if countSupported(findings) == 0 {
		return PaperAnalysis{}, fmt.Errorf("%w: paper %s produced no traceable claims", ErrInsufficientEvidence, parsed.Paper.ID)
	}
	return analysis, nil
}

func analysisFromVerified(
	document PaperDocument,
	question string,
	candidates []CandidateFinding,
	findings []VerifiedFinding,
	evidence []Evidence,
	usage *TokenUsage,
) PaperAnalysis {
	analysis := PaperAnalysis{
		Paper: document.Paper, Query: document.Query, ResearchQuestion: strings.TrimSpace(question),
		CandidateFindings: append([]CandidateFinding(nil), candidates...),
		Findings:          append([]VerifiedFinding(nil), findings...), Evidence: append([]Evidence(nil), evidence...), Usage: usage,
	}
	for _, finding := range findings {
		if finding.Status != FindingSupported {
			continue
		}
		claim := strings.TrimSpace(finding.Candidate.Claim)
		switch strings.ToLower(strings.TrimSpace(finding.Candidate.ClaimType)) {
		case "problem":
			if analysis.Problem == "" {
				analysis.Problem = claim
			}
		case "method":
			if analysis.Method == "" {
				analysis.Method = claim
			}
		case "contribution":
			analysis.KeyContributions = appendUnique(analysis.KeyContributions, claim)
		case "dataset":
			analysis.Datasets = appendUnique(analysis.Datasets, splitList(claim)...)
		case "metric":
			analysis.Metrics = appendUnique(analysis.Metrics, splitList(claim)...)
		case "experiment":
			analysis.Experiments = appendUnique(analysis.Experiments, claim)
		case "result":
			analysis.MainResults = appendUnique(analysis.MainResults, claim)
		case "limitation":
			analysis.Limitations = appendUnique(analysis.Limitations, claim)
		}
	}
	return analysis
}

func sectionContaining(sections []Section, text string) (Section, bool) {
	for _, section := range sections {
		if _, _, ok := locateNormalizedEvidence(section.Text, text); ok {
			return section, true
		}
	}
	return Section{}, false
}

func countSupported(findings []VerifiedFinding) int {
	count := 0
	for _, finding := range findings {
		if finding.Status == FindingSupported {
			count++
		}
	}
	return count
}

func splitSentences(value string) []string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return nil
	}
	parts := sentenceBoundaryRegexp.Split(value, -1)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if sentence := strings.TrimSpace(part); sentence != "" {
			result = append(result, sentence)
		}
	}
	return result
}

func firstMatchingSentence(sentences []string, markers ...string) string {
	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, marker := range markers {
			if strings.Contains(lower, marker) {
				return sentence
			}
		}
	}
	return ""
}

func boundedSnippet(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return truncateUTF8Bytes(value, 500)
	}
	return value
}

func truncateUTF8Bytes(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' })
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if item := strings.TrimSpace(field); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func appendUnique(target []string, values ...string) []string {
	seen := make(map[string]struct{}, len(target)+len(values))
	for _, value := range target {
		seen[strings.ToLower(value)] = struct{}{}
	}
	for _, value := range values {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; !exists {
			target = append(target, strings.TrimSpace(value))
			seen[key] = struct{}{}
		}
	}
	sort.Strings(target)
	return target
}

func appendUniqueOrdered(target []string, values ...string) []string {
	seen := make(map[string]struct{}, len(target)+len(values))
	for _, value := range target {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; !exists {
			target = append(target, value)
			seen[key] = struct{}{}
		}
	}
	return target
}
