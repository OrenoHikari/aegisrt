package research

import (
	"context"
	"encoding/json"
	"testing"

	"aegisrt/internal/llm"
)

func verifierDocument(t *testing.T) PaperDocument {
	t.Helper()
	text := "Method\nMethod A uses a language-conditioned density decoder for counting.\nResults\nMethod A reports 8.7 MAE on PhraseCount."
	document, err := (BasicGoParser{}).Parse(context.Background(), FetchResult{
		Paper: Paper{ID: "paper-a", Title: "Paper A", URL: "https://example.test/a", Provider: "test"},
		Query: "counting", Available: true, ContentType: "text/plain",
	}, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestEvidenceVerifierExactAndWhitespaceNormalized(t *testing.T) {
	document := verifierDocument(t)
	section := document.Sections[0]
	for _, snippet := range []string{
		"Method A uses a language-conditioned density decoder for counting.",
		"Method A   uses a language-conditioned\n density decoder for counting.",
	} {
		candidate := CandidateFinding{Claim: "Method A uses a language-conditioned density decoder for counting.", ClaimType: "method", PaperID: document.Paper.ID, SectionID: section.ID, EvidenceText: snippet}
		findings, evidence := (EvidenceVerifier{}).Verify(context.Background(), document, []CandidateFinding{candidate}, "verify-task")
		if len(findings) != 1 || findings[0].Status != FindingSupported || len(evidence) != 1 {
			t.Fatalf("evidence was not supported: findings=%+v evidence=%+v", findings, evidence)
		}
		canonical := evidence[0]
		if canonical.Snippet != section.Text[canonical.Start:canonical.End] || canonical.SectionID != section.ID || canonical.Status != FindingSupported {
			t.Fatalf("evidence is not canonical: %+v", canonical)
		}
	}
}

func TestEvidenceVerifierRejectsWrongSourceCoordinates(t *testing.T) {
	document := verifierDocument(t)
	section := document.Sections[0]
	tests := []CandidateFinding{
		{Claim: "claim", ClaimType: "method", PaperID: "paper-b", SectionID: section.ID, EvidenceText: section.Text},
		{Claim: "claim", ClaimType: "method", PaperID: document.Paper.ID, SectionID: "missing", EvidenceText: section.Text},
		{Claim: "claim", ClaimType: "method", PaperID: document.Paper.ID, SectionID: section.ID, EvidenceText: "This text does not exist."},
		{Claim: "cross-paper claim", ClaimType: "method", PaperID: "other-paper", SectionID: section.ID, EvidenceText: "Method A uses a language-conditioned density decoder for counting."},
	}
	findings, evidence := (EvidenceVerifier{}).Verify(context.Background(), document, tests, "verify-task")
	if len(findings) != len(tests) || len(evidence) != 0 {
		t.Fatalf("invalid candidates created evidence: %+v %+v", findings, evidence)
	}
	for _, finding := range findings {
		if finding.Status != FindingUnsupported || finding.EvidenceID != "" {
			t.Fatalf("invalid source was accepted: %+v", finding)
		}
	}
}

func TestDeterministicClaimSupportStates(t *testing.T) {
	checker := DeterministicClaimSupport{}
	tests := []struct {
		claim, source string
		want          ClaimSupport
	}{
		{"Method A uses density estimation.", "Method A uses density estimation.", ClaimSupported},
		{"Method A achieves 99.9% accuracy.", "Method A is evaluated on a benchmark.", ClaimUnsupported},
		{"alpha beta gamma delta epsilon", "alpha beta and other material", ClaimUncertain},
	}
	for _, test := range tests {
		decision, _, err := checker.Check(context.Background(), CandidateFinding{Claim: test.claim}, Evidence{Snippet: test.source})
		if err != nil || decision != test.want {
			t.Errorf("claim=%q decision=%s err=%v", test.claim, decision, err)
		}
	}
}

type responseLLM struct {
	response llm.Response
	err      error
}

func (c responseLLM) Generate(context.Context, llm.Request) (llm.Response, error) {
	return c.response, c.err
}

func TestLLMClaimSupportRejectsInvalidResponse(t *testing.T) {
	checker := LLMClaimSupportChecker{Client: responseLLM{response: llm.Response{Content: `{"decision":"YES","reason":"invalid"}`}}}
	if _, _, err := checker.Check(context.Background(), CandidateFinding{Claim: "claim"}, Evidence{Snippet: "source"}); err == nil {
		t.Fatal("invalid support decision was accepted")
	}
	valid, _ := json.Marshal(map[string]string{"decision": "UNCERTAIN", "reason": "not enough support"})
	checker.Client = responseLLM{response: llm.Response{Content: string(valid)}}
	decision, _, err := checker.Check(context.Background(), CandidateFinding{Claim: "claim"}, Evidence{Snippet: "source"})
	if err != nil || decision != ClaimUncertain {
		t.Fatalf("valid uncertain response rejected: %s %v", decision, err)
	}
}
