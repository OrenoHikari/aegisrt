package research

import (
	"context"
	"encoding/json"
	"testing"

	"aegisrt/internal/llm"
)

func structuredAnalysisJSON(t *testing.T, candidate CandidateFinding) string {
	t.Helper()
	encoded, err := json.Marshal(structuredPaperAnalysis{
		Problem: "counting problem", Method: candidate.Claim,
		Contributions: []string{"grounded counting"}, Datasets: []string{"PhraseCount"},
		Metrics: []string{"MAE"}, Experiments: []string{"controlled split"},
		MainResults: []string{"8.7 MAE"}, Limitations: []string{"split mismatch"},
		RelevantFindings: []CandidateFinding{candidate},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestLLMPaperAnalysisValidStructuredOutput(t *testing.T) {
	document := verifierDocument(t)
	section := document.Sections[0]
	claim := "Method A uses a language-conditioned density decoder for counting."
	candidate := CandidateFinding{Claim: claim, ClaimType: "method", PaperID: document.Paper.ID, SectionID: section.ID, EvidenceText: claim, Confidence: 0.9}
	analyzer := LLMPaperAnalyzer{Client: responseLLM{response: llm.Response{Content: structuredAnalysisJSON(t, candidate), Usage: &llm.Usage{InputTokens: 100, OutputTokens: 30}}}}
	analysis, err := analyzer.Analyze(context.Background(), document, "how does it count?", "analysis-task")
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Method != claim || len(analysis.Evidence) != 1 || analysis.Evidence[0].Status != FindingSupported || analysis.Usage == nil || analysis.Usage.InputTokens != 100 {
		t.Fatalf("unexpected LLM analysis: %+v", analysis)
	}
}

func TestLLMPaperAnalysisMalformedOutput(t *testing.T) {
	document := verifierDocument(t)
	_, err := (LLMPaperAnalyzer{Client: responseLLM{response: llm.Response{Content: `{"problem":`}}}).Analyze(context.Background(), document, "question", "task")
	if err == nil {
		t.Fatal("malformed structured analysis was accepted")
	}
}

func TestLLMPaperAnalysisRejectsUnsupportedSectionAndEvidence(t *testing.T) {
	document := verifierDocument(t)
	tests := []CandidateFinding{
		{Claim: "invented method", ClaimType: "method", PaperID: document.Paper.ID, SectionID: "missing", EvidenceText: "invented evidence"},
		{Claim: "invented method", ClaimType: "method", PaperID: document.Paper.ID, SectionID: document.Sections[0].ID, EvidenceText: "invented evidence"},
	}
	for _, candidate := range tests {
		analysis, err := (LLMPaperAnalyzer{Client: responseLLM{response: llm.Response{Content: structuredAnalysisJSON(t, candidate)}}}).Analyze(context.Background(), document, "question", "task")
		if err != nil {
			t.Fatal(err)
		}
		if len(analysis.Findings) != 1 || analysis.Findings[0].Status != FindingUnsupported || len(analysis.Evidence) != 0 || analysis.Method != "" {
			t.Fatalf("unsupported source entered analysis: %+v", analysis)
		}
	}
}

func TestStructuredAnalysisRejectsUnknownFieldsAndInvalidConfidence(t *testing.T) {
	if _, err := ParseStructuredPaperAnalysis(`{"problem":"x","unknown":true}`, "paper-a"); err == nil {
		t.Fatal("unknown analysis field was accepted")
	}
	document := verifierDocument(t)
	candidate := CandidateFinding{Claim: "x", ClaimType: "method", PaperID: document.Paper.ID, SectionID: document.Sections[0].ID, EvidenceText: "x", Confidence: 2}
	if _, err := ParseStructuredPaperAnalysis(structuredAnalysisJSON(t, candidate), document.Paper.ID); err == nil {
		t.Fatal("invalid confidence was accepted")
	}
}
