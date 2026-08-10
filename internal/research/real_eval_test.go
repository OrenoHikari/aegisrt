package research

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealEvalConfigAndPendingGoldParsing(t *testing.T) {
	suite, err := LoadRealEvalSuite(filepath.Join("..", "..", "eval", "research", "real-small.json"))
	if err != nil {
		t.Fatal(err)
	}
	if suite.Name != "real-small" || len(suite.Goals) != 5 {
		t.Fatalf("unexpected real suite: %+v", suite)
	}
	annotations, err := LoadGoldAnnotations(filepath.Join("..", "..", "eval", "research", "gold-annotations.json"), suite)
	if err != nil {
		t.Fatal(err)
	}
	metrics := EvaluateReviewedEvidence(annotations)
	if metrics.ReviewedFindings != 0 || metrics.EvidencePrecision != nil || metrics.RecallAvailable {
		t.Fatalf("incomplete gold produced invented metrics: %+v", metrics)
	}
	output := filepath.Join(t.TempDir(), "real-eval-review.md")
	if err := WriteHumanReviewTemplate(suite, annotations, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"Relevance", "Correctness", "Evidence Quality", "Coverage", "Experiment Usefulness", "Evidence exists?", "rec-representative-methods"} {
		if !strings.Contains(text, expected) {
			t.Errorf("review template is missing %q", expected)
		}
	}
}

func TestGoldAnnotationValidationAndReviewedPrecision(t *testing.T) {
	suite := RealEvalSuite{Version: 1, Name: "test-real", Mode: "real", Goals: []RealEvalGoal{{
		ID: "goal-1", Category: "single-task", Goal: "Review a real task", MaxPapers: 3,
	}}}
	findings := []GoldFinding{
		{Claim: "claim one", Section: "Methods", Evidence: "evidence one", Reviewed: true, EvidenceExists: true, SupportsClaim: true, AttributionCorrect: true},
		{Claim: "claim two", Section: "Results", Evidence: "evidence two", Reviewed: true, EvidenceExists: true, SupportsClaim: false, AttributionCorrect: true},
		{Claim: "claim three", Section: "Discussion", Evidence: "evidence three"},
	}
	annotations := GoldAnnotationSet{Version: 1, Suite: suite.Name, Goals: []GoldGoalAnnotation{{
		GoalID: "goal-1", Status: "COMPLETE", Papers: []GoldPaperAnnotation{{PaperID: "paper-1", Findings: findings}},
	}}}
	path := filepath.Join(t.TempDir(), "gold.json")
	data, _ := json.Marshal(annotations)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGoldAnnotations(path, suite)
	if err != nil {
		t.Fatal(err)
	}
	metrics := EvaluateReviewedEvidence(loaded)
	if metrics.ReviewedFindings != 2 || metrics.CorrectlySupportedFindings != 1 || metrics.UnsupportedFindings != 1 ||
		metrics.EvidencePrecision == nil || *metrics.EvidencePrecision != 0.5 || metrics.RecallAvailable {
		t.Fatalf("unexpected reviewed evidence metrics: %+v", metrics)
	}
	loaded.Goals[0].Papers[0].Findings = loaded.Goals[0].Papers[0].Findings[:2]
	invalid, _ := json.Marshal(loaded)
	if err := os.WriteFile(path, invalid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoldAnnotations(path, suite); err == nil || !strings.Contains(err.Error(), "3-5 findings") {
		t.Fatalf("invalid gold annotations accepted: %v", err)
	}
}
