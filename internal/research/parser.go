package research

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PaperParser turns one fetched artifact into the single canonical
// PaperDocument model.
type PaperParser interface {
	Name() string
	Parse(ctx context.Context, fetch FetchResult, data []byte) (PaperDocument, error)
}

// PythonPDFParser invokes the fixed pypdf adapter without a shell. It passes
// the PDF through stdin and accepts only bounded JSON page text on stdout.
type PythonPDFParser struct {
	Python        string
	ScriptPath    string
	Timeout       time.Duration
	MaxInputBytes int
}

func (p PythonPDFParser) Name() string { return "python-pypdf" }

func (p PythonPDFParser) Parse(ctx context.Context, fetch FetchResult, data []byte) (PaperDocument, error) {
	started := time.Now()
	if fetch.ContentType != "application/pdf" {
		return PaperDocument{}, fmt.Errorf("%w: Python parser accepts PDF only", ErrInvalidPaperContent)
	}
	if len(data) == 0 {
		return PaperDocument{}, fmt.Errorf("%w: empty PDF", ErrInvalidPaperContent)
	}
	limit := p.MaxInputBytes
	if limit <= 0 {
		limit = maximumParserInputBytes
	}
	if len(data) > limit {
		return PaperDocument{}, fmt.Errorf("%w: parser input exceeds %d bytes", ErrDownloadTooLarge, limit)
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return PaperDocument{}, fmt.Errorf("%w: PDF signature is missing", ErrInvalidPaperContent)
	}
	python := strings.TrimSpace(p.Python)
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return PaperDocument{}, fmt.Errorf("locate Python PDF parser: %w", err)
	}
	if strings.TrimSpace(p.ScriptPath) == "" {
		return PaperDocument{}, fmt.Errorf("Python PDF parser script is not configured")
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	parseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(
		parseCtx, python, p.ScriptPath,
		"--max-pages", strconv.Itoa(maximumPages),
		"--max-chars", strconv.Itoa(maximumParsedCharacters),
	)
	command.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if contextErr := parseCtx.Err(); contextErr != nil {
			return PaperDocument{}, contextErr
		}
		message := strings.TrimSpace(stderr.String())
		if len(message) > 1024 {
			message = message[:1024]
		}
		return PaperDocument{}, fmt.Errorf("Python PDF parser failed: %w: %s", err, message)
	}
	if stdout.Len() > maximumParsedCharacters*6+maximumPages*256 {
		return PaperDocument{}, fmt.Errorf("%w: Python parser output is too large", ErrInvalidPaperContent)
	}
	var output struct {
		Parser    string `json:"parser"`
		PageCount int    `json:"page_count,omitempty"`
		Truncated bool   `json:"truncated,omitempty"`
		Pages     []struct {
			Number int    `json:"number"`
			Text   string `json:"text"`
		} `json:"pages"`
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return PaperDocument{}, fmt.Errorf("decode Python PDF parser output: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return PaperDocument{}, fmt.Errorf("%w: trailing Python parser JSON", ErrInvalidPaperContent)
	}
	if output.Parser != "pypdf" || len(output.Pages) == 0 || len(output.Pages) > maximumPages {
		return PaperDocument{}, fmt.Errorf("%w: invalid Python parser page result", ErrInvalidPaperContent)
	}
	pageTexts := make([]string, 0, len(output.Pages))
	for index, page := range output.Pages {
		if page.Number != index+1 {
			return PaperDocument{}, fmt.Errorf("%w: non-sequential PDF page numbers", ErrInvalidPaperContent)
		}
		pageTexts = append(pageTexts, page.Text)
	}
	document, err := buildPaperDocument(fetch, p.Name(), false, pageTexts)
	if err == nil {
		document.Truncated = document.Truncated || output.Truncated
		finishParserDiagnostics(&document, []string{p.Name()}, time.Since(started), nil)
		if output.PageCount > 0 {
			document.Diagnostics.PageCount = output.PageCount
		}
	}
	return document, err
}

// FallbackPaperParser retains the basic Go parser when the configured mature
// parser is unavailable or rejects a document.
type FallbackPaperParser struct {
	Primary  PaperParser
	Fallback PaperParser
}

func (p FallbackPaperParser) Name() string { return p.Primary.Name() + "+fallback" }

func (p FallbackPaperParser) Parse(ctx context.Context, fetch FetchResult, data []byte) (PaperDocument, error) {
	started := time.Now()
	document, primaryErr := p.Primary.Parse(ctx, fetch, data)
	if primaryErr == nil {
		return document, nil
	}
	if err := ctx.Err(); err != nil {
		return PaperDocument{}, err
	}
	document, fallbackErr := p.Fallback.Parse(ctx, fetch, data)
	if fallbackErr != nil {
		return PaperDocument{}, fmt.Errorf("primary parser: %v; fallback parser: %w", primaryErr, fallbackErr)
	}
	document.Fallback = true
	warning := boundedDiagnosticWarning(primaryErr)
	finishParserDiagnostics(&document, []string{p.Primary.Name(), p.Fallback.Name()}, time.Since(started), []string{warning})
	return document, nil
}

func NewPaperParser(mode, scriptPath string, timeout time.Duration) (PaperParser, error) {
	return NewPaperParserWithPython(mode, "", scriptPath, timeout)
}

func finishParserDiagnostics(document *PaperDocument, attempted []string, duration time.Duration, warnings []string) {
	document.Diagnostics = ParserDiagnostics{
		Selected: document.Parser, Attempted: append([]string(nil), attempted...),
		PageCount: len(document.Pages), ExtractedCharacters: document.Characters,
		DetectedSections: len(document.Sections), DurationMillis: duration.Milliseconds(),
		FallbackUsed: document.Fallback, Truncated: document.Truncated,
		WarningCount: len(warnings), Warnings: append([]string(nil), warnings...),
	}
}

func boundedDiagnosticWarning(err error) string {
	message := "preferred parser failed"
	if err != nil {
		message = strings.TrimSpace(err.Error())
	}
	if len(message) > 512 {
		message = truncateUTF8Bytes(message, 512)
	}
	return message
}

type ParserDependencyStatus struct {
	Parser    string `json:"parser"`
	Python    string `json:"python"`
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

// DetectPythonPDFParser checks only the configured interpreter and pypdf
// import. The fixed command never evaluates user-provided Python source.
func DetectPythonPDFParser(ctx context.Context, python string, timeout time.Duration) ParserDependencyStatus {
	started := time.Now()
	python = strings.TrimSpace(python)
	if python == "" {
		python = "python3"
	}
	status := ParserDependencyStatus{Parser: "python-pypdf", Python: python}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(checkCtx, python, "-c", "import pypdf; print(pypdf.__version__)")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	status.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		if checkCtx.Err() != nil {
			status.Reason = checkCtx.Err().Error()
		} else {
			status.Reason = boundedDiagnosticWarning(fmt.Errorf("pypdf dependency check: %w: %s", err, strings.TrimSpace(stderr.String())))
		}
		return status
	}
	status.Version = strings.TrimSpace(stdout.String())
	status.Available = status.Version != ""
	if !status.Available {
		status.Reason = "pypdf version output is empty"
	}
	return status
}

func NewPaperParserWithPython(mode, python, scriptPath string, timeout time.Duration) (PaperParser, error) {
	return NewPaperParserWithLimit(mode, python, scriptPath, timeout, maximumParserInputBytes)
}

func NewPaperParserWithLimit(mode, python, scriptPath string, timeout time.Duration, maxInputBytes int) (PaperParser, error) {
	if maxInputBytes <= 0 {
		maxInputBytes = maximumParserInputBytes
	}
	basic := BasicGoParser{MaxInputBytes: maxInputBytes}
	preferred := PythonPDFParser{Python: strings.TrimSpace(python), ScriptPath: strings.TrimSpace(scriptPath), Timeout: timeout, MaxInputBytes: maxInputBytes}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return FallbackPaperParser{Primary: preferred, Fallback: basic}, nil
	case "basic":
		return basic, nil
	case "python", "pypdf":
		return preferred, nil
	default:
		return nil, fmt.Errorf("paper parser mode must be auto, basic, or python")
	}
}
