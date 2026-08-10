package research

import (
	"context"
	"errors"
	"testing"
)

func TestPaperParseAndAnalyze(t *testing.T) {
	provider, err := NewMockProvider(MockScenarioNormal)
	if err != nil {
		t.Fatal(err)
	}
	papers, err := provider.Search(context.Background(), SearchRequest{Query: "counting", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	document, err := provider.Fetch(context.Background(), papers[0])
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseDocument(FetchResult{
		Paper: papers[0], Query: "counting", Available: true, ContentType: document.ContentType,
	}, document.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Sections) < 4 || parsed.Abstract == "" || parsed.Characters == 0 {
		t.Fatalf("paper was not parsed: %+v", parsed)
	}
	analysis, err := AnalyzePaper(parsed, "How does this count?", "analysis-task")
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Method == "" || len(analysis.Datasets) == 0 || len(analysis.Metrics) == 0 || len(analysis.Evidence) < 5 {
		t.Fatalf("paper was not analyzed: %+v", analysis)
	}
	for _, evidence := range analysis.Evidence {
		if evidence.PaperID != papers[0].ID || evidence.Section == "" || evidence.Snippet == "" || evidence.ProducingTask != "analysis-task" {
			t.Fatalf("untraceable evidence: %+v", evidence)
		}
	}
}

func TestMockProviderInaccessiblePaper(t *testing.T) {
	provider, _ := NewMockProvider(MockScenarioUnavailable)
	papers, err := provider.Search(context.Background(), SearchRequest{Query: "unavailable", MaxResults: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) == 0 || papers[0].FullTextAvailable {
		t.Fatalf("fixture does not describe an inaccessible paper: %+v", papers)
	}
	if _, err := provider.Fetch(context.Background(), papers[0]); !errors.Is(err, ErrFullTextUnavailable) {
		t.Fatalf("expected inaccessible paper error, got %v", err)
	}
}

func TestParseDocumentRejectsInvalidContent(t *testing.T) {
	_, err := ParseDocument(FetchResult{Available: true, ContentType: "application/pdf"}, []byte("not a PDF"))
	if !errors.Is(err, ErrInvalidPaperContent) {
		t.Fatalf("expected invalid paper content, got %v", err)
	}
}
