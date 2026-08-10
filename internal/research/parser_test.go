package research

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parserFetch(contentType string) FetchResult {
	return FetchResult{
		Paper: Paper{ID: "paper.parser", Title: "Parser Paper", URL: "https://example.test/paper", Provider: "test"},
		Query: "parser", Available: true, ContentType: contentType,
	}
}

func basicTestPDF() []byte {
	return []byte("%PDF-1.4\n(Abstract\\nThis paper studies reliable parsing and evidence verification.)\n(Method\\nThe parser retains stable section identifiers and byte offsets.)\n(Results\\nThe extracted text is suitable for deterministic tests.)")
}

func TestBasicPDFParserNormal(t *testing.T) {
	document, err := (BasicGoParser{}).Parse(context.Background(), parserFetch("application/pdf"), basicTestPDF())
	if err != nil {
		t.Fatal(err)
	}
	if document.Parser != "basic-go" || len(document.Pages) != 1 || len(document.Sections) < 2 || document.Characters == 0 {
		t.Fatalf("unexpected PDF document: %+v", document)
	}
	if document.Diagnostics.Selected != "basic-go" || document.Diagnostics.PageCount != len(document.Pages) ||
		document.Diagnostics.DetectedSections != len(document.Sections) || document.Diagnostics.ExtractedCharacters != document.Characters {
		t.Fatalf("parser diagnostics did not describe the real document: %+v", document.Diagnostics)
	}
}

func TestPDFParserRejectsMalformedEmptyAndOversized(t *testing.T) {
	parser := BasicGoParser{}
	for name, data := range map[string][]byte{"malformed": []byte("not-pdf"), "empty": nil} {
		t.Run(name, func(t *testing.T) {
			if _, err := parser.Parse(context.Background(), parserFetch("application/pdf"), data); !errors.Is(err, ErrInvalidPaperContent) {
				t.Fatalf("expected invalid PDF content, got %v", err)
			}
		})
	}
	oversized := make([]byte, maximumParserInputBytes+1)
	copy(oversized, "%PDF-")
	if _, err := parser.Parse(context.Background(), parserFetch("application/pdf"), oversized); !errors.Is(err, ErrDownloadTooLarge) {
		t.Fatalf("expected oversized PDF rejection, got %v", err)
	}
}

func TestPythonPDFParserNormalBoundedAdapter(t *testing.T) {
	script := filepath.Join(t.TempDir(), "parser.py")
	program := `import json,sys
data=sys.stdin.buffer.read()
assert data.startswith(b"%PDF-")
json.dump({"parser":"pypdf","pages":[{"number":1,"text":"Abstract\\nA bounded Python parser result with sufficient text.\\nMethod\\nStable evidence offsets are retained."}]},sys.stdout)
`
	if err := os.WriteFile(script, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := (PythonPDFParser{Python: "python3", ScriptPath: script, Timeout: 5 * time.Second}).Parse(
		context.Background(), parserFetch("application/pdf"), basicTestPDF())
	if err != nil {
		t.Fatal(err)
	}
	if document.Parser != "python-pypdf" || len(document.Pages) != 1 || document.Fallback {
		t.Fatalf("unexpected Python document: %+v", document)
	}
}

type alwaysFailParser struct{}

func (alwaysFailParser) Name() string { return "always-fail" }
func (alwaysFailParser) Parse(context.Context, FetchResult, []byte) (PaperDocument, error) {
	return PaperDocument{}, errors.New("fixture parser failure")
}

func TestParserFailureFallsBackToBasic(t *testing.T) {
	parser := FallbackPaperParser{Primary: alwaysFailParser{}, Fallback: BasicGoParser{}}
	document, err := parser.Parse(context.Background(), parserFetch("application/pdf"), basicTestPDF())
	if err != nil {
		t.Fatal(err)
	}
	if !document.Fallback || document.Parser != "basic-go" || !document.Diagnostics.FallbackUsed ||
		document.Diagnostics.WarningCount != 1 || len(document.Diagnostics.Attempted) != 2 {
		t.Fatalf("fallback was not recorded: %+v", document)
	}
}

func TestPythonParserDependencyDetection(t *testing.T) {
	moduleRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(moduleRoot, "pypdf.py"), []byte("__version__ = 'fixture-1.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYTHONPATH", moduleRoot)
	status := DetectPythonPDFParser(context.Background(), "python3", 5*time.Second)
	if !status.Available || status.Version != "fixture-1.0" || status.Parser != "python-pypdf" {
		t.Fatalf("expected available parser dependency, got %+v", status)
	}
	unavailable := DetectPythonPDFParser(context.Background(), filepath.Join(t.TempDir(), "missing-python"), time.Second)
	if unavailable.Available || unavailable.Reason == "" {
		t.Fatalf("missing dependency was not explicit: %+v", unavailable)
	}
}

func TestPythonParserHonorsContextCancellation(t *testing.T) {
	script := filepath.Join(t.TempDir(), "slow.py")
	if err := os.WriteFile(script, []byte("import time\ntime.sleep(30)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (PythonPDFParser{Python: "python3", ScriptPath: script, Timeout: time.Minute}).Parse(ctx, parserFetch("application/pdf"), basicTestPDF())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("parser ignored cancellation: %v", err)
	}
}

func TestSectionNormalizationStandardMissingAndUnknown(t *testing.T) {
	standard := strings.Join([]string{
		"Abstract", "A sufficiently long abstract body for parsing.",
		"1 Introduction", "Introduction body with enough textual content.",
		"2 Related Work", "Prior work body with citations described.",
		"3 Approach", "Method body with deterministic behavior.",
		"4 Experiments", "Experiments body with a controlled protocol.",
		"5 Results", "Results body with measurements.",
		"6 Discussion", "Discussion body with qualifications.",
		"7 Conclusion", "Conclusion body for the study.",
		"References", "Reference One"}, "\n")
	document, err := (BasicGoParser{}).Parse(context.Background(), parserFetch("text/plain"), []byte(standard))
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"abstract": false, "introduction": false, "related_work": false, "method": false, "experiments": false, "results": false, "discussion": false, "conclusion": false, "references": false}
	for _, section := range document.Sections {
		if _, exists := wanted[section.NormalizedHeading]; exists {
			wanted[section.NormalizedHeading] = true
		}
		if section.ID == "" || section.Start < 0 || section.End <= section.Start || section.PageStart < 1 {
			t.Fatalf("unstable section: %+v", section)
		}
	}
	for heading, found := range wanted {
		if !found {
			t.Errorf("missing normalized heading %s", heading)
		}
	}

	unknownText := "This document intentionally has no section headings.\nIt remains a generic section and is never guessed into a standard heading."
	unknown, err := (BasicGoParser{}).Parse(context.Background(), parserFetch("text/plain"), []byte(unknownText))
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown.Sections) != 1 || unknown.Sections[0].Heading != "Unknown" || unknown.Sections[0].NormalizedHeading != "unknown" {
		t.Fatalf("missing headings were guessed: %+v", unknown.Sections)
	}
}
