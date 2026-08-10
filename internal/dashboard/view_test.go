package dashboard

import (
	"testing"
	"time"
)

func TestBuildPlanRuntimeAndDecisionViews(t *testing.T) {
	events := []DashboardEvent{
		{Sequence: 1, Kind: "cognitive.plan.created", Data: map[string]any{"version": float64(1), "iteration": float64(1), "plan_tasks": []any{
			map[string]any{"id": "search", "name": "Search", "capability": "literature.search", "depends_on": []any{}},
			map[string]any{"id": "analyze", "name": "Analyze", "capability": "paper.analyze", "depends_on": []any{"search"}},
		}}},
		{Sequence: 2, Kind: "runtime.agent.finished", TaskID: "search", Phase: "SUCCEEDED", Data: map[string]any{"metadata": map[string]any{"iteration": "1"}}},
		{Sequence: 3, Kind: "runtime.agent.finished", TaskID: "analyze", Phase: "FAILED", Data: map[string]any{"metadata": map[string]any{"iteration": "1"}}},
		{Sequence: 4, Kind: "cognitive.decision.made", Data: map[string]any{"iteration": float64(1), "decision": "REPLAN", "reason": "insufficient evidence"}},
		{Sequence: 5, Kind: "cognitive.plan.revised", Data: map[string]any{"version": float64(2), "iteration": float64(2), "plan_tasks": []any{
			map[string]any{"id": "search", "name": "Search", "capability": "literature.search", "depends_on": []any{}},
			map[string]any{"id": "analyze-2", "name": "Analyze new corpus", "capability": "paper.analyze", "depends_on": []any{"search"}},
		}}},
		{Sequence: 6, Kind: "cognitive.observation.created", TaskID: "search", Data: map[string]any{"iteration": float64(2), "reused": true, "success": true}},
		{Sequence: 7, Kind: "runtime.agent.dispatched", TaskID: "analyze-2", Phase: "RUNNING", Data: map[string]any{"metadata": map[string]any{"iteration": "2"}}},
		{Sequence: 8, Kind: "runtime.pressure.sampled", Data: map[string]any{"snapshot": map[string]any{
			"cpu":    map[string]any{"some": map[string]any{"avg10": 1.25}},
			"memory": map[string]any{"some": map[string]any{"avg10": 0.5}},
		}}},
		{Sequence: 9, Kind: "cognitive.decision.made", Data: map[string]any{"iteration": float64(2), "decision": "GOAL_COMPLETED", "reason": "verified"}},
	}
	plan := BuildPlanSnapshot(events)
	if len(plan.Versions) != 2 || plan.Versions[0].Tasks[1].Status != "FAILED" {
		t.Fatalf("unexpected plan projection: %+v", plan)
	}
	if plan.Versions[1].Tasks[0].Status != "REUSED" || plan.Versions[1].Tasks[0].Change != "REUSED" {
		t.Fatalf("completed task reuse missing: %+v", plan.Versions[1].Tasks[0])
	}
	if len(plan.Versions[1].Removed) != 1 || plan.Versions[1].Removed[0] != "analyze" {
		t.Fatalf("removed task missing: %+v", plan.Versions[1].Removed)
	}
	runtime := BuildRuntimeSnapshot(events)
	if runtime.RunningTasks != 1 || runtime.FailedTasks != 1 || runtime.SucceededTasks != 1 || runtime.CPUPressureAvg10 == nil {
		t.Fatalf("unexpected runtime projection: %+v", runtime)
	}
	decision := BuildDecisionView(events)
	if decision.Type != "GOAL_COMPLETED" || decision.ReplanReason != "insufficient evidence" || decision.FromPlan != 1 || decision.ToPlan != 2 {
		t.Fatalf("unexpected decision view: %+v", decision)
	}
}

func TestCurrentDurationUsesTerminalTime(t *testing.T) {
	start := time.Now().Add(-time.Second)
	finish := start.Add(250 * time.Millisecond)
	if got := currentDuration(RunRecord{StartedAt: &start, FinishedAt: &finish}); got != 250 {
		t.Fatalf("duration = %d", got)
	}
}

func TestRuntimeSnapshotShowsHistoricalParallelismAndResourceEnvelope(t *testing.T) {
	base := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	spec := map[string]any{"cpu_quota_percent": float64(75), "memory_max_bytes": float64(256 * 1024 * 1024), "pids_max": float64(16)}
	events := []DashboardEvent{
		{Kind: "runtime.agent.submitted", TaskID: "a", Data: map[string]any{"resource_spec": spec}},
		{Kind: "runtime.agent.submitted", TaskID: "b", Data: map[string]any{"resource_spec": spec}},
		{Kind: "runtime.agent.dispatched", TaskID: "a", Timestamp: base, Phase: "RUNNING"},
		{Kind: "runtime.agent.dispatched", TaskID: "b", Timestamp: base.Add(10 * time.Millisecond), Phase: "RUNNING"},
		{Kind: "runtime.pressure.sampled", Data: map[string]any{"snapshot": map[string]any{
			"cpu": map[string]any{"some": map[string]any{"avg10": 1.8}}, "memory": map[string]any{"some": map[string]any{"avg10": 0.1}},
		}}},
		{Kind: "runtime.agent.finished", TaskID: "a", Timestamp: base.Add(50 * time.Millisecond), Phase: "SUCCEEDED"},
		{Kind: "runtime.agent.finished", TaskID: "b", Timestamp: base.Add(70 * time.Millisecond), Phase: "SUCCEEDED"},
	}
	runtime := BuildRuntimeSnapshot(events)
	if runtime.PeakParallelAgents != 2 || runtime.ExecutedTasks != 2 || runtime.ParallelWorkMS != 110 || runtime.ParallelWindowMS != 70 || runtime.ParallelSavedMS != 40 {
		t.Fatalf("parallel execution metrics are wrong: %+v", runtime)
	}
	if runtime.AverageParallelism < 1.5 || runtime.CPUQuotaPercent != 75 || runtime.MemoryMaxBytes != 256*1024*1024 || runtime.PeakCPUPressure == nil || *runtime.PeakCPUPressure != 1.8 {
		t.Fatalf("resource metrics are wrong: %+v", runtime)
	}
}
