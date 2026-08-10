package research

import (
	"strings"
	"testing"
)

func TestEvidenceRejectionMockAddsRejectedCandidateOnlyToSelectedTask(t *testing.T) {
	analysis := PaperAnalysis{Paper: Paper{ID: "paper-1"}}
	appendEvidenceRejectionDemo(&analysis, MockScenarioEvidenceReject, "normal-p1-analysis")
	if len(analysis.CandidateFindings) != 1 || len(analysis.Findings) != 1 {
		t.Fatalf("rejected candidate missing: %+v", analysis)
	}
	if analysis.Findings[0].Status != FindingUnsupported || analysis.Findings[0].EvidenceID != "" ||
		!strings.Contains(analysis.Findings[0].Reason, "does not exist") {
		t.Fatalf("candidate was not deterministically rejected: %+v", analysis.Findings[0])
	}
	if len(analysis.Evidence) != 0 {
		t.Fatalf("rejected candidate created evidence: %+v", analysis.Evidence)
	}
	untouched := PaperAnalysis{Paper: Paper{ID: "paper-2"}}
	appendEvidenceRejectionDemo(&untouched, MockScenarioEvidenceReject, "normal-p2-analysis")
	if len(untouched.Findings) != 0 {
		t.Fatalf("scenario modified more than one deterministic task: %+v", untouched)
	}
}
