package research

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func fixtureAnalyses(t *testing.T, count int) []PaperAnalysis {
	t.Helper()
	provider, err := NewMockProvider(MockScenarioNormal)
	if err != nil {
		t.Fatal(err)
	}
	papers, err := provider.Search(context.Background(), SearchRequest{Query: "research direction", MaxResults: count})
	if err != nil {
		t.Fatal(err)
	}
	analyses := make([]PaperAnalysis, 0, len(papers))
	for index, paper := range papers {
		document, fetchErr := provider.Fetch(context.Background(), paper)
		if fetchErr != nil {
			t.Fatal(fetchErr)
		}
		parsed, parseErr := ParseDocument(FetchResult{
			Paper: paper, Query: "research direction", Available: true, ContentType: document.ContentType,
		}, document.Data)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		analysis, analysisErr := AnalyzePaper(parsed, "compare the field", "analysis-"+paper.ID)
		if analysisErr != nil {
			t.Fatal(analysisErr)
		}
		if index == 1 {
			analysis.Query = "expanded research direction"
		}
		analyses = append(analyses, analysis)
	}
	return analyses
}

func TestSynthesizeMultiplePapers(t *testing.T) {
	synthesis, err := Synthesize("compare the field", fixtureAnalyses(t, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(synthesis.References) != 3 || len(synthesis.MethodComparison) != 3 || len(synthesis.Evidence) == 0 || len(synthesis.Facts) == 0 {
		t.Fatalf("incomplete synthesis: %+v", synthesis)
	}
	if len(synthesis.Inferences) == 0 || len(synthesis.QueryHistory) != 2 {
		t.Fatalf("expected cross-paper inference and query history: %+v", synthesis)
	}
}

func TestSynthesizeRejectsInsufficientEvidence(t *testing.T) {
	_, err := Synthesize("goal", fixtureAnalyses(t, 1))
	if !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("expected insufficient evidence, got %v", err)
	}
}

func TestSynthesizeSkipsAnalysisWithoutSupportedEvidence(t *testing.T) {
	analyses := fixtureAnalyses(t, 3)
	for index := range analyses[1].Evidence {
		analyses[1].Evidence[index].Status = FindingVerifiedSource
	}
	synthesis, err := Synthesize("goal", analyses)
	if err != nil {
		t.Fatal(err)
	}
	if len(synthesis.References) != 2 || len(synthesis.MetadataOnlyPapers) != 1 {
		t.Fatalf("unsupported analysis was not downgraded to metadata-only: %+v", synthesis)
	}
	if synthesis.MetadataOnlyPapers[0].ID != analyses[1].Paper.ID {
		t.Fatalf("wrong metadata-only paper: %+v", synthesis.MetadataOnlyPapers)
	}
}

func TestSynthesizeStillRequiresTwoSupportedPapers(t *testing.T) {
	analyses := fixtureAnalyses(t, 2)
	for index := range analyses[1].Evidence {
		analyses[1].Evidence[index].Status = FindingUnsupported
	}
	if _, err := Synthesize("goal", analyses); !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("expected insufficient evidence after filtering, got %v", err)
	}
}

func TestCitationReportUsesOnlyRetrievedPapers(t *testing.T) {
	synthesis, err := Synthesize("goal", fixtureAnalyses(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	design, err := DesignExperiment("goal", "", synthesis)
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport("goal", synthesis, design)
	if err != nil {
		t.Fatal(err)
	}
	for _, paper := range synthesis.References {
		if !strings.Contains(report, paper.Title) || !strings.Contains(report, paper.URL) {
			t.Fatalf("retrieved reference missing from report: %s", paper.ID)
		}
	}
	if strings.Contains(report, "nonexistent-paper") {
		t.Fatal("report invented a reference")
	}
}

func TestCitationRejectsNonexistentReference(t *testing.T) {
	synthesis, err := Synthesize("goal", fixtureAnalyses(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	synthesis.References = append(synthesis.References, Paper{
		ID: "nonexistent-paper", Title: "Invented", Authors: []string{"Nobody"}, Year: 2025,
		URL: "https://example.invalid/nonexistent", Provider: "invented",
	})
	if err := ValidateSynthesis(synthesis); !errors.Is(err, ErrInvalidCitation) {
		t.Fatalf("expected nonexistent reference rejection, got %v", err)
	}
}

func TestExperimentDesignSeparatesProposalFromEvidence(t *testing.T) {
	synthesis, err := Synthesize("goal", fixtureAnalyses(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	design, err := DesignExperiment("goal", "small compute budget", synthesis)
	if err != nil {
		t.Fatal(err)
	}
	findings := append([]Finding{design.Hypothesis}, design.BaselineSuggestions...)
	findings = append(findings, design.AblationPlan...)
	for _, finding := range findings {
		if finding.Kind != FindingProposal || len(finding.EvidenceIDs) != 0 {
			t.Fatalf("experiment suggestion was represented as a published fact: %+v", finding)
		}
	}
}
