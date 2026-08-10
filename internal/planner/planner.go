// Package planner turns natural-language goals into validated task DAGs.
package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"aegisrt/internal/llm"
	"aegisrt/internal/scheduler"
)

var (
	ErrInvalidJSON       = errors.New("planner response is not valid JSON")
	ErrDuplicateTaskID   = errors.New("planner task ID is duplicated")
	ErrUnknownCapability = errors.New("planner selected an unregistered capability")
)

var safeTaskID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Task is one executable node in a cognitive plan.
type Task struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Capability  string         `json:"capability,omitempty"`
	Arguments   map[string]any `json:"arguments,omitempty"`
	DependsOn   []string       `json:"depends_on"`

	// Agent, Action, and Parameters preserve compatibility with first-stage
	// plans. Normalize converts them to Capability and Arguments before use.
	Agent      string            `json:"agent,omitempty"`
	Action     string            `json:"action,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// Plan is the validated cognitive-plane representation of one user goal.
type Plan struct {
	Goal  string `json:"goal"`
	Tasks []Task `json:"tasks"`
}

// Capability describes an Agent/action pair the planner may select.
type Capability struct {
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	InputSchema        ArgumentSchema    `json:"input_schema"`
	OutputDescription  string            `json:"output_description"`
	OutputSchema       map[string]string `json:"output_schema,omitempty"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	ExecutionType      string            `json:"execution_type"`
	Safety             SafetyMetadata    `json:"safety"`
	RequiresDependency bool              `json:"requires_dependency,omitempty"`

	// Legacy adapter identity. These fields are not shown to the LLM.
	Agent              string   `json:"-"`
	Action             string   `json:"-"`
	RequiredParameters []string `json:"-"`
	OptionalParameters []string `json:"-"`
}

// SafetyMetadata describes the permission boundary of a capability.
type SafetyMetadata struct {
	ReadOnly   bool   `json:"read_only"`
	RootScoped bool   `json:"root_scoped"`
	Permission string `json:"permission"`
}

// Planner owns prompting, strict decoding, and program-side validation.
type Planner struct {
	client       llm.Client
	capabilities []Capability
}

// New creates a planner restricted to the supplied capability catalog.
func New(client llm.Client, capabilities []Capability) (*Planner, error) {
	if client == nil {
		return nil, fmt.Errorf("LLM client is required")
	}

	if err := validateCapabilityCatalog(capabilities); err != nil {
		return nil, err
	}
	capabilities = cloneCapabilities(capabilities)

	return &Planner{
		client:       client,
		capabilities: capabilities,
	}, nil
}

// Create asks the LLM for a plan and accepts it only after strict validation.
func (p *Planner) Create(ctx context.Context, userTask string) (Plan, error) {
	userTask = strings.TrimSpace(userTask)
	if userTask == "" {
		return Plan{}, fmt.Errorf("user task is required")
	}

	temperature := 0.0
	maximumTokens := 4096
	response, err := p.client.Generate(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: p.systemPrompt()},
			{Role: "user", Content: "User task:\n" + userTask},
		},
		Temperature: &temperature,
		MaxTokens:   &maximumTokens,
		JSONMode:    true,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("generate task plan: %w", err)
	}

	plan, err := Parse(response.Content)
	if err != nil {
		return Plan{}, err
	}

	if _, err := Validate(plan, p.capabilities); err != nil {
		return Plan{}, err
	}

	return Normalize(plan), nil
}

// Parse strictly decodes a single JSON plan object.
func Parse(content string) (Plan, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()

	var plan Plan
	if err := decoder.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Plan{}, fmt.Errorf("%w: multiple JSON values", ErrInvalidJSON)
		}
		return Plan{}, fmt.Errorf("%w: trailing content: %v", ErrInvalidJSON, err)
	}

	return plan, nil
}

// Validate checks plan fields, registered capabilities, dependencies, and DAG
// integrity. The returned IDs are in stable topological submission order.
func Validate(plan Plan, capabilities []Capability) ([]string, error) {
	plan = Normalize(plan)
	capabilities = cloneCapabilities(capabilities)

	if strings.TrimSpace(plan.Goal) == "" {
		return nil, fmt.Errorf("plan goal is required")
	}

	if len(plan.Tasks) == 0 {
		return nil, fmt.Errorf("plan must contain at least one task")
	}

	if err := validateCapabilityCatalog(capabilities); err != nil {
		return nil, err
	}

	capabilityByName := make(map[string]Capability, len(capabilities))
	for _, capability := range capabilities {
		capabilityByName[CapabilityName(capability)] = capability
	}

	seen := make(map[string]struct{}, len(plan.Tasks))
	nodes := make([]scheduler.DAGNode, 0, len(plan.Tasks))

	for _, task := range plan.Tasks {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			return nil, fmt.Errorf("task ID is required")
		}
		if !safeTaskID.MatchString(id) {
			return nil, fmt.Errorf("task ID %q is not a valid CAPSuleRT Agent ID", id)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateTaskID, id)
		}
		seen[id] = struct{}{}

		if strings.TrimSpace(task.Name) == "" {
			return nil, fmt.Errorf("task %s name is required", id)
		}
		if strings.TrimSpace(task.Description) == "" {
			return nil, fmt.Errorf("task %s description is required", id)
		}
		if strings.TrimSpace(task.Capability) == "" {
			return nil, fmt.Errorf("task %s capability is required", id)
		}

		capability, exists := capabilityByName[task.Capability]
		if !exists {
			return nil, fmt.Errorf(
				"%w: %s for task %s",
				ErrUnknownCapability,
				task.Capability,
				id,
			)
		}

		if err := capability.InputSchema.Validate(task.Arguments); err != nil {
			return nil, fmt.Errorf("task %s arguments: %w", id, err)
		}
		if err := validateLegacyParameters(id, task.Arguments, capability); err != nil {
			return nil, err
		}
		if capability.RequiresDependency && len(task.DependsOn) == 0 {
			return nil, fmt.Errorf(
				"task %s using %s requires at least one dependency",
				id,
				CapabilityName(capability),
			)
		}

		nodes = append(nodes, scheduler.DAGNode{ID: id, DependsOn: task.DependsOn})
	}

	order, err := scheduler.ValidateDAG(nodes)
	if err != nil {
		return nil, fmt.Errorf("validate plan DAG: %w", err)
	}

	return order, nil
}

func (p *Planner) systemPrompt() string {
	catalog, _ := json.MarshalIndent(p.capabilities, "", "  ")

	return `You are the CAPSuleRT task planner in the cognitive plane.
Convert the user's task into a directed acyclic graph of concrete executable tasks.

Return exactly one json object and no Markdown, comments, or surrounding text.
The JSON schema is:
{
  "goal": "non-empty goal",
  "tasks": [
    {
      "id": "unique safe ID such as task-1",
      "name": "short task name",
      "description": "precise execution instruction",
      "capability": "registered capability name",
      "depends_on": ["existing task IDs"],
      "arguments": {"registered_argument": "typed value"}
    }
  ]
}

Rules:
1. Break complex goals into clear executable subtasks.
2. Every task ID must be unique and every dependency must name a task in this plan.
3. Dependencies must form a DAG; never create a cycle or self-dependency.
4. Every task must specify one non-empty registered capability.
5. Use only the registered capabilities below.
6. Arguments must match the selected capability input_schema, including JSON value types.
7. Never invent a tool, capability, agent, action, command, or shell operation.
8. Independent tasks should not depend on each other.

Registered capabilities:
` + string(catalog)
}

func validateCapabilityCatalog(capabilities []Capability) error {
	if len(capabilities) == 0 {
		return fmt.Errorf("at least one planner capability is required")
	}

	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		name := CapabilityName(capability)
		if name == "" {
			return fmt.Errorf("capability name is required")
		}

		if _, exists := seen[name]; exists {
			return fmt.Errorf("capability %s is duplicated", name)
		}
		seen[name] = struct{}{}

		if err := capability.InputSchema.ValidateDefinition(); err != nil {
			return fmt.Errorf("capability %s input schema: %w", name, err)
		}
	}

	return nil
}

func validateLegacyParameters(taskID string, arguments map[string]any, capability Capability) error {
	if len(capability.RequiredParameters) == 0 && len(capability.OptionalParameters) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(capability.RequiredParameters)+len(capability.OptionalParameters))
	for _, name := range append(append([]string(nil), capability.RequiredParameters...), capability.OptionalParameters...) {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("capability %s/%s contains an empty parameter name", capability.Agent, capability.Action)
		}
		allowed[name] = struct{}{}
	}

	for _, required := range capability.RequiredParameters {
		required = strings.TrimSpace(required)
		value, exists := arguments[required]
		text, isString := value.(string)
		if !exists || !isString || strings.TrimSpace(text) == "" {
			return fmt.Errorf("task %s requires parameter %q", taskID, required)
		}
	}

	for name := range arguments {
		if _, exists := allowed[name]; !exists {
			return fmt.Errorf(
				"task %s parameter %q is not registered for %s",
				taskID,
				name,
				CapabilityName(capability),
			)
		}
	}

	return nil
}

func capabilityKey(agent, action string) string {
	return strings.TrimSpace(agent) + "\x00" + strings.TrimSpace(action)
}

// CapabilityName returns the canonical Registry identity.
func CapabilityName(capability Capability) string {
	name := strings.TrimSpace(capability.Name)
	if name != "" {
		return name
	}

	agent := strings.TrimSpace(capability.Agent)
	action := strings.TrimSpace(capability.Action)
	if agent == "" || action == "" {
		return ""
	}

	return agent + "." + action
}

// Normalize trims planner-controlled identifiers and returns a stable
// dependency ordering without changing parameter values.
func Normalize(plan Plan) Plan {
	plan.Goal = strings.TrimSpace(plan.Goal)
	tasks := make([]Task, len(plan.Tasks))
	for index, task := range plan.Tasks {
		tasks[index] = task
		tasks[index].DependsOn = append([]string(nil), task.DependsOn...)
		if task.Arguments != nil {
			tasks[index].Arguments = make(map[string]any, len(task.Arguments))
			for name, value := range task.Arguments {
				tasks[index].Arguments[name] = value
			}
		}
		if task.Parameters != nil {
			tasks[index].Parameters = make(map[string]string, len(task.Parameters))
			for name, value := range task.Parameters {
				tasks[index].Parameters[name] = value
			}
		}
	}
	plan.Tasks = tasks

	for index := range plan.Tasks {
		task := &plan.Tasks[index]
		task.ID = strings.TrimSpace(task.ID)
		task.Name = strings.TrimSpace(task.Name)
		task.Description = strings.TrimSpace(task.Description)
		task.Capability = strings.TrimSpace(task.Capability)
		task.Agent = strings.TrimSpace(task.Agent)
		task.Action = strings.TrimSpace(task.Action)

		if task.Capability == "" && task.Agent != "" && task.Action != "" {
			task.Capability = task.Agent + "." + task.Action
		}
		if task.Arguments == nil && task.Parameters != nil {
			task.Arguments = make(map[string]any, len(task.Parameters))
			for name, value := range task.Parameters {
				task.Arguments[name] = value
			}
		}

		dependencies := append([]string(nil), task.DependsOn...)
		for dependencyIndex := range dependencies {
			dependencies[dependencyIndex] = strings.TrimSpace(dependencies[dependencyIndex])
		}
		sort.Strings(dependencies)
		task.DependsOn = dependencies
	}

	return plan
}

func cloneCapabilities(source []Capability) []Capability {
	result := make([]Capability, len(source))
	for index, capability := range source {
		result[index] = capability
		result[index].Name = CapabilityName(capability)
		result[index].InputSchema = capability.InputSchema.Clone()
		if len(result[index].InputSchema.Fields) == 0 &&
			(len(capability.RequiredParameters) > 0 || len(capability.OptionalParameters) > 0) {
			result[index].InputSchema.Fields = make(map[string]ArgumentField)
			for _, name := range capability.RequiredParameters {
				result[index].InputSchema.Fields[name] = ArgumentField{Type: ArgumentString, Required: true}
			}
			for _, name := range capability.OptionalParameters {
				result[index].InputSchema.Fields[name] = ArgumentField{Type: ArgumentString}
			}
		}
		if capability.OutputSchema != nil {
			result[index].OutputSchema = make(map[string]string, len(capability.OutputSchema))
			for name, value := range capability.OutputSchema {
				result[index].OutputSchema[name] = value
			}
		}
		result[index].RequiredParameters = append([]string(nil), capability.RequiredParameters...)
		result[index].OptionalParameters = append([]string(nil), capability.OptionalParameters...)
	}
	return result
}
