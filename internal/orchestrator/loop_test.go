package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

type fixedPlanCreator struct {
	plan planner.Plan
	err  error
}

func (p fixedPlanCreator) Create(_ context.Context, _ string) (planner.Plan, error) {
	return p.plan, p.err
}

type scriptedController struct {
	decisions []Decision
	plans     []planner.Plan
	decideErr error
	requests  []ReplanRequest
	decideAt  int
	replanAt  int
}

func (c *scriptedController) Decide(_ context.Context, _ DecisionRequest) (Decision, error) {
	if c.decideErr != nil {
		return Decision{}, c.decideErr
	}
	if c.decideAt >= len(c.decisions) {
		return Decision{}, errors.New("scripted decision exhausted")
	}
	decision := c.decisions[c.decideAt]
	c.decideAt++
	return decision, nil
}

func (c *scriptedController) Replan(_ context.Context, request ReplanRequest) (planner.Plan, error) {
	c.requests = append(c.requests, request)
	if c.replanAt >= len(c.plans) {
		return planner.Plan{}, errors.New("scripted replan exhausted")
	}
	plan := c.plans[c.replanAt]
	c.replanAt++
	return plan, nil
}

func TestAgentLoopZeroReplanSuccess(t *testing.T) {
	initial := loopPlan(loopTask("task-a"))
	loop, controller, executor := newLoopHarness(t, initial, []Decision{{
		Type: DecisionGoalCompleted, Reason: "done",
	}}, nil, nil, LoopOptions{})
	result, err := loop.Run(context.Background(), "goal")
	if err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if result.Replans != 0 || len(result.Iterations) != 1 || len(executor.order) != 1 || controller.replanAt != 0 {
		t.Fatalf("unexpected loop result: %+v order=%v", result, executor.order)
	}
}

func TestAgentLoopOneReplanReusesCompletedTask(t *testing.T) {
	a := loopTask("task-a")
	b := loopTask("task-b")
	b.DependsOn = []string{"task-a"}
	initial := loopPlan(a)
	revised := loopPlan(a, b)
	loop, controller, executor := newLoopHarness(t, initial, []Decision{
		{Type: DecisionReplan, Reason: "need another task"},
		{Type: DecisionGoalCompleted, Reason: "done"},
	}, []planner.Plan{revised}, nil, LoopOptions{MaxReplans: 3})
	result, err := loop.Run(context.Background(), "goal")
	if err != nil {
		t.Fatalf("run loop: %v", err)
	}
	if result.Replans != 1 || len(result.Iterations) != 2 {
		t.Fatalf("unexpected iterations: %+v", result)
	}
	if len(result.Iterations[1].Execution.ReusedTaskIDs) != 1 || result.Iterations[1].Execution.ReusedTaskIDs[0] != "task-a" {
		t.Fatalf("successful task was not reused: %+v", result.Iterations[1].Execution)
	}
	if len(executor.order) != 2 || executor.order[0] != "task-a" || executor.order[1] != "task-b" {
		t.Fatalf("task was re-executed instead of reused: %v", executor.order)
	}
	if len(controller.requests) != 1 || len(controller.requests[0].CompletedTask) != 1 {
		t.Fatalf("completed tasks were not supplied to re-planning: %+v", controller.requests)
	}
}

func TestAgentLoopExecutionFailureThenRecovery(t *testing.T) {
	initial := loopPlan(loopTask("bad-task"))
	revised := loopPlan(loopTask("recovery-task"))
	loop, controller, executor := newLoopHarness(t, initial, []Decision{
		{Type: DecisionReplan, Reason: "replace failed task"},
		{Type: DecisionGoalCompleted, Reason: "recovered"},
	}, []planner.Plan{revised}, map[string]bool{"bad-task": true}, LoopOptions{MaxReplans: 2})
	result, err := loop.Run(context.Background(), "goal")
	if err != nil {
		t.Fatalf("recover failed execution: %v", err)
	}
	if result.Replans != 1 || len(executor.order) != 2 || len(controller.requests[0].FailedTask) != 1 {
		t.Fatalf("unexpected recovery: result=%+v order=%v requests=%+v", result, executor.order, controller.requests)
	}
}

func TestAgentLoopUnrecoverableFailure(t *testing.T) {
	loop, _, _ := newLoopHarness(t, loopPlan(loopTask("bad-task")), []Decision{{
		Type: DecisionFailed, Reason: "no capability can recover",
	}}, nil, map[string]bool{"bad-task": true}, LoopOptions{})
	_, err := loop.Run(context.Background(), "goal")
	if !errors.Is(err, ErrGoalFailed) {
		t.Fatalf("expected unrecoverable goal error, got %v", err)
	}
}

func TestAgentLoopContinueWithoutPendingWorkStops(t *testing.T) {
	loop, _, _ := newLoopHarness(t, loopPlan(loopTask("task-a")), []Decision{{
		Type: DecisionContinue, Reason: "continue",
	}}, nil, nil, LoopOptions{})
	if _, err := loop.Run(context.Background(), "goal"); !errors.Is(err, ErrLoopNoProgress) {
		t.Fatalf("expected no-progress protection, got %v", err)
	}
}

func TestAgentLoopRejectsInvalidRevisedPlan(t *testing.T) {
	a := loopTask("task-a")
	invalid := loopPlan(a, planner.Task{
		ID: "unknown", Name: "unknown", Description: "unknown", Capability: "shell.run",
	})
	loop, _, _ := newLoopHarness(t, loopPlan(a), []Decision{{
		Type: DecisionReplan, Reason: "invalid revision",
	}}, []planner.Plan{invalid}, nil, LoopOptions{})
	if _, err := loop.Run(context.Background(), "goal"); !errors.Is(err, planner.ErrUnknownCapability) {
		t.Fatalf("expected invalid revised plan error, got %v", err)
	}
}

func TestAgentLoopRejectsRepeatedEquivalentPlan(t *testing.T) {
	initial := loopPlan(loopTask("task-a"))
	loop, _, _ := newLoopHarness(t, initial, []Decision{{
		Type: DecisionReplan, Reason: "try the same thing",
	}}, []planner.Plan{initial}, nil, LoopOptions{})
	if _, err := loop.Run(context.Background(), "goal"); !errors.Is(err, ErrRepeatedPlan) {
		t.Fatalf("expected repeated-plan error, got %v", err)
	}
}

func TestAgentLoopMaxReplansExceeded(t *testing.T) {
	a := loopTask("task-a")
	b := loopTask("task-b")
	revised := loopPlan(a, b)
	loop, _, _ := newLoopHarness(t, loopPlan(a), []Decision{
		{Type: DecisionReplan, Reason: "first"},
		{Type: DecisionReplan, Reason: "again"},
	}, []planner.Plan{revised}, nil, LoopOptions{MaxReplans: 1})
	if _, err := loop.Run(context.Background(), "goal"); !errors.Is(err, ErrMaxReplansExceeded) {
		t.Fatalf("expected max-replans error, got %v", err)
	}
}

func TestAgentLoopContextCancellation(t *testing.T) {
	loop, _, executor := newLoopHarness(t, loopPlan(loopTask("task-a")), []Decision{{
		Type: DecisionGoalCompleted, Reason: "done",
	}}, nil, nil, LoopOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loop.Run(ctx, "goal"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if len(executor.order) != 0 {
		t.Fatalf("cancelled loop executed tasks: %v", executor.order)
	}
}

func TestAgentLoopHandlesLLMDecisionFailure(t *testing.T) {
	loop, controller, _ := newLoopHarness(t, loopPlan(loopTask("task-a")), nil, nil, nil, LoopOptions{})
	controller.decideErr = errors.New("LLM unavailable")
	if _, err := loop.Run(context.Background(), "goal"); err == nil || err.Error() != "LLM unavailable" {
		t.Fatalf("expected LLM decision failure, got %v", err)
	}
}

func TestPlanTelemetryPayloadContainsSanitizedDAG(t *testing.T) {
	task := loopTask("task-a")
	task.Arguments = map[string]any{"secret_input": "must not be emitted"}
	payload := planTelemetryPayload(loopPlan(task), 2, 1)
	if payload["version"] != 2 || payload["replan"] != 1 || payload["tasks"] != 1 {
		t.Fatalf("unexpected plan telemetry metadata: %+v", payload)
	}
	tasks, ok := payload["plan_tasks"].([]map[string]any)
	if !ok || len(tasks) != 1 || tasks[0]["id"] != "task-a" || tasks[0]["capability"] != "test.run" {
		t.Fatalf("plan DAG was not emitted: %+v", payload)
	}
	if _, leaked := tasks[0]["arguments"]; leaked {
		t.Fatalf("plan arguments leaked into telemetry: %+v", tasks[0])
	}
}

func TestDecisionPresentationMetadataIsStructuredAndBounded(t *testing.T) {
	observations := []Observation{
		{Capability: "literature.search", Success: true, Output: map[string]any{"total_results": float64(1)}, Metadata: ObservationMeta{OutputVerified: true}},
		{Capability: "paper.analyze", Success: false, Output: map[string]any{"candidate_findings": []any{map[string]any{"claim": "candidate"}}}},
	}
	summary := decisionObservationSummary(observations)
	if !strings.Contains(summary, "1 verified output") || !strings.Contains(summary, "1 failed task") || !strings.Contains(summary, "1 result") {
		t.Fatalf("unexpected observation summary: %q", summary)
	}
	if action := decisionAction(DecisionReplan); !strings.Contains(action, "revised validated DAG") || !strings.Contains(action, "reuse") {
		t.Fatalf("unexpected replan action: %q", action)
	}
}

func newLoopHarness(
	t *testing.T,
	initial planner.Plan,
	decisions []Decision,
	revisions []planner.Plan,
	failures map[string]bool,
	options LoopOptions,
) (*AgentLoop, *scriptedController, *orchestratorTestExecutor) {
	t.Helper()
	executor := &orchestratorTestExecutor{
		root: t.TempDir(), fail: failures, dependencies: make(map[string]int), delay: time.Duration(0),
	}
	runtime, err := scheduler.New(executor, 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry([]Registration{{
		Capability: planner.Capability{Name: "test.run", Description: "test"},
		Build: func(ctx context.Context, task planner.Task) (scheduler.Job, error) {
			return scheduler.Job{
				Agent: agent.New(task.ID, task.Capability, "test", nil), Context: ctx,
				Timeout: time.Second, DependsOn: append([]string(nil), task.DependsOn...),
			}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := New(runtime, registry, telemetry.NopPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	controller := &scriptedController{decisions: decisions, plans: revisions}
	loop, err := NewAgentLoop(fixedPlanCreator{plan: initial}, controller, orchestrator, registry, telemetry.NopPublisher{}, options)
	if err != nil {
		t.Fatal(err)
	}
	return loop, controller, executor
}

func loopTask(id string) planner.Task {
	return planner.Task{ID: id, Name: id, Description: "execute " + id, Capability: "test.run", DependsOn: []string{}}
}

func loopPlan(tasks ...planner.Task) planner.Plan {
	return planner.Plan{Goal: "goal", Tasks: tasks}
}
