package research

import (
	"context"
	"path/filepath"
	"testing"
)

func loadStage4EvalCorpus(t *testing.T) EvalCorpus {
	t.Helper()
	corpus, err := LoadEvalCorpus(filepath.Join("..", "..", "eval", "research", "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

func TestOfflineEvalDeterministicMetrics(t *testing.T) {
	corpus := loadStage4EvalCorpus(t)
	first, err := RunOfflineEval(context.Background(), corpus)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunOfflineEval(context.Background(), corpus)
	if err != nil {
		t.Fatal(err)
	}
	first.Metrics.Duration, second.Metrics.Duration = "", ""
	first.Metrics.DurationMillis, second.Metrics.DurationMillis = 0, 0
	if first.Metrics != second.Metrics {
		t.Fatalf("fixture metrics are nondeterministic:\n%+v\n%+v", first.Metrics, second.Metrics)
	}
	if first.Metrics.TotalTasks < 10 || first.Metrics.PassedTasks != first.Metrics.TotalTasks || first.Metrics.UnsupportedFindings < 1 || first.Metrics.HallucinatedReferenceCount < 1 {
		t.Fatalf("incomplete eval metrics: %+v", first.Metrics)
	}
	if first.Metrics.InputTokens != nil || first.Metrics.OutputTokens != nil {
		t.Fatalf("fixture eval claimed real token usage: %+v", first.Metrics)
	}
}

func TestOfflineEvalRecoveryUnsupportedAndHallucinationScenarios(t *testing.T) {
	report, err := RunOfflineEval(context.Background(), loadStage4EvalCorpus(t))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]EvalTaskResult)
	for _, result := range report.Tasks {
		byID[result.ID] = result
	}
	for _, id := range []string{"insufficient-search-replan", "unavailable-paper-recovery"} {
		if result := byID[id]; !result.Passed || !result.RecoverySucceeded || result.Replans != 1 {
			t.Fatalf("recovery scenario %s failed: %+v", id, result)
		}
	}
	if result := byID["unsupported-quantitative-claim"]; !result.Passed || result.UnsupportedFindings != 1 {
		t.Fatalf("unsupported claim scenario failed: %+v", result)
	}
	if result := byID["citation-hallucination"]; !result.Passed || result.HallucinatedReferences != 1 {
		t.Fatalf("citation hallucination scenario failed: %+v", result)
	}
}
