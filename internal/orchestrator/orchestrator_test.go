package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
)

type orchestratorTestExecutor struct {
	root  string
	fail  map[string]bool
	delay time.Duration

	mu            sync.Mutex
	order         []string
	dependencies  map[string]int
	running       int
	maxConcurrent int
}

func (e *orchestratorTestExecutor) Run(_ context.Context, acb *agent.ACB) error {
	e.mu.Lock()
	e.order = append(e.order, acb.ID)
	e.dependencies[acb.ID] = len(acb.DependencyOutputs)
	e.running++
	if e.running > e.maxConcurrent {
		e.maxConcurrent = e.running
	}
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		e.running--
		e.mu.Unlock()
	}()

	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	if e.fail[acb.ID] {
		return errors.New("simulated failure")
	}

	commitPath := filepath.Join(e.root, acb.ID)
	if err := os.MkdirAll(commitPath, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("result from %s with %d dependencies\n", acb.ID, len(acb.DependencyOutputs))
	if err := os.WriteFile(filepath.Join(commitPath, "result.txt"), []byte(content), 0o444); err != nil {
		return err
	}

	acb.OutputCommitted = true
	acb.OutputTransactionID = acb.ID + "-transaction"
	acb.OutputCommitPath = commitPath
	acb.OutputManifestPath = filepath.Join(commitPath, ".aegis-commit.json")
	acb.OutputFileCount = 1
	acb.OutputBytes = uint64(len(content))
	return nil
}

func TestOrchestratorSingleTask(t *testing.T) {
	orchestrator, executor := newTestOrchestrator(t, 1, nil, 0)
	result, err := orchestrator.Run(context.Background(), testPlan(testTask("task-1")))
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}

	if len(result.Records) != 1 || result.Records[0].Phase != scheduler.PhaseSucceeded {
		t.Fatalf("unexpected records: %+v", result.Records)
	}
	if result.FinalOutputs["task-1"] == "" {
		t.Fatalf("final output was not aggregated")
	}
	if len(executor.order) != 1 || executor.order[0] != "task-1" {
		t.Fatalf("unexpected execution order: %v", executor.order)
	}
}

func TestOrchestratorSerialDependency(t *testing.T) {
	orchestrator, executor := newTestOrchestrator(t, 2, nil, 0)
	first := testTask("task-1")
	second := testTask("task-2")
	second.DependsOn = []string{"task-1"}

	result, err := orchestrator.Run(context.Background(), testPlan(first, second))
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if len(result.Records) != 2 || executor.dependencies["task-2"] != 1 {
		t.Fatalf("dependency output was not injected: %+v", executor.dependencies)
	}
	if executor.order[0] != "task-1" || executor.order[1] != "task-2" {
		t.Fatalf("dependency order was not enforced: %v", executor.order)
	}
}

func TestOrchestratorIndependentTasksUseSchedulerConcurrency(t *testing.T) {
	orchestrator, executor := newTestOrchestrator(t, 2, nil, 50*time.Millisecond)

	_, err := orchestrator.Run(
		context.Background(),
		testPlan(testTask("task-1"), testTask("task-2")),
	)
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if executor.maxConcurrent < 2 {
		t.Fatalf("expected Scheduler concurrency, max running was %d", executor.maxConcurrent)
	}
}

func TestOrchestratorPropagatesUpstreamFailure(t *testing.T) {
	orchestrator, executor := newTestOrchestrator(
		t,
		2,
		map[string]bool{"task-1": true},
		0,
	)
	first := testTask("task-1")
	second := testTask("task-2")
	second.DependsOn = []string{"task-1"}

	result, err := orchestrator.Run(context.Background(), testPlan(first, second))
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("expected ExecutionError, got %v", err)
	}
	if len(executionErr.Failed) != 1 || len(executionErr.Blocked) != 1 {
		t.Fatalf("unexpected execution error: %+v", executionErr)
	}
	if len(executor.order) != 1 || executor.order[0] != "task-1" {
		t.Fatalf("blocked child was executed: %v", executor.order)
	}
	if result.Records[0].Phase != scheduler.PhaseFailed ||
		result.Records[1].Phase != scheduler.PhaseBlocked {
		t.Fatalf("unexpected propagated phases: %+v", result.Records)
	}
}

func newTestOrchestrator(
	t *testing.T,
	workers int,
	failures map[string]bool,
	delay time.Duration,
) (*Orchestrator, *orchestratorTestExecutor) {
	t.Helper()

	executor := &orchestratorTestExecutor{
		root:         t.TempDir(),
		fail:         failures,
		delay:        delay,
		dependencies: make(map[string]int),
	}
	runtime, err := scheduler.New(executor, workers, 16)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	registry, err := NewRegistry([]Registration{
		{
			Capability: planner.Capability{
				Agent:       "test_agent",
				Action:      "run",
				Description: "test capability",
			},
			Build: func(_ context.Context, task planner.Task) (scheduler.Job, error) {
				return scheduler.Job{
					Agent:     agent.New(task.ID, task.Agent, "test", nil),
					Timeout:   time.Second,
					DependsOn: append([]string(nil), task.DependsOn...),
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}

	orchestrator, err := New(runtime, registry, nil)
	if err != nil {
		t.Fatalf("create Orchestrator: %v", err)
	}
	return orchestrator, executor
}

func testTask(id string) planner.Task {
	return planner.Task{
		ID:          id,
		Name:        id,
		Description: "execute " + id,
		Agent:       "test_agent",
		Action:      "run",
	}
}

func testPlan(tasks ...planner.Task) planner.Plan {
	return planner.Plan{Goal: "test orchestration", Tasks: tasks}
}
