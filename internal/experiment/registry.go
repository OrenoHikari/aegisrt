package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
	"aegisrt/internal/resource"
	"aegisrt/internal/scheduler"
)

type RegistrationOptions struct {
	Executable    string
	WorkspaceRoot string
	TaskTimeout   time.Duration
	MemoryLimit   int64
	WorkScale     int
}

func NewRegistry(options RegistrationOptions) (*orchestrator.Registry, error) {
	registrations, err := Registrations(options)
	if err != nil {
		return nil, err
	}
	return orchestrator.NewRegistry(registrations)
}

func Registrations(options RegistrationOptions) ([]orchestrator.Registration, error) {
	executable := strings.TrimSpace(options.Executable)
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve experiment worker executable: %w", err)
		}
	}
	workspaceRoot := strings.TrimSpace(options.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	workspaceRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve experiment workspace root: %w", err)
	}
	workspaceRoot, err = filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("evaluate experiment workspace root: %w", err)
	}
	taskTimeout := options.TaskTimeout
	if taskTimeout <= 0 {
		taskTimeout = 15 * time.Second
	}
	memoryLimit := options.MemoryLimit
	if memoryLimit <= 0 {
		memoryLimit = defaultMemoryLimitBytes
	}
	workScale, err := normalizeWorkScale(options.WorkScale)
	if err != nil {
		return nil, err
	}

	build := func(capability, action string, demand scheduler.Demand) orchestrator.JobFactory {
		return func(ctx context.Context, task planner.Task) (scheduler.Job, error) {
			if err := validateTaskArguments(task); err != nil {
				return scheduler.Job{}, err
			}
			encoded, err := json.Marshal(task.Arguments)
			if err != nil {
				return scheduler.Job{}, err
			}
			acb := agent.New(task.ID, capability, executable, []string{"internal-experiment-worker", "--action", action})
			acb.Environment = map[string]string{
				"CAPSULE_TASK_ID":                 task.ID,
				"CAPSULE_TASK_CAPABILITY":         capability,
				"CAPSULE_TASK_ARGUMENTS_JSON":     string(encoded),
				"CAPSULE_EXPERIMENT_MEMORY_LIMIT": fmt.Sprint(memoryLimit),
				"CAPSULE_EXPERIMENT_WORK_SCALE":   fmt.Sprint(workScale),
				"CAPSULE_EXPERIMENT_WORKSPACE":    workspaceRoot,
			}
			switch capability {
			case CapabilityManifestInspect:
				requested := stringArgument(task.Arguments, "path")
				if err := validateWorkspaceRelativeRequest("experiment directory", requested, true); err != nil {
					return scheduler.Job{}, err
				}
				path, err := resolveManifestDirectoryPath(workspaceRoot, requested)
				if err != nil {
					return scheduler.Job{}, err
				}
				acb.Environment["CAPSULE_EXPERIMENT_MANIFEST_DIR"] = path
			case CapabilityDatasetPrepare:
				requested := stringArgument(task.Arguments, "path")
				path, err := resolveDatasetPath(workspaceRoot, requested)
				if err != nil {
					return scheduler.Job{}, err
				}
				acb.Environment["CAPSULE_EXPERIMENT_DATASET"] = path
			}
			acb.Resources = resource.Spec{CPUQuotaPercent: 75, MemoryMaxBytes: uint64(memoryLimit), PidsMax: 16}
			return scheduler.Job{
				Agent: acb, Context: ctx, Timeout: taskTimeout, Demand: demand,
				DependsOn: append([]string(nil), task.DependsOn...),
			}, nil
		}
	}

	stringField := func(description string, required bool) planner.ArgumentField {
		return planner.ArgumentField{Type: planner.ArgumentString, Description: description, Required: required}
	}
	numberField := func(description string, required bool) planner.ArgumentField {
		return planner.ArgumentField{Type: planner.ArgumentNumber, Description: description, Required: required}
	}
	safety := planner.SafetyMetadata{ReadOnly: true, RootScoped: true, Permission: "local_experiment.execute"}
	timeoutSeconds := int(taskTimeout.Seconds())

	return []orchestrator.Registration{
		{
			Capability: planner.Capability{
				Name: CapabilityManifestInspect, Description: "Inspect one root-scoped local directory and strictly validate its capsule-experiment.json manifest.",
				InputSchema:       planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{"path": stringField("Experiment directory inside the configured workspace", true)}},
				OutputDescription: "Verified workspace-relative manifest, dataset, allowlisted methods, content digest, and bounded directory entries.",
				OutputSchema: map[string]string{
					"directory": "string", "manifest_file": "string", "manifest_sha256": "string",
					"dataset_path": "string", "methods": "array", "entries": "array",
				},
				TimeoutSeconds: timeoutSeconds, ExecutionType: "go_worker", Safety: safety,
			},
			Build: build(CapabilityManifestInspect, "manifest_inspect", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.3}),
		},
		{
			Capability: planner.Capability{
				Name: CapabilityDatasetPrepare, Description: "Inspect and prepare one root-scoped local CSV classification dataset.",
				InputSchema:       planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{"path": stringField("Dataset path inside the configured workspace", true)}},
				OutputDescription: "Verified dataset rows, features, classes, and content digest.",
				OutputSchema:      map[string]string{"rows": "number", "features": "array", "classes": "array", "sha256": "string"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: safety,
			},
			Build: build(CapabilityDatasetPrepare, "dataset_prepare", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.4}),
		},
		{
			Capability: planner.Capability{
				Name: CapabilityRun, Description: "Run one bounded CPU-only deterministic classification method simulation using a verified dataset.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"method":       stringField("logistic_regression, random_forest, or svm", true),
					"attempt":      numberField("Positive execution attempt", true),
					"n_estimators": numberField("Random Forest estimator count", false),
				}},
				OutputDescription: "Worker-measured runtime and deterministic accuracy/resource result, or a structured resource failure.",
				OutputSchema:      map[string]string{"method": "string", "status": "string", "accuracy": "number", "runtime_ms": "number", "memory_peak_bytes": "number", "memory_limit_bytes": "number"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: safety, RequiresDependency: true,
			},
			Build: build(CapabilityRun, "experiment_run", scheduler.Demand{CPU: 0.8, Memory: 0.8, IO: 0.1}),
		},
		{
			Capability: planner.Capability{
				Name: CapabilityAnalyze, Description: "Compare three verified experiment results by accuracy, runtime, and memory and select the best method.",
				InputSchema:       planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{}},
				OutputDescription: "Ranked experiment metrics, selected method, and evidence-based rationale.",
				OutputSchema:      map[string]string{"experiments": "array", "best_method": "string", "rationale": "string"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: safety, RequiresDependency: true,
			},
			Build: build(CapabilityAnalyze, "experiment_analyze", scheduler.Demand{CPU: 0.2, Memory: 0.2, IO: 0.1}),
		},
		{
			Capability: planner.Capability{
				Name: CapabilityReport, Description: "Generate experiment_report.md from a verified comparison result.",
				InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
					"goal": stringField("Original user goal", true), "replans": numberField("Number of replans", true),
					"retry_code":      stringField("Recovered failure code", false),
					"manifest_file":   stringField("Workspace-relative experiment manifest path", false),
					"manifest_sha256": stringField("Validated experiment manifest digest", false),
				}},
				OutputDescription: "Verified Markdown report and its underlying experiment metrics.",
				OutputSchema:      map[string]string{"report_file": "string", "best_method": "string", "experiments": "array", "replans": "number"},
				TimeoutSeconds:    timeoutSeconds, ExecutionType: "go_worker", Safety: safety, RequiresDependency: true,
			},
			Build: build(CapabilityReport, "experiment_report", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.3}),
		},
	}, nil
}

func normalizeWorkScale(workScale int) (int, error) {
	if workScale == 0 {
		return DefaultWorkScale, nil
	}
	if err := validateWorkScale(workScale); err != nil {
		return 0, err
	}
	return workScale, nil
}

func validateWorkScale(workScale int) error {
	if workScale < 1 || workScale > MaximumWorkScale {
		return fmt.Errorf("experiment work scale must be between 1 and %d", MaximumWorkScale)
	}
	return nil
}

func validateTaskArguments(task planner.Task) error {
	for _, name := range []string{"attempt", "n_estimators", "replans"} {
		value, exists := task.Arguments[name]
		if !exists {
			continue
		}
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number || number < 0 {
			return fmt.Errorf("task %s argument %s must be a non-negative integer", task.ID, name)
		}
	}
	if task.Capability != CapabilityRun {
		return nil
	}
	method := stringArgument(task.Arguments, "method")
	switch method {
	case MethodLogisticRegression, MethodSVM:
		if _, exists := task.Arguments["n_estimators"]; exists {
			return fmt.Errorf("task %s n_estimators is only valid for random_forest", task.ID)
		}
	case MethodRandomForest:
		estimators := integerArgument(task.Arguments, "n_estimators")
		if estimators <= 0 || estimators > 1000 {
			return fmt.Errorf("task %s random_forest n_estimators must be between 1 and 1000", task.ID)
		}
	default:
		return fmt.Errorf("task %s uses unsupported experiment method %q", task.ID, method)
	}
	if integerArgument(task.Arguments, "attempt") <= 0 {
		return fmt.Errorf("task %s attempt must be positive", task.ID)
	}
	return nil
}

func resolveDatasetPath(root, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", fmt.Errorf("dataset path is required")
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("dataset path must not be a symbolic link")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve experiment workspace: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("inspect dataset: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("dataset path escapes configured workspace")
	}
	info, err := os.Stat(resolvedCandidate)
	if err != nil {
		return "", fmt.Errorf("inspect dataset: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("dataset path is not a regular file")
	}
	if !strings.EqualFold(filepath.Ext(resolvedCandidate), ".csv") {
		return "", fmt.Errorf("dataset path must be a CSV file")
	}
	if info.Size() > MaximumDatasetBytes {
		return "", fmt.Errorf("dataset exceeds the %d-byte limit", MaximumDatasetBytes)
	}
	return resolvedCandidate, nil
}

func resolveManifestDirectoryPath(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("experiment directory path is required")
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("experiment directory must not be a symbolic link")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve experiment workspace: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("inspect experiment directory: %w", err)
	}
	if !pathWithinExperimentRoot(resolvedRoot, resolvedCandidate) {
		return "", fmt.Errorf("experiment directory escapes configured workspace")
	}
	info, err := os.Stat(resolvedCandidate)
	if err != nil {
		return "", fmt.Errorf("inspect experiment directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("experiment path is not a directory")
	}
	return resolvedCandidate, nil
}

func pathWithinExperimentRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func workspaceRelativePath(root, candidate string) (string, error) {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes configured workspace")
	}
	return filepath.ToSlash(relative), nil
}

func validateWorkspaceRelativeRequest(name, value string, allowDot bool) error {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return fmt.Errorf("%s must be a workspace-relative path", name)
	}
	cleaned := filepath.Clean(value)
	if cleaned != value || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) || (!allowDot && cleaned == ".") {
		return fmt.Errorf("%s must be a normalized workspace-relative path without traversal", name)
	}
	return nil
}

func stringArgument(arguments map[string]any, name string) string {
	value, _ := arguments[name].(string)
	return strings.TrimSpace(value)
}

func integerArgument(arguments map[string]any, name string) int {
	value, _ := arguments[name].(float64)
	return int(value)
}
