package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegisrt/internal/experiment"
	"aegisrt/internal/research"
	"aegisrt/internal/telemetry"
)

type executorFunc func(context.Context, RunSpec) error

func (function executorFunc) Execute(ctx context.Context, spec RunSpec) error {
	return function(ctx, spec)
}

func TestControllerNormalAndReplan(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		replan     bool
		wantPlans  int
		wantReplan int
	}{{"normal", false, 1, 0}, {"replan", true, 2, 1}} {
		t.Run(scenario.name, func(t *testing.T) {
			controller := newTestController(t, fakeSuccessfulExecutor(t, scenario.replan))
			view, err := controller.Create(CreateRunRequest{Goal: "research a bounded topic", Mode: "mock", Scenario: "normal"})
			if err != nil {
				t.Fatal(err)
			}
			view = waitForTerminal(t, controller, view.ID)
			if view.Status != StatusCompleted || view.Progress.SupportedFindings != 1 {
				t.Fatalf("unexpected completed view: %+v", view)
			}
			plans, err := controller.Plans(view.ID)
			if err != nil || len(plans.Versions) != scenario.wantPlans || view.Progress.Replans != scenario.wantReplan {
				t.Fatalf("unexpected replan projection: plans=%+v view=%+v err=%v", plans, view, err)
			}
			if scenario.replan && view.Decision.ReplanReason == "" {
				t.Fatal("replan reason was not retained")
			}
		})
	}
}

func TestControllerFailureConcurrentRunAndCancellation(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		controller := newTestController(t, executorFunc(func(context.Context, RunSpec) error { return errors.New("provider timeout") }))
		view, err := controller.Create(CreateRunRequest{Goal: "goal", Mode: "mock"})
		if err != nil {
			t.Fatal(err)
		}
		view = waitForTerminal(t, controller, view.ID)
		if view.Status != StatusFailed || !strings.Contains(view.Error, "provider timeout") {
			t.Fatalf("unexpected failure view: %+v", view)
		}
	})
	t.Run("concurrent and cancellation", func(t *testing.T) {
		started := make(chan struct{})
		controller := newTestController(t, executorFunc(func(ctx context.Context, spec RunSpec) error {
			line := telemetryLine(t, 1, telemetry.KindPlanCreated, "", "", map[string]any{
				"iteration": 1, "version": 1, "plan_tasks": []map[string]any{{
					"id": "pending", "name": "Pending", "capability": "literature.search", "depends_on": []string{},
				}},
			})
			if err := os.WriteFile(filepath.Join(spec.Root, "runtime-events.jsonl"), line, 0o600); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}))
		view, err := controller.Create(CreateRunRequest{Goal: "goal", Mode: "mock"})
		if err != nil {
			t.Fatal(err)
		}
		<-started
		if _, err := controller.Create(CreateRunRequest{Goal: "other", Mode: "mock"}); !errors.Is(err, ErrRunActive) {
			t.Fatalf("expected active-run rejection, got %v", err)
		}
		if err := controller.Cancel(view.ID); err != nil {
			t.Fatal(err)
		}
		view = waitForTerminal(t, controller, view.ID)
		if view.Status != StatusCancelled {
			t.Fatalf("status = %s", view.Status)
		}
		plan, err := controller.Plans(view.ID)
		if err != nil || len(plan.Versions) != 1 || plan.Versions[0].Tasks[0].Status != "CANCELLED" {
			t.Fatalf("cancelled plan projection = %+v, err=%v", plan, err)
		}
	})
}

func TestControllerRejectsInvalidRequests(t *testing.T) {
	controller := newTestController(t, executorFunc(func(context.Context, RunSpec) error { return nil }))
	for _, request := range []CreateRunRequest{
		{}, {Goal: "goal", Mode: "unsafe"}, {Goal: "goal", Mode: "mock", Scenario: "unknown"}, {Goal: "goal", Mode: "mock", MaxPDFMB: 65},
		{Goal: "goal", Mode: "mock", ExperimentDirectory: "examples/experiment"},
		{Goal: "goal", Workload: "experiment", ExperimentDirectory: "/tmp/outside"},
		{Goal: "goal", Workload: "experiment", ExperimentDirectory: "../outside"},
		{Goal: "goal", Workload: "experiment", ExperimentDirectory: "safe/../outside"},
	} {
		if _, err := controller.Create(request); err == nil {
			t.Fatalf("request should fail: %+v", request)
		}
	}
}

func TestNormalizeExperimentDirectory(t *testing.T) {
	for input, expected := range map[string]string{
		".":                         ".",
		"./examples//experiment":    "examples/experiment",
		"fixtures/local experiment": "fixtures/local experiment",
	} {
		actual, err := normalizeExperimentDirectory(input)
		if err != nil || actual != expected {
			t.Fatalf("normalize %q = %q, %v; want %q", input, actual, err, expected)
		}
	}
	for _, input := range []string{"", "/absolute", `C:\outside`, `\\server\share`, "../outside", "safe/../outside", `safe\..\outside`, `examples\experiment`, strings.Repeat("a", maximumExperimentDirectory+1)} {
		if actual, err := normalizeExperimentDirectory(input); err == nil {
			t.Fatalf("unsafe experiment directory %q normalized to %q", input, actual)
		}
	}
}

func TestControllerPassesUserSelectedPDFBudgetToResearchCLI(t *testing.T) {
	specs := make(chan RunSpec, 1)
	controller := newTestController(t, executorFunc(func(_ context.Context, spec RunSpec) error {
		specs <- spec
		return errors.New("stop after argument capture")
	}))
	view, err := controller.Create(CreateRunRequest{Goal: "research", Mode: "mock", MaxPDFMB: 48})
	if err != nil {
		t.Fatal(err)
	}
	spec := <-specs
	view = waitForTerminal(t, controller, view.ID)
	if view.MaxPDFMB != 48 {
		t.Fatalf("persisted PDF budget = %d", view.MaxPDFMB)
	}
	joined := strings.Join(spec.Arguments, " ")
	if !strings.Contains(joined, "--max-pdf-mb 48") {
		t.Fatalf("research CLI did not receive PDF budget: %v", spec.Arguments)
	}
}

func TestControllerRunsAutonomousExperimentPresetAsLocalRealExecution(t *testing.T) {
	specs := make(chan RunSpec, 1)
	controller := newTestController(t, executorFunc(func(_ context.Context, spec RunSpec) error {
		specs <- spec
		return writeFakeExperimentArtifacts(t, spec.Root)
	}))
	view, err := controller.Create(CreateRunRequest{
		Goal: "检查指定目录的实验设置并比较三个方法", Workload: "experiment", Mode: "mock", Scenario: "normal",
		ExperimentDirectory: "./examples//experiment",
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := <-specs
	view = waitForTerminal(t, controller, view.ID)
	if view.Status != StatusCompleted || view.Workload != "experiment" || view.Mode != "local" || view.Experiment == nil {
		t.Fatalf("experiment view = %+v", view)
	}
	if view.Goal != "检查指定目录的实验设置并比较三个方法" || view.ExperimentDirectory != DefaultExperimentDirectory {
		t.Fatalf("experiment goal/directory = %q / %q", view.Goal, view.ExperimentDirectory)
	}
	if view.Experiment.BestMethod != experiment.MethodRandomForest || len(view.Failures) != 1 || !view.Failures[0].Recovered {
		t.Fatalf("experiment result/failure = %+v / %+v", view.Experiment, view.Failures)
	}
	joined := strings.Join(spec.Arguments, " ")
	if spec.Workload != "experiment" || !strings.Contains(joined, "experiment demo") || !strings.Contains(joined, "--work-scale 2000") || strings.Contains(joined, "agent research") {
		t.Fatalf("experiment CLI spec = %+v", spec)
	}
	if argumentValue(spec.Arguments, "--workspace-root") != controller.options.WorkDir || argumentValue(spec.Arguments, "--experiment-dir") != DefaultExperimentDirectory {
		t.Fatalf("experiment workspace arguments = %v", spec.Arguments)
	}
	plans, err := controller.Plans(view.ID)
	if err != nil || len(plans.Versions) != 2 || plans.Versions[1].Tasks[0].Change != "REUSED" {
		t.Fatalf("experiment plans = %+v, err=%v", plans, err)
	}
	report, err := controller.Report(view.ID)
	if err != nil || !strings.Contains(string(report), "Random Forest") {
		t.Fatalf("experiment report err=%v body=%s", err, report)
	}
}

func TestControllerPersistsExperimentDirectoryInHistory(t *testing.T) {
	root := t.TempDir()
	options := Options{
		Root: root, WorkDir: t.TempDir(), DefaultMode: "mock",
		Executor: executorFunc(func(_ context.Context, spec RunSpec) error {
			return writeFakeExperimentArtifacts(t, spec.Root)
		}),
		PollInterval: time.Millisecond, LoopTimeout: time.Second,
	}
	controller, err := NewController(options)
	if err != nil {
		t.Fatal(err)
	}
	view, err := controller.Create(CreateRunRequest{
		Goal: "run settings from a local directory", Workload: "experiment",
		ExperimentDirectory: "fixtures/dynamic-case",
	})
	if err != nil {
		t.Fatal(err)
	}
	view = waitForTerminal(t, controller, view.ID)
	if view.ExperimentDirectory != "fixtures/dynamic-case" {
		t.Fatalf("completed experiment directory = %q", view.ExperimentDirectory)
	}
	restored, err := NewController(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = controller.Close(ctx)
		_ = restored.Close(ctx)
	})
	history := restored.List()
	if len(history) != 1 || history[0].ID != view.ID || history[0].ExperimentDirectory != "fixtures/dynamic-case" {
		t.Fatalf("restored experiment history = %+v", history)
	}
}

func TestControllerWaitsForProcessFinalizationAfterCognitiveCompletion(t *testing.T) {
	started := make(chan RunSpec, 1)
	release := make(chan struct{})
	controller := newTestController(t, executorFunc(func(_ context.Context, spec RunSpec) error {
		started <- spec
		<-release
		return writeFakeExperimentArtifacts(t, spec.Root)
	}))
	view, err := controller.Create(CreateRunRequest{
		Goal: experiment.DefaultGoal, Workload: "experiment",
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	controller.mu.RLock()
	store := controller.runs[view.ID].Events
	controller.mu.RUnlock()
	if _, err := store.AddRaw(telemetryLine(t, 1, telemetry.KindGoalCompleted, "", "", map[string]any{
		"run_id": "cognitive-finalizing",
	})); err != nil {
		t.Fatal(err)
	}
	view, err = controller.View(view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Status.Terminal() || view.Status != StatusSynthesizing {
		t.Fatalf("cognitive completion exposed premature terminal status: %s", view.Status)
	}
	if _, err := controller.Create(CreateRunRequest{Goal: "must remain blocked", Mode: "mock"}); !errors.Is(err, ErrRunActive) {
		t.Fatalf("process was still finalizing but second run error = %v", err)
	}
	close(release)
	view = waitForTerminal(t, controller, view.ID)
	if view.Status != StatusCompleted || view.Experiment == nil {
		t.Fatalf("finalized experiment view = %+v", view)
	}
}

func TestControllerRejectsResearchBudgetBelowDAGMinimum(t *testing.T) {
	_, err := NewController(Options{Root: t.TempDir(), DefaultMode: "mock", MaxPapers: 1})
	if err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("expected research DAG budget rejection, got %v", err)
	}
}

func TestControllerRejectsInvalidExperimentWorkScale(t *testing.T) {
	for _, scale := range []int{-1, experiment.MaximumWorkScale + 1} {
		_, err := NewController(Options{
			Root: t.TempDir(), DefaultMode: "mock", ExperimentWorkScale: scale,
		})
		if err == nil || !strings.Contains(err.Error(), "work scale") {
			t.Fatalf("expected experiment work scale %d rejection, got %v", scale, err)
		}
	}
}

func TestControllerPersistsTerminalArtifactsAndRestoresHistory(t *testing.T) {
	root := t.TempDir()
	options := Options{
		Root: root, DefaultMode: "mock", Executor: fakeSuccessfulExecutor(t, true),
		PollInterval: time.Millisecond, LoopTimeout: time.Second,
	}
	controller, err := NewController(options)
	if err != nil {
		t.Fatal(err)
	}
	view, err := controller.Create(CreateRunRequest{Goal: "persist this research run", Mode: "mock", Scenario: "search-replan"})
	if err != nil {
		t.Fatal(err)
	}
	view = waitForTerminal(t, controller, view.ID)
	runRoot := filepath.Join(root, "runs", view.ID)
	for _, name := range []string{
		"dashboard-run.json", "runtime-events.jsonl", "plan.json", "papers.json",
		"evidence.json", "run-summary.json", "failure-cases.json", "report.md",
	} {
		info, statErr := os.Stat(filepath.Join(runRoot, name))
		if statErr != nil {
			t.Fatalf("persisted artifact %s: %v", name, statErr)
		}
		if info.Size() == 0 {
			t.Fatalf("persisted artifact %s is empty", name)
		}
	}

	restored, err := NewController(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = controller.Close(ctx)
		_ = restored.Close(ctx)
	})
	history := restored.List()
	if len(history) != 1 || history[0].ID != view.ID || history[0].Status != StatusCompleted || history[0].Summary == nil {
		t.Fatalf("restored history = %+v", history)
	}
	plans, err := restored.Plans(view.ID)
	if err != nil || len(plans.Versions) != 2 || plans.Versions[1].Tasks[0].Status != "REUSED" {
		t.Fatalf("restored plan = %+v, err=%v", plans, err)
	}
	artifacts, err := restored.Artifacts(view.ID)
	if err != nil || artifacts.Evidence.SupportedCount != 1 || artifacts.Evidence.RejectedCount != 1 {
		t.Fatalf("restored evidence = %+v, err=%v", artifacts.Evidence, err)
	}

	relocatedRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(relocatedRoot, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(runRoot, filepath.Join(relocatedRoot, "runs", view.ID)); err != nil {
		t.Fatal(err)
	}
	relocatedOptions := options
	relocatedOptions.Root = relocatedRoot
	relocated, err := NewController(relocatedOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = relocated.Close(ctx)
	})
	relocatedArtifacts, err := relocated.Artifacts(view.ID)
	if err != nil || relocatedArtifacts.Evidence.SupportedCount != 1 || relocatedArtifacts.Evidence.RejectedCount != 1 {
		t.Fatalf("relocated persisted evidence = %+v, err=%v", relocatedArtifacts.Evidence, err)
	}
	relocatedPlan, err := relocated.Plans(view.ID)
	if err != nil || len(relocatedPlan.Versions) != 2 || relocatedPlan.Versions[1].Tasks[0].Status != "REUSED" {
		t.Fatalf("relocated persisted plan = %+v, err=%v", relocatedPlan, err)
	}
}

func TestReadPresetsValidatesConfiguredCompetitionScenarios(t *testing.T) {
	valid := `[
		{"id":"real","name":"Real","description":"live","goal":"research","mode":"real"},
		{"id":"replan","name":"Replan","goal":"recover","mode":"mock","scenario":"search-replan"},
		{"id":"guard","name":"Guard","goal":"verify","mode":"mock","scenario":"evidence-rejection"},
		{"id":"experiment","name":"Experiment","goal":"compare","workload":"experiment","mode":"local","scenario":"resource-replan"}
	]`
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	presets, err := readPresets(path)
	if err != nil || len(presets) != 4 || presets[0].Mode != "real" || presets[2].Scenario != "evidence-rejection" || presets[3].Workload != "experiment" || presets[3].ExperimentDirectory != DefaultExperimentDirectory {
		t.Fatalf("presets = %+v, err=%v", presets, err)
	}
	invalid := []string{
		`[{"id":"dup","name":"One","goal":"a","mode":"real"},{"id":"dup","name":"Two","goal":"b","mode":"real"}]`,
		`[{"id":"real","name":"Real","goal":"a","mode":"real","scenario":"normal"}]`,
		`[{"id":"mock","name":"Mock","goal":"a","mode":"mock","scenario":"invented"}]`,
		`[{"id":"extra","name":"Extra","goal":"a","mode":"real","unknown":true}]`,
		`[{"id":"experiment","name":"Experiment","goal":"a","workload":"experiment","mode":"mock","scenario":"resource-replan"}]`,
		`[{"id":"experiment","name":"Experiment","goal":"a","workload":"experiment","mode":"local","scenario":"resource-replan","experiment_directory":"../outside"}]`,
		`[{"id":"research","name":"Research","goal":"a","mode":"real","experiment_directory":"examples/experiment"}]`,
	}
	for index, document := range invalid {
		if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPresets(path); err == nil {
			t.Fatalf("invalid preset document %d was accepted", index)
		}
	}
}

func argumentValue(arguments []string, name string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func TestFailureDiagnosticsAreStructuredAndRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dashboard-run.log")
	if err := os.WriteFile(path, []byte("setup\ncapsulectl: provider timeout Authorization: Bearer deep-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	diagnostic := readProcessDiagnostic(path)
	if !strings.Contains(diagnostic, "provider timeout") || strings.Contains(diagnostic, "deep-secret") {
		t.Fatalf("unsafe diagnostic = %q", diagnostic)
	}
	if code := failureDisplayCode(research.FailureSearch, diagnostic); code != "PROVIDER_TIMEOUT" {
		t.Fatalf("failure code = %s", code)
	}
	if code := failureDisplayCode(research.FailureUnknown, "provider request timeout"); code != "PROVIDER_TIMEOUT" {
		t.Fatalf("fallback failure code = %s", code)
	}
}

func newTestController(t *testing.T, executor RunExecutor) *Controller {
	t.Helper()
	controller, err := NewController(Options{
		Root: t.TempDir(), DefaultMode: "mock", Executor: executor,
		PollInterval: time.Millisecond, LoopTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = controller.Close(ctx)
	})
	return controller
}

func waitForTerminal(t *testing.T, controller *Controller, id string) RunView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := controller.View(id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status.Terminal() {
			controller.mu.RLock()
			active := controller.activeID
			controller.mu.RUnlock()
			if active == "" {
				return view
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not reach a terminal state")
	return RunView{}
}

func fakeSuccessfulExecutor(t *testing.T, replan bool) RunExecutor {
	t.Helper()
	return executorFunc(func(_ context.Context, spec RunSpec) error {
		commitPath, err := writeFakeArtifacts(spec.Root)
		if err != nil {
			return err
		}
		var lines [][]byte
		sequence := uint64(1)
		add := func(kind telemetry.Kind, taskID, phase string, data any) {
			lines = append(lines, telemetryLine(t, sequence, kind, taskID, phase, data))
			sequence++
		}
		planTasks := []map[string]any{{"id": "search", "name": "Search", "capability": "literature.search", "depends_on": []string{}}}
		add(telemetry.KindPlanCreated, "", "", map[string]any{"run_id": "cognitive-test", "iteration": 1, "version": 1, "plan_tasks": planTasks})
		add(telemetry.KindAgentSubmitted, "search", "QUEUED", map[string]any{"metadata": map[string]string{"iteration": "1"}})
		add(telemetry.KindAgentDispatched, "search", "RUNNING", map[string]any{"metadata": map[string]string{"iteration": "1"}})
		add(telemetry.KindPressureSampled, "search", "RUNNING", map[string]any{"snapshot": map[string]any{"cpu": map[string]any{"some": map[string]any{"avg10": 0.1}}, "memory": map[string]any{"some": map[string]any{"avg10": 0.2}}}})
		add(telemetry.KindOutputVerified, "analysis", "SUCCEEDED", map[string]any{"output_verified": true, "output_commit_path": commitPath})
		add(telemetry.KindAgentFinished, "search", "SUCCEEDED", map[string]any{"metadata": map[string]string{"iteration": "1"}})
		if replan {
			add(telemetry.KindDecisionMade, "", "", map[string]any{"run_id": "cognitive-test", "iteration": 1, "decision": "REPLAN", "reason": "insufficient evidence"})
			add(telemetry.KindReplanRequested, "", "", map[string]any{"run_id": "cognitive-test", "iteration": 1, "reason": "insufficient evidence"})
			add(telemetry.KindPlanRevised, "", "", map[string]any{"run_id": "cognitive-test", "iteration": 2, "version": 2, "plan_tasks": []map[string]any{
				{"id": "search", "name": "Search", "capability": "literature.search", "depends_on": []string{}},
				{"id": "analyze", "name": "Analyze", "capability": "paper.analyze", "depends_on": []string{"search"}},
			}})
			add(telemetry.KindObservationCreated, "search", "SUCCEEDED", map[string]any{"iteration": 2, "success": true, "reused": true})
		}
		iteration := 1
		if replan {
			iteration = 2
		}
		add(telemetry.KindDecisionMade, "", "", map[string]any{"run_id": "cognitive-test", "iteration": iteration, "decision": "GOAL_COMPLETED", "reason": "verified"})
		add(telemetry.KindGoalCompleted, "", "", map[string]any{"run_id": "cognitive-test", "iteration": iteration})
		var data []byte
		for _, line := range lines {
			data = append(data, line...)
		}
		return os.WriteFile(filepath.Join(spec.Root, "runtime-events.jsonl"), data, 0o600)
	})
}

func writeFakeExperimentArtifacts(t *testing.T, root string) error {
	t.Helper()
	sequence := uint64(1)
	var lines []byte
	add := func(kind telemetry.Kind, taskID, phase string, data any) {
		lines = append(lines, telemetryLine(t, sequence, kind, taskID, phase, data)...)
		sequence++
	}
	planV1 := []map[string]any{
		{"id": "dataset", "name": "Dataset", "capability": experiment.CapabilityDatasetPrepare, "depends_on": []string{}},
		{"id": "rf-large", "name": "Random Forest large", "capability": experiment.CapabilityRun, "depends_on": []string{"dataset"}},
	}
	planV2 := []map[string]any{
		{"id": "dataset", "name": "Dataset", "capability": experiment.CapabilityDatasetPrepare, "depends_on": []string{}},
		{"id": "rf-retry", "name": "Random Forest retry", "capability": experiment.CapabilityRun, "depends_on": []string{"dataset"}},
	}
	add(telemetry.KindPlanCreated, "", "", map[string]any{"run_id": "experiment-test", "iteration": 1, "version": 1, "plan_tasks": planV1})
	add(telemetry.KindAgentDispatched, "rf-large", "RUNNING", map[string]any{"role": experiment.CapabilityRun, "metadata": map[string]string{"iteration": "1"}})
	add(telemetry.KindAgentFinished, "rf-large", "FAILED", map[string]any{"role": experiment.CapabilityRun, "metadata": map[string]string{"iteration": "1"}, "resource_spec": map[string]any{"memory_max_bytes": 64 << 20}})
	add(telemetry.KindObservationCreated, "rf-large", "FAILED", map[string]any{
		"iteration": 1, "capability": experiment.CapabilityRun, "failure_code": experiment.FailureMemoryLimitExceeded,
		"memory_peak_bytes": 92 << 20, "memory_limit_bytes": 64 << 20,
	})
	add(telemetry.KindDecisionMade, "", "", map[string]any{"run_id": "experiment-test", "iteration": 1, "decision": "REPLAN", "reason": "memory limit"})
	add(telemetry.KindReplanRequested, "", "", map[string]any{"run_id": "experiment-test", "iteration": 1, "reason": "memory limit"})
	add(telemetry.KindPlanRevised, "", "", map[string]any{"run_id": "experiment-test", "iteration": 2, "version": 2, "plan_tasks": planV2})
	add(telemetry.KindObservationCreated, "dataset", "SUCCEEDED", map[string]any{"iteration": 2, "success": true, "reused": true})
	add(telemetry.KindAgentDispatched, "rf-retry", "RUNNING", map[string]any{"role": experiment.CapabilityRun, "metadata": map[string]string{"iteration": "2"}})
	add(telemetry.KindAgentFinished, "rf-retry", "SUCCEEDED", map[string]any{"role": experiment.CapabilityRun, "metadata": map[string]string{"iteration": "2"}, "resource_spec": map[string]any{"memory_max_bytes": 64 << 20}})
	add(telemetry.KindDecisionMade, "", "", map[string]any{"run_id": "experiment-test", "iteration": 2, "decision": "GOAL_COMPLETED", "reason": "verified"})
	add(telemetry.KindGoalCompleted, "", "", map[string]any{"run_id": "experiment-test", "iteration": 2})
	if err := os.WriteFile(filepath.Join(root, "runtime-events.jsonl"), lines, 0o600); err != nil {
		return err
	}
	summary := experiment.RunSummary{
		RunID: "experiment-test", Goal: experiment.DefaultGoal, Status: "COMPLETED",
		ExecutionMode: "LOCAL_REAL", PlannerMode: "OFFLINE_DETERMINISTIC_LLM_FIXTURE", Replans: 1,
		Experiments: []experiment.MethodResult{
			{Method: experiment.MethodLogisticRegression, DisplayName: "Logistic Regression", Accuracy: 0.86, RuntimeMS: 2, MemoryPeakBytes: 18 << 20, MemoryLimitBytes: 64 << 20, Attempt: 1, Status: "SUCCEEDED"},
			{Method: experiment.MethodRandomForest, DisplayName: "Random Forest", Accuracy: 0.91, RuntimeMS: 4, MemoryPeakBytes: 28 << 20, MemoryLimitBytes: 64 << 20, Attempt: 2, Status: "SUCCEEDED"},
			{Method: experiment.MethodSVM, DisplayName: "SVM", Accuracy: 0.88, RuntimeMS: 3, MemoryPeakBytes: 24 << 20, MemoryLimitBytes: 64 << 20, Attempt: 1, Status: "SUCCEEDED"},
		},
		FailedAttempts: []experiment.FailureObservation{{
			TaskID: "rf-large", Method: experiment.MethodRandomForest, Attempt: 1, Status: "FAILED",
			FailureCode: experiment.FailureMemoryLimitExceeded, Reason: "92 MiB exceeds 64 MiB", Retryable: true,
			MemoryPeakBytes: 92 << 20, MemoryLimitBytes: 64 << 20,
		}},
		BestMethod: experiment.MethodRandomForest, BestName: "Random Forest", DurationMS: 12,
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "experiment-summary.json"), encoded, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "report.md"), []byte("# Experiment\n\nBest method: Random Forest\n"), 0o600)
}

func writeFakeArtifacts(root string) (string, error) {
	paper := research.Paper{ID: "paper-1", Title: "Grounded paper", Authors: []string{"Researcher"}, Year: 2025, Provider: "mock"}
	analysis := research.PaperAnalysis{
		Paper: paper,
		CandidateFindings: []research.CandidateFinding{
			{Claim: "Supported claim", PaperID: paper.ID, SectionID: "results", EvidenceText: "measured result"},
			{Claim: "Unsupported claim", PaperID: paper.ID, SectionID: "results", EvidenceText: "measured result"},
		},
		Findings: []research.VerifiedFinding{
			{Candidate: research.CandidateFinding{Claim: "Supported claim", PaperID: paper.ID, SectionID: "results", EvidenceText: "measured result"}, Status: research.FindingSupported, EvidenceID: "ev-1"},
			{Candidate: research.CandidateFinding{Claim: "Unsupported claim", PaperID: paper.ID, SectionID: "results", EvidenceText: "measured result"}, Status: research.FindingUnsupported, Reason: "claim exceeds evidence support"},
		},
		Evidence: []research.Evidence{{ID: "ev-1", PaperID: paper.ID, Source: "mock", Claim: "Supported claim", Section: "Results", SectionID: "results", Snippet: "measured result", Status: research.FindingSupported, ProducingTask: "paper-analyze-1"}},
	}
	data, err := json.Marshal(analysis)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, "outputs", "committed", "analysis", "txn")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(directory, "result.json"), data, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("# Verified report\n\nSupported claim [ev-1].\n"), 0o600); err != nil {
		return "", err
	}
	summary := research.RunSummary{
		Goal: "research a bounded topic", Mode: "mock", Duration: "10ms", DurationMS: 10,
		Paper:    research.PaperRunSummary{ParsedSuccessfully: 1},
		Evidence: research.EvidenceRunSummary{Candidates: 2, SourceVerified: 1, Supported: 1, Rejected: 1},
		Report:   research.ReportRunSummary{Facts: 1, References: 1, CitationClosure: true},
	}
	summaryData, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "run-summary.json"), summaryData, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "failure-cases.json"), []byte("[]\n"), 0o600); err != nil {
		return "", err
	}
	return directory, nil
}
