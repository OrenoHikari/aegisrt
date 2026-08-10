package research

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

func TestBuildRunSummaryAggregatesRealObservations(t *testing.T) {
	result := orchestrator.LoopResult{
		RunID: "run-summary", Goal: "review papers", Replans: 1,
		Iterations: []orchestrator.IterationResult{{
			Version: 1,
			Plan:    planner.Plan{Tasks: []planner.Task{{ID: "search", Capability: "literature.search"}}},
			Observations: []orchestrator.Observation{
				{RunID: "run-summary", Iteration: 1, TaskID: "search", Capability: "literature.search", Success: true, Output: map[string]any{
					"query": "Evidence", "papers": []any{
						map[string]any{"id": "paper-1", "metadata_sources": []any{"arxiv", "crossref"}},
						map[string]any{"id": "paper-2", "metadata_sources": []any{"arxiv"}},
					},
				}},
				{RunID: "run-summary", Iteration: 1, TaskID: "fetch", Capability: "paper.fetch", Success: true, Output: map[string]any{"available": true}},
				{RunID: "run-summary", Iteration: 1, TaskID: "parse", Capability: "paper.parse", Success: true, Output: map[string]any{
					"sections":    []any{map[string]any{"id": "section-1"}},
					"diagnostics": map[string]any{"selected": "pypdf", "attempted": []any{"python-pypdf"}, "page_count": float64(12), "extracted_characters": float64(50000), "detected_sections": float64(7), "duration_ms": float64(90), "fallback_used": false, "truncated": false, "warning_count": float64(0)},
				}},
				{RunID: "run-summary", Iteration: 1, TaskID: "analysis", Capability: "paper.analyze", Success: true, Output: map[string]any{
					"llm_calls": float64(1), "llm_failures": float64(0),
					"candidate_findings": []any{map[string]any{"claim": "one"}, map[string]any{"claim": "two"}},
					"findings": []any{
						map[string]any{"status": "SUPPORTED", "evidence_id": "e1"},
						map[string]any{"status": "UNSUPPORTED", "evidence_id": "e2", "reason": "claim exceeds evidence"},
					},
					"usage": map[string]any{"input_tokens": float64(100), "output_tokens": float64(30)},
				}},
				{RunID: "run-summary", Iteration: 1, TaskID: "synthesis", Capability: "research.synthesize", Success: true, Output: map[string]any{
					"facts": []any{map[string]any{"kind": "FACT"}}, "inferences": []any{map[string]any{"kind": "INFERENCE"}},
					"references": []any{map[string]any{"id": "paper-1"}, map[string]any{"id": "paper-2"}},
				}},
				{RunID: "run-summary", Iteration: 1, TaskID: "design", Capability: "experiment.design", Success: true, Output: map[string]any{
					"hypothesis": map[string]any{"kind": "PROPOSAL"}, "baseline_suggestions": []any{map[string]any{}}, "datasets": []any{}, "metrics": []any{}, "ablation_plan": []any{map[string]any{}}, "evaluation_protocol": []any{}, "expected_risks": []any{},
				}},
				{RunID: "run-summary", Iteration: 1, TaskID: "report", Capability: "research.report", Success: true, Output: map[string]any{"references": float64(2), "citation_closed": true}},
			},
		}},
	}
	budget := ResearchBudget{MaxPapers: 3, MaxLLMCalls: 10, MaxAnalysisCallsPerPaper: 1, MaxReplans: 3, MaxRuntime: time.Minute, MaxContextBytes: 4096}
	summary, failures := BuildRunSummary(result, nil, 1500*time.Millisecond, budget, "real")
	if summary.Planning.PlansGenerated != 1 || summary.Planning.Replans != 1 || summary.Search.Queries != 1 || summary.Search.PapersRetrieved != 2 || summary.Search.PapersDeduplicated != 1 {
		t.Fatalf("unexpected planning/search summary: %+v", summary)
	}
	if summary.Paper.PDFsRequested != 1 || summary.Paper.PDFsAvailable != 1 || summary.Paper.ParsedSuccessfully != 1 || len(summary.Paper.Diagnostics) != 1 {
		t.Fatalf("unexpected paper summary: %+v", summary.Paper)
	}
	if summary.LLM.Calls != 1 || summary.LLM.InputTokens == nil || *summary.LLM.InputTokens != 100 || summary.Evidence.Candidates != 2 || summary.Evidence.SourceVerified != 2 || summary.Evidence.Supported != 1 || summary.Evidence.Rejected != 1 {
		t.Fatalf("unexpected LLM/evidence summary: llm=%+v evidence=%+v", summary.LLM, summary.Evidence)
	}
	if summary.Report.Facts != 1 || summary.Report.Inferences != 1 || summary.Report.Proposals != 3 || summary.Report.References != 2 ||
		summary.Report.RetrievedReferences != 2 || summary.Report.ValidMetadataReferences != 2 || summary.Report.HallucinatedReferences != 0 ||
		summary.Report.DuplicatedReferences != 0 || !summary.Report.CitationClosure {
		t.Fatalf("unexpected report summary: %+v", summary.Report)
	}
	if len(failures) != 1 || failures[0].Category != FailureClaimUnsupported || !failures[0].Recovered {
		t.Fatalf("unexpected failure cases: %+v", failures)
	}
	root := t.TempDir()
	summaryPath, failurePath, err := WriteRunArtifacts(root, summary, failures)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{summaryPath, failurePath} {
		data, err := os.ReadFile(path)
		if err != nil || !json.Valid(data) || filepath.Dir(path) != root {
			t.Fatalf("invalid run artifact %s: %v", path, err)
		}
	}
}

func TestFailureClassification(t *testing.T) {
	tests := []struct {
		capability string
		reason     string
		output     map[string]any
		want       FailureCategory
	}{
		{"literature.search", "network unavailable", nil, FailureSearch},
		{"paper.fetch", "paper full text unavailable", map[string]any{"available": false}, FailurePDFUnavailable},
		{"paper.fetch", "paper download exceeds limit", map[string]any{"available": false, "failure_code": "PDF_LIMIT_EXCEEDED"}, FailurePDFLimit},
		{"paper.parse", "malformed PDF", nil, FailurePDFParse},
		{"paper.analyze", "model response invalid", nil, FailureLLMAnalysis},
		{"research.report", "citation closure failed", nil, FailureCitationReject},
		{"literature.search", "HTTP 429 too many requests", nil, FailureProviderRate},
		{"", "maximum Agent replans exceeded", nil, FailureReplanExhausted},
		{"", "LLM call budget exceeded", nil, FailureContextLimit},
		{"", "something else", nil, FailureUnknown},
	}
	for _, test := range tests {
		if got := ClassifyFailure(test.capability, test.reason, test.output); got != test.want {
			t.Errorf("ClassifyFailure(%q, %q)=%s, want %s", test.capability, test.reason, got, test.want)
		}
	}
	result := orchestrator.LoopResult{RunID: "failed-run", Goal: "goal"}
	_, failures := BuildRunSummary(result, errors.New("maximum Agent replans exceeded"), time.Second,
		ResearchBudget{MaxPapers: 3, MaxLLMCalls: 8, MaxAnalysisCallsPerPaper: 1, MaxReplans: 3, MaxRuntime: time.Minute, MaxContextBytes: 4096}, "real")
	if len(failures) != 1 || failures[0].Category != FailureReplanExhausted || failures[0].Recovered {
		t.Fatalf("run failure classification was wrong: %+v", failures)
	}
}

func TestObservationFailureCategories(t *testing.T) {
	tests := []struct {
		name        string
		observation orchestrator.Observation
		want        FailureCategory
	}{
		{
			name: "section",
			observation: orchestrator.Observation{
				Capability: "paper.parse", Success: true,
				Output: map[string]any{"sections": []any{}},
			},
			want: FailureSection,
		},
		{
			name: "evidence",
			observation: orchestrator.Observation{
				Capability: "paper.analyze", Success: true,
				Output: map[string]any{"findings": []any{map[string]any{"status": "UNSUPPORTED", "reason": "snippet missing"}}},
			},
			want: FailureEvidenceReject,
		},
		{
			name: "claim",
			observation: orchestrator.Observation{
				Capability: "paper.analyze", Success: true,
				Output: map[string]any{"findings": []any{map[string]any{"status": "UNSUPPORTED", "evidence_id": "e1", "reason": "not entailed"}}},
			},
			want: FailureClaimUnsupported,
		},
		{
			name: "citation",
			observation: orchestrator.Observation{
				Capability: "research.report", Success: true,
				Output: map[string]any{"citation_closed": false, "hallucinated_references": float64(1)},
			},
			want: FailureCitationReject,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := failuresFromObservation(test.observation, false)
			if len(failures) != 1 || failures[0].Category != test.want {
				t.Fatalf("unexpected failures: %+v", failures)
			}
		})
	}
}

func TestResearchBudgetValidation(t *testing.T) {
	valid := ResearchBudget{MaxPapers: 3, MaxLLMCalls: 8, MaxAnalysisCallsPerPaper: 1, MaxReplans: 3, MaxRuntime: time.Minute, MaxContextBytes: 4096}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.MaxLLMCalls = 4
	if err := invalid.Validate(); err == nil {
		t.Fatal("budget without planning/decision reserve was accepted")
	}
	invalid = valid
	invalid.MaxContextBytes = MaximumAnalysisContextBytes + 1
	if err := invalid.Validate(); err == nil {
		t.Fatal("oversized analysis context was accepted")
	}
}
