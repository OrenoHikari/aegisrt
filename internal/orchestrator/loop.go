package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"aegisrt/internal/planner"
	"aegisrt/internal/telemetry"
)

const (
	defaultMaxReplans  = 3
	defaultLoopTimeout = 2 * time.Minute
)

var (
	ErrMaxReplansExceeded = errors.New("maximum Agent replans exceeded")
	ErrRepeatedPlan       = errors.New("re-planner returned an equivalent plan")
	ErrGoalFailed         = errors.New("Agent could not complete the goal")
	ErrLoopNoProgress     = errors.New("Agent loop cannot continue without a revised plan")
)

var runSequence atomic.Uint64

// PlanCreator is implemented by planner.Planner and deterministic test fakes.
type PlanCreator interface {
	Create(ctx context.Context, userTask string) (planner.Plan, error)
}

// PlanValidator is an optional application-level policy applied to every plan
// version after the generic Registry and DAG validation. It lets vertical
// applications enforce bounded domain actions without moving policy into the
// Scheduler.
type PlanValidator interface {
	ValidatePlan(plan planner.Plan) error
}

// LoopOptions provides hard safety bounds around cognitive iterations.
type LoopOptions struct {
	MaxReplans    int
	Timeout       time.Duration
	PlanValidator PlanValidator
}

// IterationResult preserves the evidence and decision for one plan version.
type IterationResult struct {
	Version        int           `json:"version"`
	Plan           planner.Plan  `json:"plan"`
	Execution      Result        `json:"execution"`
	Observations   []Observation `json:"observations"`
	Decision       Decision      `json:"decision"`
	ExecutionError string        `json:"execution_error,omitempty"`
}

// LoopResult is the complete, bounded Agentic execution history.
type LoopResult struct {
	RunID         string            `json:"run_id"`
	Goal          string            `json:"goal"`
	Iterations    []IterationResult `json:"iterations"`
	Replans       int               `json:"replans"`
	FinalDecision Decision          `json:"final_decision"`
	FinalOutputs  map[string]string `json:"final_outputs,omitempty"`
	FinalAnswer   string            `json:"final_answer,omitempty"`
}

// AgentLoop decides what to do; Orchestrator and Scheduler remain responsible
// for all task execution and dependency timing.
type AgentLoop struct {
	planner      PlanCreator
	controller   Controller
	orchestrator *Orchestrator
	registry     *Registry
	events       telemetry.Publisher
	options      LoopOptions
}

func NewAgentLoop(
	planCreator PlanCreator,
	controller Controller,
	orchestrator *Orchestrator,
	registry *Registry,
	events telemetry.Publisher,
	options LoopOptions,
) (*AgentLoop, error) {
	if planCreator == nil {
		return nil, fmt.Errorf("Planner is required")
	}
	if controller == nil {
		return nil, fmt.Errorf("Decision controller is required")
	}
	if orchestrator == nil {
		return nil, fmt.Errorf("Orchestrator is required")
	}
	if registry == nil {
		return nil, fmt.Errorf("Capability Registry is required")
	}
	if events == nil {
		events = telemetry.NopPublisher{}
	}
	if options.MaxReplans <= 0 {
		options.MaxReplans = defaultMaxReplans
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultLoopTimeout
	}
	return &AgentLoop{
		planner: planCreator, controller: controller, orchestrator: orchestrator,
		registry: registry, events: events, options: options,
	}, nil
}

// Run performs initial planning followed by bounded execute-observe-decide and
// optional re-plan iterations.
func (l *AgentLoop) Run(parent context.Context, goal string) (LoopResult, error) {
	if parent == nil {
		parent = context.Background()
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return LoopResult{}, fmt.Errorf("user goal is required")
	}
	ctx, cancel := context.WithTimeout(parent, l.options.Timeout)
	defer cancel()
	defer l.orchestrator.Stop()

	runID := fmt.Sprintf("agent-%d-%d", time.Now().UTC().UnixNano(), runSequence.Add(1))
	loopResult := LoopResult{RunID: runID, Goal: goal}
	plan, err := l.planner.Create(ctx, goal)
	if err != nil {
		return loopResult, l.abort(runID, 0, fmt.Errorf("initial planning: %w", err))
	}
	if _, err := planner.Validate(plan, l.registry.Capabilities()); err != nil {
		return loopResult, l.abort(runID, 0, fmt.Errorf("validate initial plan: %w", err))
	}
	plan = planner.Normalize(plan)
	if l.options.PlanValidator != nil {
		if err := l.options.PlanValidator.ValidatePlan(plan); err != nil {
			return loopResult, l.abort(runID, 0, fmt.Errorf("validate initial application plan: %w", err))
		}
	}
	l.publish(telemetry.KindPlanCreated, runID, 1, "", planTelemetryPayload(plan, 1, 0))
	seenPlans := map[string]struct{}{planFingerprint(plan): {}}

	for iteration := 1; ; iteration++ {
		if err := ctx.Err(); err != nil {
			return loopResult, l.abort(runID, iteration, err)
		}
		execution, executionErr := l.orchestrator.ExecuteIteration(ctx, runID, iteration, plan)
		if execution.Plan.Goal == "" {
			return loopResult, l.abort(runID, iteration, executionErr)
		}
		observations := l.orchestrator.Observe(runID, iteration, execution)
		decision, err := l.controller.Decide(ctx, DecisionRequest{
			RunID: runID, Iteration: iteration, UserGoal: goal,
			CurrentPlan: plan, Observations: observations,
			Capabilities: l.registry.Capabilities(),
		})
		if err != nil {
			return loopResult, l.abort(runID, iteration, err)
		}
		l.publish(telemetry.KindDecisionMade, runID, iteration, "", map[string]any{
			"decision": decision.Type, "reason": decision.Reason,
			"observation_summary": decisionObservationSummary(observations),
			"action":              decisionAction(decision.Type),
		})
		iterationResult := IterationResult{
			Version: iteration, Plan: plan, Execution: execution,
			Observations: observations, Decision: decision,
			ExecutionError: errorString(executionErr),
		}
		loopResult.Iterations = append(loopResult.Iterations, iterationResult)
		loopResult.FinalDecision = decision

		switch decision.Type {
		case DecisionGoalCompleted:
			if executionErr != nil || !allObservationsVerified(observations) {
				if executionErr == nil {
					executionErr = fmt.Errorf("one or more task outputs were not verified")
				}
				return loopResult, l.abort(runID, iteration, fmt.Errorf("decision claimed completion after failed execution: %w", executionErr))
			}
			loopResult.FinalOutputs = execution.FinalOutputs
			loopResult.FinalAnswer = decision.FinalAnswer
			if loopResult.FinalAnswer == "" {
				loopResult.FinalAnswer = joinFinalOutputs(execution.FinalOutputs)
			}
			l.publish(telemetry.KindGoalCompleted, runID, iteration, "", map[string]any{
				"replans": loopResult.Replans, "tasks": len(plan.Tasks),
			})
			return loopResult, nil

		case DecisionContinue:
			// Execute waits for the submitted DAG to become terminal. There is no
			// hidden cognitive ready queue, so continuing requires a revised DAG.
			return loopResult, l.abort(runID, iteration, ErrLoopNoProgress)

		case DecisionFailed:
			return loopResult, l.abort(runID, iteration, fmt.Errorf("%w: %s", ErrGoalFailed, decision.Reason))

		case DecisionReplan:
			if loopResult.Replans >= l.options.MaxReplans {
				return loopResult, l.abort(runID, iteration, fmt.Errorf("%w (%d): %s", ErrMaxReplansExceeded, l.options.MaxReplans, decision.Reason))
			}
			l.publish(telemetry.KindReplanRequested, runID, iteration, "", map[string]any{
				"reason": decision.Reason, "replan": loopResult.Replans + 1,
			})
			completed, failed := classifyTasks(plan, observations)
			revised, err := l.controller.Replan(ctx, ReplanRequest{
				RunID: runID, Iteration: iteration, UserGoal: goal,
				PreviousPlan: plan, CompletedTask: completed, FailedTask: failed,
				Observations: observations, Capabilities: l.registry.Capabilities(),
			})
			if err != nil {
				return loopResult, l.abort(runID, iteration, err)
			}
			if _, err := planner.Validate(revised, l.registry.Capabilities()); err != nil {
				return loopResult, l.abort(runID, iteration, fmt.Errorf("validate revised plan: %w", err))
			}
			revised = planner.Normalize(revised)
			if err := validateRevisedPlan(plan, completed, failed, revised); err != nil {
				return loopResult, l.abort(runID, iteration, err)
			}
			if l.options.PlanValidator != nil {
				if err := l.options.PlanValidator.ValidatePlan(revised); err != nil {
					return loopResult, l.abort(runID, iteration, fmt.Errorf("validate revised application plan: %w", err))
				}
			}
			fingerprint := planFingerprint(revised)
			if _, repeated := seenPlans[fingerprint]; repeated {
				return loopResult, l.abort(runID, iteration, ErrRepeatedPlan)
			}
			seenPlans[fingerprint] = struct{}{}
			loopResult.Replans++
			l.publish(telemetry.KindPlanRevised, runID, iteration+1, "", planTelemetryPayload(revised, iteration+1, loopResult.Replans))
			plan = revised
		}
	}
}

func decisionObservationSummary(observations []Observation) string {
	verified, failed, reused := 0, 0, 0
	var details []string
	for _, observation := range observations {
		if observation.Reused {
			reused++
		}
		if observation.Success && observation.Metadata.OutputVerified {
			verified++
		} else if !observation.Success {
			failed++
		}
		switch observation.Capability {
		case "experiment.manifest.inspect":
			if file, _ := observation.Output["manifest_file"].(string); file != "" {
				details = append(details, fmt.Sprintf("validated experiment manifest %s", file))
			}
		case "literature.search":
			if count := integerOutput(observation.Output["total_results"]); count >= 0 {
				details = append(details, fmt.Sprintf("literature search returned %d result(s)", count))
			}
		case "paper.analyze":
			candidates := arrayOutputLength(observation.Output["candidate_findings"])
			if candidates > 0 {
				details = append(details, fmt.Sprintf("paper analysis produced %d candidate finding(s)", candidates))
			}
		case "research.report":
			if closed, _ := observation.Output["citation_closed"].(bool); closed {
				details = append(details, "report citation closure passed")
			}
		case "experiment.run":
			if code, _ := observation.Output["failure_code"].(string); code != "" {
				details = append(details, fmt.Sprintf("experiment failed with %s", code))
			}
		case "experiment.report":
			if best, _ := observation.Output["best_name"].(string); best != "" {
				details = append(details, fmt.Sprintf("experiment selected %s", best))
			}
		}
	}
	parts := []string{fmt.Sprintf("%d verified output(s), %d failed task(s)", verified, failed)}
	if reused > 0 {
		parts = append(parts, fmt.Sprintf("%d output(s) reused", reused))
	}
	if len(details) > 3 {
		details = details[:3]
	}
	parts = append(parts, details...)
	return strings.Join(parts, "; ") + "."
}

func decisionAction(decision DecisionType) string {
	switch decision {
	case DecisionGoalCompleted:
		return "Publish the verified result and report."
	case DecisionContinue:
		return "Continue the current validated plan."
	case DecisionReplan:
		return "Create a revised validated DAG, reuse valid outputs, and resume CAPSuleRT execution."
	case DecisionFailed:
		return "Stop safely and surface the structured failure diagnostics."
	default:
		return ""
	}
}

func integerOutput(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return -1
}

func arrayOutputLength(value any) int {
	switch typed := value.(type) {
	case []any:
		return len(typed)
	case []map[string]any:
		return len(typed)
	default:
		return 0
	}
}

func planTelemetryPayload(plan planner.Plan, version, replan int) map[string]any {
	tasks := make([]map[string]any, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		tasks = append(tasks, map[string]any{
			"id": task.ID, "name": task.Name, "capability": task.Capability,
			"depends_on": append([]string(nil), task.DependsOn...),
		})
	}
	payload := map[string]any{
		"goal_sha256": fingerprintText(plan.Goal), "tasks": len(tasks),
		"version": version, "plan_tasks": tasks,
	}
	if replan > 0 {
		payload["replan"] = replan
	}
	return payload
}

func fingerprintText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func classifyTasks(plan planner.Plan, observations []Observation) ([]planner.Task, []planner.Task) {
	byID := make(map[string]planner.Task, len(plan.Tasks))
	for _, task := range plan.Tasks {
		byID[task.ID] = task
	}
	var completed, failed []planner.Task
	for _, observation := range observations {
		if observation.Success && observation.Metadata.OutputVerified {
			completed = append(completed, byID[observation.TaskID])
		} else {
			failed = append(failed, byID[observation.TaskID])
		}
	}
	return completed, failed
}

func validateRevisedPlan(previous planner.Plan, completed, failed []planner.Task, revised planner.Plan) error {
	if strings.TrimSpace(revised.Goal) != strings.TrimSpace(previous.Goal) {
		return fmt.Errorf("revised plan changed the original plan goal")
	}
	revisedByID := make(map[string]planner.Task, len(revised.Tasks))
	for _, task := range revised.Tasks {
		revisedByID[task.ID] = task
	}
	for _, task := range completed {
		reused, exists := revisedByID[task.ID]
		if !exists {
			return fmt.Errorf("revised plan discarded successful task %s", task.ID)
		}
		oldFingerprint, _ := taskFingerprint(task)
		newFingerprint, _ := taskFingerprint(reused)
		if oldFingerprint != newFingerprint {
			return fmt.Errorf("revised plan changed successful task %s", task.ID)
		}
	}
	for _, task := range failed {
		if _, exists := revisedByID[task.ID]; exists {
			return fmt.Errorf("revised plan retained failed task ID %s; a new ID is required", task.ID)
		}
	}
	return nil
}

func planFingerprint(plan planner.Plan) string {
	plan = planner.Normalize(plan)
	sort.Slice(plan.Tasks, func(i, j int) bool { return plan.Tasks[i].ID < plan.Tasks[j].ID })
	type fingerprintTask struct {
		ID         string         `json:"id"`
		Capability string         `json:"capability"`
		Arguments  map[string]any `json:"arguments"`
		DependsOn  []string       `json:"depends_on"`
	}
	canonical := make([]fingerprintTask, 0, len(plan.Tasks))
	for _, task := range plan.Tasks {
		canonical = append(canonical, fingerprintTask{
			ID: task.ID, Capability: task.Capability, Arguments: task.Arguments, DependsOn: task.DependsOn,
		})
	}
	encoded, _ := json.Marshal(canonical)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func allObservationsVerified(observations []Observation) bool {
	if len(observations) == 0 {
		return false
	}
	for _, observation := range observations {
		if !observation.Success || !observation.Metadata.OutputVerified {
			return false
		}
	}
	return true
}

func joinFinalOutputs(outputs map[string]string) string {
	ids := make([]string, 0, len(outputs))
	for id := range outputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, outputs[id])
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (l *AgentLoop) abort(runID string, iteration int, err error) error {
	if err == nil {
		err = ErrGoalFailed
	}
	l.publish(telemetry.KindAgentLoopAborted, runID, iteration, "FAILED", map[string]any{"error": err.Error()})
	return err
}

func (l *AgentLoop) publish(kind telemetry.Kind, runID string, iteration int, phase string, payload map[string]any) {
	payload["run_id"] = runID
	payload["iteration"] = iteration
	event, err := telemetry.NewEvent(kind, "cognitive-loop", "", phase, payload)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = l.events.Publish(ctx, event)
}
