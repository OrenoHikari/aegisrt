package research

import (
	"errors"
	"strings"
	"testing"
)

func closedReportFixture(t *testing.T) (Synthesis, ExperimentDesign, string) {
	t.Helper()
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
	return synthesis, design, report
}

func TestReportClosureValidAndNonexistentCitation(t *testing.T) {
	synthesis, design, report := closedReportFixture(t)
	if err := ValidateReportClosure(report, synthesis, design); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"# Research Readiness", "Answer completeness", "# Method Comparison", "Limitations"} {
		if !strings.Contains(report, expected) {
			t.Fatalf("report omitted quality/comparison section %q", expected)
		}
	}
	if strings.Contains(report, "Reproduce the  route") {
		t.Fatal("report emitted an empty experiment baseline")
	}
	mutated := strings.Replace(report, "[P1]", "[P999]", 1)
	if err := ValidateReportClosure(mutated, synthesis, design); !errors.Is(err, ErrInvalidCitation) {
		t.Fatalf("nonexistent citation accepted: %v", err)
	}
}

func TestReportClosureRejectsFactWithoutEvidenceAndHallucinatedMetadata(t *testing.T) {
	synthesis, design, report := closedReportFixture(t)
	withoutEvidence := strings.Replace(report, "# Evidence-backed Findings\n\n", "# Evidence-backed Findings\n\n- **FACT:** An invented unsupported result.\n", 1)
	if err := ValidateReportClosure(withoutEvidence, synthesis, design); !errors.Is(err, ErrInvalidCitation) {
		t.Fatalf("FACT without evidence accepted: %v", err)
	}
	hallucinated := strings.Replace(report, synthesis.References[0].Title, "Hallucinated Paper Title", 1)
	if err := ValidateReportClosure(hallucinated, synthesis, design); !errors.Is(err, ErrInvalidCitation) {
		t.Fatalf("hallucinated metadata accepted: %v", err)
	}
}

func TestReportClosureRejectsUnsupportedFact(t *testing.T) {
	synthesis, design, _ := closedReportFixture(t)
	synthesis.Evidence[0].Status = FindingVerifiedSource
	if _, err := BuildReport("goal", synthesis, design); !errors.Is(err, ErrInvalidCitation) {
		t.Fatalf("unsupported FACT accepted: %v", err)
	}
}

func TestReportClosureRejectsInventedFactWithBorrowedEvidence(t *testing.T) {
	synthesis, design, _ := closedReportFixture(t)
	synthesis.Facts[0].Statement = "An invented quantitative result that the cited evidence never states."
	if _, err := BuildReport("goal", synthesis, design); !errors.Is(err, ErrInvalidCitation) {
		t.Fatalf("invented FACT borrowed unrelated evidence: %v", err)
	}
}
