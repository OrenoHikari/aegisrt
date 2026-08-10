// Package orchestrator adapts validated cognitive plans to the existing
// CAPSuleRT execution plane. It deliberately delegates scheduling, lifecycle,
// resources, dependencies, and output integrity to the Runtime.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

const maximumAggregatedResultBytes = 8 * 1024 * 1024

// Runtime is the existing Scheduler surface used by Orchestrator.
type Runtime interface {
	Submit(job scheduler.Job) error
	Start()
	Wait()
	Stop()
	Snapshot() []scheduler.Record
}

// Result is a completed plan execution and its leaf task outputs.
type Result struct {
	Plan            planner.Plan
	Records         []scheduler.Record
	FinalOutputs    map[string]string
	ExecutedTaskIDs []string
	ReusedTaskIDs   []string
}

// ExecutionError summarizes failed and dependency-blocked Runtime jobs.
type ExecutionError struct {
	Failed  []string
	Blocked []string
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf(
		"plan execution did not fully succeed (failed=%s blocked=%s)",
		strings.Join(e.Failed, ","),
		strings.Join(e.Blocked, ","),
	)
}

// Orchestrator converts and submits jobs but does not implement scheduling.
type Orchestrator struct {
	runtime  Runtime
	registry *Registry
	events   telemetry.Publisher

	mu        sync.Mutex
	submitted map[string]string
	stopped   bool
}

// New creates a thin cognitive-to-execution-plane adapter.
func New(runtime Runtime, registry *Registry, events telemetry.Publisher) (*Orchestrator, error) {
	if runtime == nil {
		return nil, fmt.Errorf("CAPSuleRT Scheduler is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("capability registry is required")
	}
	if events == nil {
		events = telemetry.NopPublisher{}
	}

	return &Orchestrator{
		runtime:   runtime,
		registry:  registry,
		events:    events,
		submitted: make(map[string]string),
	}, nil
}

// Run validates the graph, builds native Jobs, submits them in topological
// order, then lets the existing Scheduler execute and propagate failures.
func (o *Orchestrator) Run(ctx context.Context, plan planner.Plan) (Result, error) {
	defer o.Stop()
	return o.Execute(ctx, plan)
}

// Execute runs one plan iteration while keeping the native Scheduler session
// alive. A revised plan can reuse an identical, already successful task; only
// new task IDs are submitted to the Scheduler.
func (o *Orchestrator) Execute(ctx context.Context, plan planner.Plan) (Result, error) {
	return o.ExecuteIteration(ctx, "", 0, plan)
}

// ExecuteIteration attaches cognitive correlation data to native Scheduler
// records and events without introducing another execution queue.
func (o *Orchestrator) ExecuteIteration(ctx context.Context, runID string, iteration int, plan planner.Plan) (Result, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return Result{}, fmt.Errorf("orchestrator has stopped")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	plan = planner.Normalize(plan)

	order, err := planner.Validate(plan, o.registry.Capabilities())
	if err != nil {
		return Result{}, err
	}

	taskByID := make(map[string]planner.Task, len(plan.Tasks))
	for _, task := range plan.Tasks {
		taskByID[task.ID] = task
	}

	recordByID := make(map[string]scheduler.Record)
	for _, record := range o.runtime.Snapshot() {
		recordByID[record.ID] = record
	}

	jobs := make([]scheduler.Job, 0, len(order))
	executedTaskIDs := make([]string, 0, len(order))
	reusedTaskIDs := make([]string, 0, len(order))
	for _, taskID := range order {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("prepare plan execution: %w", err)
		}

		fingerprint, err := taskFingerprint(taskByID[taskID])
		if err != nil {
			return Result{}, fmt.Errorf("fingerprint task %s: %w", taskID, err)
		}
		if previousFingerprint, exists := o.submitted[taskID]; exists {
			if previousFingerprint != fingerprint {
				return Result{}, fmt.Errorf("revised plan changed previously submitted task %s", taskID)
			}
			record, recorded := recordByID[taskID]
			if !recorded || record.Phase != scheduler.PhaseSucceeded || !record.OutputVerified {
				return Result{}, fmt.Errorf("revised plan cannot reuse non-successful task %s", taskID)
			}
			reusedTaskIDs = append(reusedTaskIDs, taskID)
			continue
		}
		if _, collides := recordByID[taskID]; collides {
			return Result{}, fmt.Errorf("task ID %s already exists in the Scheduler", taskID)
		}

		job, err := o.registry.Build(ctx, taskByID[taskID])
		if err != nil {
			return Result{}, err
		}
		if job.Agent == nil || job.Agent.ID != taskID {
			return Result{}, fmt.Errorf("capability adapter returned an invalid Agent for task %s", taskID)
		}
		job.Context = ctx
		job.Metadata = map[string]string{
			"run_id":     runID,
			"iteration":  fmt.Sprintf("%d", iteration),
			"capability": taskByID[taskID].Capability,
		}
		job.DependsOn = append([]string(nil), taskByID[taskID].DependsOn...)
		jobs = append(jobs, job)
		executedTaskIDs = append(executedTaskIDs, taskID)
	}

	accepted := 0
	for _, job := range jobs {
		if err := o.runtime.Submit(job); err != nil {
			if accepted > 0 {
				o.runtime.Start()
				o.runtime.Wait()
			}
			return Result{}, fmt.Errorf("submit task %s to CAPSuleRT: %w", job.Agent.ID, err)
		}
		fingerprint, _ := taskFingerprint(taskByID[job.Agent.ID])
		o.submitted[job.Agent.ID] = fingerprint
		task := taskByID[job.Agent.ID]
		switch task.Capability {
		case "paper.analyze":
			o.publish(telemetry.KindPaperAnalysisStarted, job.Agent.ID, "STARTED", map[string]any{
				"run_id": runID, "iteration": iteration, "capability": task.Capability,
			})
		case "research.report":
			o.publish(telemetry.KindReportValidationStart, job.Agent.ID, "STARTED", map[string]any{
				"run_id": runID, "iteration": iteration, "capability": task.Capability,
			})
		}
		accepted++
	}

	o.publish(
		telemetry.KindOrchestrationStarted,
		"",
		"",
		map[string]any{
			"run_id": runID, "iteration": iteration,
			"goal": plan.Goal, "tasks": len(plan.Tasks),
		},
	)

	if accepted > 0 {
		o.runtime.Start()
		o.runtime.Wait()
	}

	records := recordsForPlan(o.runtime.Snapshot(), taskByID)
	sortRecordsByPlan(records, order)

	result := Result{
		Plan:            plan,
		Records:         records,
		FinalOutputs:    collectFinalOutputs(plan, records),
		ExecutedTaskIDs: executedTaskIDs,
		ReusedTaskIDs:   reusedTaskIDs,
	}

	executionErr := summarizeExecutionErrors(records)
	phase := "SUCCEEDED"
	if executionErr != nil {
		phase = "FAILED"
	}

	o.publish(
		telemetry.KindOrchestrationFinished,
		"",
		phase,
		map[string]any{
			"run_id":        runID,
			"iteration":     iteration,
			"goal":          plan.Goal,
			"tasks":         len(plan.Tasks),
			"final_outputs": len(result.FinalOutputs),
			"error":         errorString(executionErr),
		},
	)

	return result, executionErr
}

// Stop terminates the retained Scheduler session after the whole Agent loop.
func (o *Orchestrator) Stop() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return
	}
	o.stopped = true
	o.runtime.Stop()
}

func recordsForPlan(records []scheduler.Record, tasks map[string]planner.Task) []scheduler.Record {
	result := make([]scheduler.Record, 0, len(tasks))
	for _, record := range records {
		if _, exists := tasks[record.ID]; exists {
			result = append(result, record)
		}
	}
	return result
}

func taskFingerprint(task planner.Task) (string, error) {
	normalized := planner.Normalize(planner.Plan{Goal: "fingerprint", Tasks: []planner.Task{task}}).Tasks[0]
	normalized.Agent = ""
	normalized.Action = ""
	normalized.Parameters = nil
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (o *Orchestrator) publish(kind telemetry.Kind, agentID, phase string, payload any) {
	event, err := telemetry.NewEvent(kind, "orchestrator", agentID, phase, payload)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = o.events.Publish(ctx, event)
}

func summarizeExecutionErrors(records []scheduler.Record) error {
	executionErr := &ExecutionError{}

	for _, record := range records {
		switch record.Phase {
		case scheduler.PhaseFailed:
			executionErr.Failed = append(executionErr.Failed, record.ID)
		case scheduler.PhaseBlocked:
			executionErr.Blocked = append(executionErr.Blocked, record.ID)
		case scheduler.PhaseSucceeded:
		default:
			executionErr.Failed = append(executionErr.Failed, record.ID)
		}
	}

	if len(executionErr.Failed) == 0 && len(executionErr.Blocked) == 0 {
		return nil
	}

	return executionErr
}

func collectFinalOutputs(plan planner.Plan, records []scheduler.Record) map[string]string {
	dependedOn := make(map[string]struct{})
	for _, task := range plan.Tasks {
		for _, dependencyID := range task.DependsOn {
			dependedOn[dependencyID] = struct{}{}
		}
	}

	outputs := make(map[string]string)
	for _, record := range records {
		if _, isUpstream := dependedOn[record.ID]; isUpstream {
			continue
		}
		if record.Phase != scheduler.PhaseSucceeded ||
			!record.OutputCommitted ||
			!record.OutputVerified {
			continue
		}

		resultPath := filepath.Join(record.OutputCommitPath, "result.txt")
		content, err := readBoundedFile(resultPath, maximumAggregatedResultBytes)
		if err == nil {
			outputs[record.ID] = strings.TrimSpace(string(content))
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			outputs[record.ID] = record.OutputCommitPath
		}
	}

	return outputs
}

func readBoundedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("result exceeds %d bytes", maximum)
	}
	return data, nil
}

func sortRecordsByPlan(records []scheduler.Record, order []string) {
	position := make(map[string]int, len(order))
	for index, id := range order {
		position[id] = index
	}

	sort.SliceStable(records, func(i, j int) bool {
		return position[records[i].ID] < position[records[j].ID]
	})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
