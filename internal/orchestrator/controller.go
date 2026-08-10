package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"aegisrt/internal/llm"
	"aegisrt/internal/planner"
)

// DecisionType is the validated cognitive outcome after one execution round.
type DecisionType string

const (
	DecisionGoalCompleted DecisionType = "GOAL_COMPLETED"
	DecisionContinue      DecisionType = "CONTINUE"
	DecisionReplan        DecisionType = "REPLAN"
	DecisionFailed        DecisionType = "FAILED"
)

var ErrMalformedDecision = errors.New("LLM decision is malformed")

// Decision is intentionally small so the Runtime never receives arbitrary
// model-generated commands.
type Decision struct {
	Type        DecisionType `json:"decision"`
	Reason      string       `json:"reason"`
	FinalAnswer string       `json:"final_answer,omitempty"`
}

// DecisionRequest contains bounded execution evidence for the cognitive plane.
type DecisionRequest struct {
	RunID        string               `json:"run_id"`
	Iteration    int                  `json:"iteration"`
	UserGoal     string               `json:"user_goal"`
	CurrentPlan  planner.Plan         `json:"current_plan"`
	Observations []Observation        `json:"observations"`
	Capabilities []planner.Capability `json:"available_capabilities"`
}

// ReplanRequest retains successful evidence while replacing invalid work.
type ReplanRequest struct {
	RunID         string               `json:"run_id"`
	Iteration     int                  `json:"iteration"`
	UserGoal      string               `json:"user_goal"`
	Constraints   []string             `json:"replan_constraints,omitempty"`
	PreviousPlan  planner.Plan         `json:"previous_plan"`
	CompletedTask []planner.Task       `json:"completed_tasks"`
	FailedTask    []planner.Task       `json:"failed_tasks"`
	Observations  []Observation        `json:"observations"`
	Capabilities  []planner.Capability `json:"available_capabilities"`
}

// Controller owns post-execution reasoning and revised planning.
type Controller interface {
	Decide(ctx context.Context, request DecisionRequest) (Decision, error)
	Replan(ctx context.Context, request ReplanRequest) (planner.Plan, error)
}

// LLMController is the validated LLM-backed Decision/Re-plan implementation.
type LLMController struct {
	client       llm.Client
	capabilities []planner.Capability
}

func NewLLMController(client llm.Client, capabilities []planner.Capability) (*LLMController, error) {
	if client == nil {
		return nil, fmt.Errorf("LLM client is required")
	}
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("at least one capability is required")
	}
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		name := planner.CapabilityName(capability)
		if name == "" {
			return nil, fmt.Errorf("capability name is required")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("capability %s is duplicated", name)
		}
		seen[name] = struct{}{}
		if err := capability.InputSchema.ValidateDefinition(); err != nil {
			return nil, fmt.Errorf("capability %s input schema: %w", name, err)
		}
	}
	return &LLMController{client: client, capabilities: clonePlannerCapabilities(capabilities)}, nil
}

func (c *LLMController) Decide(ctx context.Context, request DecisionRequest) (Decision, error) {
	request.Capabilities = clonePlannerCapabilities(c.capabilities)
	payload, err := json.Marshal(request)
	if err != nil {
		return Decision{}, fmt.Errorf("encode decision context: %w", err)
	}
	temperature := 0.0
	maximumTokens := 2048
	response, err := c.client.Generate(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: decisionSystemPrompt},
			{Role: "user", Content: "CAPSULERT_DECISION_REQUEST\n" + string(payload)},
		},
		Temperature: &temperature,
		MaxTokens:   &maximumTokens,
		JSONMode:    true,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("generate execution decision: %w", err)
	}
	return ParseDecision(response.Content)
}

func (c *LLMController) Replan(ctx context.Context, request ReplanRequest) (planner.Plan, error) {
	request.Capabilities = clonePlannerCapabilities(c.capabilities)
	payload, err := json.Marshal(request)
	if err != nil {
		return planner.Plan{}, fmt.Errorf("encode replan context: %w", err)
	}
	temperature := 0.0
	maximumTokens := 4096
	response, err := c.client.Generate(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: replanSystemPrompt},
			{Role: "user", Content: "CAPSULERT_REPLAN_REQUEST\n" + string(payload)},
		},
		Temperature: &temperature,
		MaxTokens:   &maximumTokens,
		JSONMode:    true,
	})
	if err != nil {
		return planner.Plan{}, fmt.Errorf("generate revised plan: %w", err)
	}
	plan, err := planner.Parse(response.Content)
	if err != nil {
		return planner.Plan{}, err
	}
	// The previous validated goal is authoritative. The model chooses revised
	// actions, but it cannot broaden or rewrite the user's objective merely by
	// returning a differently punctuated or paraphrased goal field.
	plan.Goal = request.PreviousPlan.Goal
	if _, err := planner.Validate(plan, c.capabilities); err != nil {
		return planner.Plan{}, fmt.Errorf("validate revised plan: %w", err)
	}
	return planner.Normalize(plan), nil
}

// ParseDecision strictly decodes and validates the model decision object.
func ParseDecision(content string) (Decision, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	var decision Decision
	if err := decoder.Decode(&decision); err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrMalformedDecision, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Decision{}, fmt.Errorf("%w: trailing JSON content", ErrMalformedDecision)
	}
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.FinalAnswer = strings.TrimSpace(decision.FinalAnswer)
	if decision.Reason == "" {
		return Decision{}, fmt.Errorf("%w: reason is required", ErrMalformedDecision)
	}
	switch decision.Type {
	case DecisionGoalCompleted, DecisionContinue, DecisionReplan, DecisionFailed:
	default:
		return Decision{}, fmt.Errorf("%w: unknown decision %q", ErrMalformedDecision, decision.Type)
	}
	return decision, nil
}

func clonePlannerCapabilities(source []planner.Capability) []planner.Capability {
	result := make([]planner.Capability, len(source))
	for index, capability := range source {
		result[index] = cloneCapability(capability)
	}
	return result
}

const decisionSystemPrompt = `You are the CAPSuleRT execution decision layer.
The execution plane has already acted. Judge only from the supplied goal, plan,
structured observations, and registered capabilities. Return exactly one json
object: {"decision":"GOAL_COMPLETED|CONTINUE|REPLAN|FAILED","reason":"...","final_answer":"optional"}.
Use REPLAN when evidence invalidates the plan or more registered work is needed.
Use FAILED when no registered capability can recover. Never invent tools or commands.`

const replanSystemPrompt = `You are the CAPSuleRT re-planner. Return exactly one json Plan object using
the schema {"goal":"...","tasks":[{"id":"...","name":"...","description":"...","capability":"...","arguments":{},"depends_on":[]}] }.
Use only available_capabilities and valid typed arguments. Preserve every
completed task unchanged, including ID, capability, arguments, description and
dependencies. Do not retain failed tasks: replace them with new IDs. New tasks
may depend on preserved successful tasks. A successful task can still have an
empty or insufficient result: keep that task and add a different registered
action with a new ID instead of merely rebuilding the same downstream plan.
Obey every supplied replan_constraints entry. Never create shell commands.`
