package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
	"aegisrt/internal/planner"
	"aegisrt/internal/resource"
	"aegisrt/internal/scheduler"
)

const (
	defaultTaskTimeout       = 30 * time.Second
	defaultMaximumInputBytes = 16 * 1024 * 1024
)

// BuiltinOptions configures the trusted local cognitive capabilities.
type BuiltinOptions struct {
	WorkerPath    string
	PythonCommand string
	ContextStore  *contextfs.Store
	InputRoot     string
	MaxInputBytes int64
	TaskTimeout   time.Duration
}

// NewBuiltinRegistry registers root-scoped environment, file, data-analysis,
// and text capabilities. Every action still runs as a normal Scheduler Job.
func NewBuiltinRegistry(options BuiltinOptions) (*Registry, error) {
	workerPath := strings.TrimSpace(options.WorkerPath)
	if workerPath == "" {
		return nil, fmt.Errorf("cognitive Agent worker path is required")
	}
	absWorker, err := filepath.Abs(workerPath)
	if err != nil {
		return nil, fmt.Errorf("resolve cognitive Agent worker: %w", err)
	}
	workerInfo, err := os.Stat(absWorker)
	if err != nil {
		return nil, fmt.Errorf("inspect cognitive Agent worker: %w", err)
	}
	if !workerInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("cognitive Agent worker is not a regular file")
	}
	if options.ContextStore == nil {
		return nil, fmt.Errorf("ContextFS store is required")
	}

	inputRoot := strings.TrimSpace(options.InputRoot)
	if inputRoot == "" {
		inputRoot = "."
	}
	inputRoot, err = filepath.Abs(inputRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve capability input root: %w", err)
	}
	inputRoot, err = filepath.EvalSymlinks(inputRoot)
	if err != nil {
		return nil, fmt.Errorf("evaluate capability input root: %w", err)
	}

	maximumInputBytes := options.MaxInputBytes
	if maximumInputBytes <= 0 {
		maximumInputBytes = defaultMaximumInputBytes
	}
	pythonCommand := strings.TrimSpace(options.PythonCommand)
	if pythonCommand == "" {
		pythonCommand = "python3"
	}
	taskTimeout := options.TaskTimeout
	if taskTimeout <= 0 {
		taskTimeout = defaultTaskTimeout
	}

	build := func(capabilityName, action string, demand scheduler.Demand, rootScoped, importFile bool) JobFactory {
		return func(ctx context.Context, task planner.Task) (scheduler.Job, error) {
			arguments := cloneArguments(task.Arguments)
			var contexts []contextstore.Ref
			if rootScoped {
				requested, _ := arguments["path"].(string)
				resolved, resolveErr := resolveScopedPath(inputRoot, requested)
				if resolveErr != nil {
					return scheduler.Job{}, resolveErr
				}
				arguments["path"] = resolved
				if importFile {
					contexts, err = importContextFile(ctx, options.ContextStore, resolved, maximumInputBytes)
					if err != nil {
						return scheduler.Job{}, err
					}
				}
			}

			encodedArguments, err := json.Marshal(arguments)
			if err != nil {
				return scheduler.Job{}, fmt.Errorf("encode structured arguments: %w", err)
			}
			acb := agent.New(task.ID, capabilityName, pythonCommand, []string{absWorker, "--action", action})
			acb.Environment = map[string]string{
				"CAPSULE_TASK_ID":             task.ID,
				"CAPSULE_TASK_NAME":           task.Name,
				"CAPSULE_TASK_DESCRIPTION":    task.Description,
				"CAPSULE_TASK_CAPABILITY":     capabilityName,
				"CAPSULE_TASK_ARGUMENTS_JSON": string(encodedArguments),
				"CAPSULE_CAPABILITY_ROOT":     inputRoot,
			}
			acb.Resources = resource.Spec{
				CPUQuotaPercent: 50,
				MemoryMaxBytes:  128 * 1024 * 1024,
				PidsMax:         16,
			}
			return scheduler.Job{
				Agent:     acb,
				Context:   ctx,
				Timeout:   taskTimeout,
				Demand:    demand,
				Contexts:  contexts,
				DependsOn: append([]string(nil), task.DependsOn...),
			}, nil
		}
	}

	pathSchema := planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
		"path": {
			Type:        planner.ArgumentString,
			Description: "Path relative to the configured capability root",
			Required:    true,
		},
	}}
	questionSchema := planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
		"question": {
			Type:        planner.ArgumentString,
			Description: "Optional analysis or summary focus",
		},
	}}
	timeoutSeconds := int(taskTimeout.Seconds())
	readOnlyRoot := planner.SafetyMetadata{ReadOnly: true, RootScoped: true, Permission: "workspace.read"}
	verifiedInputs := planner.SafetyMetadata{ReadOnly: true, Permission: "verified_dependencies.read"}

	return NewRegistry([]Registration{
		{
			Capability: planner.Capability{
				Name:              "filesystem.list",
				Description:       "List immediate files and directories at a path inside the configured workspace root.",
				InputSchema:       pathSchema,
				OutputDescription: "Directory existence, entry names, types, and sizes.",
				OutputSchema:      map[string]string{"exists": "boolean", "entries": "array"},
				TimeoutSeconds:    timeoutSeconds,
				ExecutionType:     "python_worker",
				Safety:            readOnlyRoot,
			},
			Build: build("filesystem.list", "filesystem_list", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.4}, true, false),
		},
		{
			Capability: planner.Capability{
				Name:              "filesystem.stat",
				Description:       "Inspect whether a root-scoped path exists and report its type, size, and extension.",
				InputSchema:       pathSchema,
				OutputDescription: "Path existence and metadata; a missing path is a successful environmental observation.",
				OutputSchema:      map[string]string{"exists": "boolean", "kind": "string", "size_bytes": "number"},
				TimeoutSeconds:    timeoutSeconds,
				ExecutionType:     "python_worker",
				Safety:            readOnlyRoot,
			},
			Build: build("filesystem.stat", "filesystem_stat", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.3}, true, false),
		},
		{
			Capability: planner.Capability{
				Name:              "file.inspect",
				Description:       "Read metadata and textual content from one file inside the configured workspace root.",
				InputSchema:       pathSchema,
				OutputDescription: "Text preview, byte count, line count, and file extension.",
				OutputSchema:      map[string]string{"bytes": "number", "lines": "number", "content": "string"},
				TimeoutSeconds:    timeoutSeconds,
				ExecutionType:     "python_worker",
				Safety:            readOnlyRoot,
			},
			Build: build("file.inspect", "inspect_file", scheduler.Demand{CPU: 0.1, Memory: 0.1, IO: 0.4}, true, true),
		},
		{
			Capability: planner.Capability{
				Name:              "data.inspect",
				Description:       "Inspect a CSV or JSON file and return rows, fields, inferred types, missing values, and basic numeric statistics.",
				InputSchema:       pathSchema,
				OutputDescription: "A bounded structured data profile for CSV or JSON input.",
				OutputSchema:      map[string]string{"format": "string", "rows": "number", "fields": "array", "statistics": "object"},
				TimeoutSeconds:    timeoutSeconds,
				ExecutionType:     "python_worker",
				Safety:            readOnlyRoot,
			},
			Build: build("data.inspect", "data_inspect", scheduler.Demand{CPU: 0.4, Memory: 0.3, IO: 0.4}, true, true),
		},
		{
			Capability: planner.Capability{
				Name:               "text.analyze",
				Description:        "Analyze structured or unstructured verified upstream task outputs.",
				InputSchema:        questionSchema,
				OutputDescription:  "Evidence-based facts and basic numeric signals from dependencies.",
				OutputSchema:       map[string]string{"facts": "array", "numeric_summary": "object"},
				TimeoutSeconds:     timeoutSeconds,
				ExecutionType:      "python_worker",
				Safety:             verifiedInputs,
				RequiresDependency: true,
			},
			Build: build("text.analyze", "analyze", scheduler.Demand{CPU: 0.4, Memory: 0.2, IO: 0.1}, false, false),
		},
		{
			Capability: planner.Capability{
				Name:               "text.summarize",
				Description:        "Create the final concise report from verified upstream results.",
				InputSchema:        questionSchema,
				OutputDescription:  "A human-readable final answer grounded in verified dependency outputs.",
				OutputSchema:       map[string]string{"summary": "string"},
				TimeoutSeconds:     timeoutSeconds,
				ExecutionType:      "python_worker",
				Safety:             verifiedInputs,
				RequiresDependency: true,
			},
			Build: build("text.summarize", "summarize", scheduler.Demand{CPU: 0.2, Memory: 0.2, IO: 0.2}, false, false),
		},
	})
}

func cloneArguments(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for name, value := range source {
		result[name] = value
	}
	return result
}

// resolveScopedPath resolves existing symlinks, including the nearest existing
// ancestor of a missing path, before enforcing the configured root boundary.
func resolveScopedPath(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("path argument is required")
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if !pathWithinRoot(root, candidate) {
		return "", fmt.Errorf("path %q escapes configured root %q", requested, root)
	}

	current := candidate
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect path %q: %w", requested, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve path %q: no existing ancestor", requested)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}

	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", fmt.Errorf("evaluate path %q: %w", requested, err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	resolved = filepath.Clean(resolved)
	if !pathWithinRoot(root, resolved) {
		return "", fmt.Errorf("path %q escapes configured root %q", requested, root)
	}
	return resolved, nil
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func importContextFile(ctx context.Context, store *contextfs.Store, path string, maximum int64) ([]contextstore.Ref, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect input file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	if info.Size() > maximum {
		return nil, fmt.Errorf("input file %q exceeds the %d-byte capability limit", path, maximum)
	}
	object, err := store.PutFile(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("store input file in ContextFS: %w", err)
	}
	return []contextstore.Ref{{
		Key:       "file://" + filepath.ToSlash(path),
		Digest:    object.Digest,
		SizeBytes: object.SizeBytes,
	}}, nil
}
