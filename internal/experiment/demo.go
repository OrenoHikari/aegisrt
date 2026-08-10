package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aegisrt/internal/contextstore"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/outputtxn"
	"aegisrt/internal/planner"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

type DemoOptions struct {
	Root                string
	WorkspaceRoot       string
	DatasetPath         string
	ExperimentDirectory string
	Executable          string
	Workers             int
	MaxReplans          int
	TaskTimeout         time.Duration
	LoopTimeout         time.Duration
	EnableCgroup        bool
	WorkScale           int
}

func RunDemo(ctx context.Context, goal string, options DemoOptions) (DemoResult, error) {
	started := time.Now()
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = DefaultGoal
	}
	if options.Root == "" {
		options.Root = filepath.Join("var", "experiment-demo")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return DemoResult{}, err
	}
	options.Root = root
	if options.WorkspaceRoot == "" {
		options.WorkspaceRoot = "."
	}
	if options.DatasetPath == "" {
		options.DatasetPath = filepath.ToSlash(filepath.Join("examples", "experiment", "classification.csv"))
	}
	if options.Workers <= 0 {
		options.Workers = 3
	}
	if options.MaxReplans <= 0 {
		options.MaxReplans = 3
	}
	if options.TaskTimeout <= 0 {
		options.TaskTimeout = 15 * time.Second
	}
	if options.LoopTimeout <= 0 {
		options.LoopTimeout = time.Minute
	}
	workScale, err := normalizeWorkScale(options.WorkScale)
	if err != nil {
		return DemoResult{}, err
	}
	options.WorkScale = workScale
	if err := os.MkdirAll(root, 0o755); err != nil {
		return DemoResult{}, err
	}

	registry, err := NewRegistry(RegistrationOptions{
		Executable: options.Executable, WorkspaceRoot: options.WorkspaceRoot,
		TaskTimeout: options.TaskTimeout, MemoryLimit: defaultMemoryLimitBytes,
		WorkScale: options.WorkScale,
	})
	if err != nil {
		return DemoResult{}, err
	}
	model := &OfflineDemoModel{
		Goal: goal, DatasetPath: options.DatasetPath,
		ExperimentDirectory: strings.TrimSpace(options.ExperimentDirectory),
	}
	taskPlanner, err := planner.New(model, registry.Capabilities())
	if err != nil {
		return DemoResult{}, err
	}
	controller, err := orchestrator.NewLLMController(model, registry.Capabilities())
	if err != nil {
		return DemoResult{}, err
	}

	outputManager, err := outputtxn.Open(filepath.Join(root, "outputs"), outputtxn.DefaultLimits())
	if err != nil {
		return DemoResult{}, fmt.Errorf("open experiment output store: %w", err)
	}
	telemetryPath := filepath.Join(root, "runtime-events.jsonl")
	jsonlSink, err := telemetry.OpenJSONLSink(telemetryPath)
	if err != nil {
		return DemoResult{}, fmt.Errorf("open experiment telemetry: %w", err)
	}
	eventBus, err := telemetry.NewBus(1024, jsonlSink)
	if err != nil {
		_ = jsonlSink.Close()
		return DemoResult{}, err
	}
	busClosed := false
	defer func() {
		if busClosed {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = eventBus.Close(closeCtx)
	}()

	agentLog, err := os.OpenFile(filepath.Join(root, "agent-events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return DemoResult{}, err
	}
	defer agentLog.Close()
	var resourceManager *resource.Manager
	if options.EnableCgroup {
		resourceManager, err = resource.NewManagerFromCurrent()
		if err != nil {
			return DemoResult{}, fmt.Errorf("discover delegated cgroup: %w", err)
		}
		if err := resourceManager.Initialize(); err != nil {
			return DemoResult{}, fmt.Errorf("initialize cgroup manager: %w", err)
		}
	}
	baseRunner := &agentRuntime.Runner{Log: agentLog, Resources: resourceManager}
	outputExecutor, err := agentRuntime.NewTransactionalOutputExecutor(baseRunner, outputManager, agentRuntime.OutputRetainOnFailure)
	if err != nil {
		return DemoResult{}, err
	}
	executor, err := NewFailureAwareExecutor(outputExecutor)
	if err != nil {
		return DemoResult{}, err
	}
	runtimeScheduler, err := scheduler.NewWithOptions(executor, scheduler.Options{
		WorkerCount: options.Workers, QueueSize: 32, Policy: scheduler.NewCAPSPolicy(), PressureSource: pressure.NewReader(),
		ContextRegistry: contextstore.NewRegistry(), ContextResolver: contextstore.PassthroughResolver{},
		OutputVerifier: outputManager, EventPublisher: eventBus,
	})
	if err != nil {
		return DemoResult{}, err
	}
	agentOrchestrator, err := orchestrator.New(runtimeScheduler, registry, eventBus)
	if err != nil {
		return DemoResult{}, err
	}
	loop, err := orchestrator.NewAgentLoop(taskPlanner, controller, agentOrchestrator, registry, eventBus, orchestrator.LoopOptions{
		MaxReplans: options.MaxReplans, Timeout: options.LoopTimeout, PlanValidator: PlanPolicy{},
	})
	if err != nil {
		return DemoResult{}, err
	}

	loopResult, runErr := loop.Run(ctx, goal)
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := eventBus.Close(closeCtx)
	cancel()
	busClosed = true
	if closeErr != nil {
		runErr = errors.Join(runErr, closeErr)
	}
	summary := buildSummary(loopResult, runErr, telemetryPath, time.Since(started))
	if model.ExperimentDirectory != "" {
		summary.PlannerMode = "OFFLINE_MANIFEST_DRIVEN_LLM_FIXTURE"
	}
	if runErr == nil {
		reportPath, exportErr := exportReport(root, loopResult)
		if exportErr != nil {
			runErr = exportErr
			summary.Status = "FAILED"
			summary.Error = exportErr.Error()
		} else {
			summary.ReportPath = reportPath
		}
	}
	if err := writeSummary(root, summary); err != nil {
		runErr = errors.Join(runErr, err)
	}
	return DemoResult{Loop: loopResult, Summary: summary}, runErr
}

func buildSummary(result orchestrator.LoopResult, runErr error, telemetryPath string, duration time.Duration) RunSummary {
	summary := RunSummary{
		RunID: result.RunID, Goal: result.Goal, Status: "COMPLETED", ExecutionMode: "LOCAL_REAL",
		PlannerMode: "OFFLINE_DETERMINISTIC_LLM_FIXTURE", Replans: result.Replans,
		TelemetryPath: telemetryPath, DurationMS: duration.Milliseconds(),
	}
	if runErr != nil {
		summary.Status = "FAILED"
		summary.Error = runErr.Error()
	}
	methods := make(map[string]MethodResult)
	for _, iteration := range result.Iterations {
		for _, observation := range iteration.Observations {
			if observation.Capability != CapabilityRun {
				continue
			}
			if observation.Success {
				encoded, _ := json.Marshal(observation.Output)
				var method MethodResult
				if json.Unmarshal(encoded, &method) == nil && method.Method != "" {
					methods[method.Method] = method
				}
				continue
			}
			encoded, _ := json.Marshal(observation.Output)
			var failure FailureObservation
			if json.Unmarshal(encoded, &failure) == nil && failure.Validate() == nil {
				summary.FailedAttempts = append(summary.FailedAttempts, failure)
			}
		}
	}
	for _, method := range []string{MethodLogisticRegression, MethodRandomForest, MethodSVM} {
		if result, exists := methods[method]; exists {
			summary.Experiments = append(summary.Experiments, result)
		}
	}
	for _, method := range summary.Experiments {
		if method.Accuracy > 0 && (summary.BestMethod == "" || method.Accuracy > accuracyFor(summary.Experiments, summary.BestMethod)) {
			summary.BestMethod = method.Method
			summary.BestName = method.DisplayName
		}
	}
	return summary
}

func accuracyFor(results []MethodResult, method string) float64 {
	for _, result := range results {
		if result.Method == method {
			return result.Accuracy
		}
	}
	return 0
}

func exportReport(root string, result orchestrator.LoopResult) (string, error) {
	if len(result.Iterations) == 0 {
		return "", fmt.Errorf("experiment report iteration is missing")
	}
	last := result.Iterations[len(result.Iterations)-1]
	reportTaskID := ""
	for _, task := range last.Plan.Tasks {
		if task.Capability == CapabilityReport {
			reportTaskID = task.ID
		}
	}
	for _, record := range last.Execution.Records {
		if record.ID != reportTaskID || !record.OutputVerified {
			continue
		}
		data, err := os.ReadFile(filepath.Join(record.OutputCommitPath, "experiment_report.md"))
		if err != nil {
			return "", err
		}
		reportPath := filepath.Join(root, "experiment_report.md")
		if err := os.WriteFile(reportPath, data, 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(root, "report.md"), data, 0o600); err != nil {
			return "", err
		}
		return reportPath, nil
	}
	return "", fmt.Errorf("verified experiment report output is missing")
}

func writeSummary(root string, summary RunSummary) error {
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "experiment-summary.json"), encoded, 0o600)
}

func SortedOutputIDs(outputs map[string]string) []string {
	ids := make([]string, 0, len(outputs))
	for id := range outputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
