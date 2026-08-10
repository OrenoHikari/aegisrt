package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "internal-experiment-worker" {
		if err := RunWorker(context.Background(), os.Args[2:]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRegistryValidatesMethodArgumentsAndDatasetBoundary(t *testing.T) {
	workspace := t.TempDir()
	dataset := filepath.Join(workspace, "data.csv")
	if err := os.WriteFile(dataset, []byte("x,label\n1,A\n2,B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(RegistrationOptions{WorkspaceRoot: workspace, WorkScale: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Capabilities()) != 5 {
		t.Fatalf("capability count = %d", len(registry.Capabilities()))
	}
	job, err := registry.Build(context.Background(), planner.Task{
		ID: "dataset", Name: "dataset", Description: "prepare", Capability: CapabilityDatasetPrepare,
		Arguments: map[string]any{"path": "data.csv"}, DependsOn: []string{},
	})
	if err != nil || job.Agent.Environment["CAPSULE_EXPERIMENT_DATASET"] != dataset {
		t.Fatalf("dataset job = %+v, err=%v", job.Agent, err)
	}
	if got := job.Agent.Environment["CAPSULE_EXPERIMENT_WORK_SCALE"]; got != "7" {
		t.Fatalf("work scale environment = %q", got)
	}
	invalid := []planner.Task{
		{ID: "escape", Name: "escape", Description: "escape", Capability: CapabilityDatasetPrepare, Arguments: map[string]any{"path": "../data.csv"}, DependsOn: []string{}},
		{ID: "method", Name: "method", Description: "method", Capability: CapabilityRun, Arguments: map[string]any{"method": "neural_network", "attempt": float64(1)}, DependsOn: []string{"dataset"}},
		{ID: "forest", Name: "forest", Description: "forest", Capability: CapabilityRun, Arguments: map[string]any{"method": MethodRandomForest, "attempt": float64(1), "n_estimators": float64(1001)}, DependsOn: []string{"dataset"}},
	}
	for _, task := range invalid {
		if _, err := registry.Build(context.Background(), task); err == nil {
			t.Fatalf("invalid task was accepted: %+v", task)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("x,label\n1,A\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "linked.csv")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := registry.Build(context.Background(), planner.Task{
			ID: "symlink", Name: "symlink", Description: "symlink", Capability: CapabilityDatasetPrepare,
			Arguments: map[string]any{"path": "linked.csv"}, DependsOn: []string{},
		}); err == nil {
			t.Fatal("symlink dataset escape was accepted")
		}
	}
	for _, scale := range []int{-1, MaximumWorkScale + 1} {
		if _, err := NewRegistry(RegistrationOptions{WorkspaceRoot: workspace, WorkScale: scale}); err == nil {
			t.Fatalf("invalid work scale %d was accepted", scale)
		}
	}
}

func TestWorkerNormalExecutionIntentionalFailureAnalysisAndReport(t *testing.T) {
	dataset := DatasetResult{Path: "data.csv", Rows: 18, Features: []string{"a", "b"}, Classes: []string{"A", "B"}, SHA256: "fixture"}
	datasetJSON, _ := json.Marshal(dataset)
	t.Run("normal execution", func(t *testing.T) {
		staging := t.TempDir()
		t.Setenv("CAPSULE_TASK_ID", "rf-ok")
		t.Setenv("CAPSULE_EXPERIMENT_MEMORY_LIMIT", fmt.Sprint(defaultMemoryLimitBytes))
		t.Setenv("CAPSULE_EXPERIMENT_WORK_SCALE", "2")
		err := runMethod(context.Background(), staging, map[string]any{
			"method": MethodRandomForest, "attempt": float64(2), "n_estimators": float64(100),
		}, []json.RawMessage{datasetJSON})
		if err != nil {
			t.Fatal(err)
		}
		var result MethodResult
		readJSONFile(t, filepath.Join(staging, "result.json"), &result)
		if result.Accuracy != 0.91 || result.Status != "SUCCEEDED" || result.RuntimeMS <= 0 {
			t.Fatalf("result = %+v", result)
		}
		if result.Parameters["work_scale"] != float64(2) {
			t.Fatalf("result work scale = %#v", result.Parameters["work_scale"])
		}
	})
	t.Run("intentional resource failure", func(t *testing.T) {
		staging := t.TempDir()
		t.Setenv("CAPSULE_TASK_ID", "rf-large")
		t.Setenv("CAPSULE_EXPERIMENT_MEMORY_LIMIT", fmt.Sprint(defaultMemoryLimitBytes))
		t.Setenv("CAPSULE_EXPERIMENT_WORK_SCALE", "3")
		err := runMethod(context.Background(), staging, map[string]any{
			"method": MethodRandomForest, "attempt": float64(1), "n_estimators": float64(1000),
		}, []json.RawMessage{datasetJSON})
		if err == nil || !strings.Contains(err.Error(), FailureMemoryLimitExceeded) {
			t.Fatalf("failure = %v", err)
		}
		var failure FailureObservation
		readJSONFile(t, filepath.Join(staging, "failure.json"), &failure)
		if failure.Validate() != nil || !failure.Retryable || failure.MemoryPeakBytes <= failure.MemoryLimitBytes {
			t.Fatalf("failure observation = %+v", failure)
		}
		if failure.Parameters["work_scale"] != float64(3) {
			t.Fatalf("failure work scale = %#v", failure.Parameters["work_scale"])
		}
	})

	results := []MethodResult{
		{Method: MethodLogisticRegression, DisplayName: "Logistic Regression", Status: "SUCCEEDED", Accuracy: 0.86, RuntimeMS: 2, MemoryPeakBytes: 18 << 20, MemoryLimitBytes: 64 << 20, Attempt: 1},
		{Method: MethodRandomForest, DisplayName: "Random Forest", Status: "SUCCEEDED", Accuracy: 0.91, RuntimeMS: 4, MemoryPeakBytes: 28 << 20, MemoryLimitBytes: 64 << 20, Attempt: 2},
		{Method: MethodSVM, DisplayName: "SVM", Status: "SUCCEEDED", Accuracy: 0.88, RuntimeMS: 3, MemoryPeakBytes: 24 << 20, MemoryLimitBytes: 64 << 20, Attempt: 1},
	}
	dependencies := make([]json.RawMessage, 0, len(results))
	for _, result := range results {
		encoded, _ := json.Marshal(result)
		dependencies = append(dependencies, encoded)
	}
	analysisDir := t.TempDir()
	if err := analyzeMethods(analysisDir, dependencies); err != nil {
		t.Fatal(err)
	}
	var analysis AnalysisResult
	readJSONFile(t, filepath.Join(analysisDir, "result.json"), &analysis)
	if analysis.BestMethod != MethodRandomForest {
		t.Fatalf("analysis = %+v", analysis)
	}
	analysisJSON, _ := json.Marshal(analysis)
	reportDir := t.TempDir()
	if err := writeReport(reportDir, map[string]any{
		"goal": "compare", "replans": float64(1), "retry_code": FailureMemoryLimitExceeded,
	}, []json.RawMessage{analysisJSON}); err != nil {
		t.Fatal(err)
	}
	report, err := os.ReadFile(filepath.Join(reportDir, "experiment_report.md"))
	if err != nil || !strings.Contains(string(report), "Best method: Random Forest") || !strings.Contains(string(report), FailureMemoryLimitExceeded) {
		t.Fatalf("report error=%v\n%s", err, report)
	}
}

func TestDeterministicCPUWorkScalesAndValidates(t *testing.T) {
	ctx := context.Background()
	scaled, err := deterministicCPUWork(ctx, 4, 3, 17)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := deterministicCPUWork(ctx, 12, 1, 17)
	if err != nil {
		t.Fatal(err)
	}
	if scaled != equivalent {
		t.Fatalf("scaled checksum %d != equivalent checksum %d", scaled, equivalent)
	}
	for _, scale := range []int{0, -1, MaximumWorkScale + 1} {
		if _, err := deterministicCPUWork(ctx, 1, scale, 17); err == nil {
			t.Fatalf("invalid work scale %d was accepted", scale)
		}
	}
	for _, value := range []string{"not-an-integer", "0", fmt.Sprint(MaximumWorkScale + 1)} {
		t.Setenv("CAPSULE_EXPERIMENT_WORK_SCALE", value)
		if _, err := environmentWorkScale(); err == nil {
			t.Fatalf("invalid environment work scale %q was accepted", value)
		}
	}
}

func TestRunDemoRejectsInvalidWorkScale(t *testing.T) {
	for _, scale := range []int{-1, MaximumWorkScale + 1} {
		_, err := RunDemo(context.Background(), DefaultGoal, DemoOptions{
			Root: t.TempDir(), WorkspaceRoot: repositoryRoot(t), WorkScale: scale,
		})
		if err == nil || !strings.Contains(err.Error(), "work scale") {
			t.Fatalf("work scale %d error = %v", scale, err)
		}
	}
}

func TestAutonomousExperimentDemoReplansReusesRetriesAndReports(t *testing.T) {
	workspace := repositoryRoot(t)
	root := t.TempDir()
	result, err := RunDemo(context.Background(), DefaultGoal, DemoOptions{
		Root: root, WorkspaceRoot: workspace, DatasetPath: "examples/experiment/classification.csv",
		Workers: 3, MaxReplans: 3, LoopTimeout: 30 * time.Second, WorkScale: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Status != "COMPLETED" || result.Loop.Replans != 1 || len(result.Loop.Iterations) != 2 {
		t.Fatalf("unexpected loop result: %+v", result.Summary)
	}
	if result.Summary.BestMethod != MethodRandomForest || len(result.Summary.Experiments) != 3 || len(result.Summary.FailedAttempts) != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if result.Summary.FailedAttempts[0].FailureCode != FailureMemoryLimitExceeded {
		t.Fatalf("failure = %+v", result.Summary.FailedAttempts[0])
	}
	if result.Summary.FailedAttempts[0].Parameters["work_scale"] != float64(2) {
		t.Fatalf("failed attempt work scale = %#v", result.Summary.FailedAttempts[0].Parameters["work_scale"])
	}
	for _, method := range result.Summary.Experiments {
		if method.Parameters["work_scale"] != float64(2) {
			t.Fatalf("%s work scale = %#v", method.Method, method.Parameters["work_scale"])
		}
	}
	second := result.Loop.Iterations[1]
	reused := make(map[string]bool)
	for _, id := range second.Execution.ReusedTaskIDs {
		reused[id] = true
	}
	for _, id := range []string{"dataset-prepare", "logistic-regression", "svm"} {
		if !reused[id] {
			t.Fatalf("successful task %s was not reused: %v", id, second.Execution.ReusedTaskIDs)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "experiment_report.md")); err != nil {
		t.Fatal(err)
	}
	telemetryData, err := os.ReadFile(filepath.Join(root, "runtime-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"runtime.agent.finished", "cognitive.observation.created", "cognitive.replan.requested", "cognitive.plan.revised", "cognitive.goal.completed"} {
		if !strings.Contains(string(telemetryData), kind) {
			t.Fatalf("telemetry missing %s", kind)
		}
	}
}

func TestAutonomousExperimentDemoHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RunDemo(ctx, DefaultGoal, DemoOptions{
		Root: t.TempDir(), WorkspaceRoot: repositoryRoot(t), DatasetPath: "examples/experiment/classification.csv",
	})
	if err == nil || (!errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "canceled")) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestOfflineModelUsesProductionPlanAndDecisionValidation(t *testing.T) {
	registry, err := NewRegistry(RegistrationOptions{WorkspaceRoot: repositoryRoot(t)})
	if err != nil {
		t.Fatal(err)
	}
	model := &OfflineDemoModel{Goal: DefaultGoal, DatasetPath: "examples/experiment/classification.csv"}
	taskPlanner, err := planner.New(model, registry.Capabilities())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := taskPlanner.Create(context.Background(), DefaultGoal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Validate(plan, registry.Capabilities()); err != nil {
		t.Fatal(err)
	}
	controller, err := orchestrator.NewLLMController(model, registry.Capabilities())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Decide(context.Background(), orchestrator.DecisionRequest{
		CurrentPlan: plan, Observations: []orchestrator.Observation{{TaskID: "unknown", Capability: CapabilityAnalyze, Success: false}},
	})
	if err != nil || decision.Type != orchestrator.DecisionFailed {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
