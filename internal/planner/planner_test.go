package planner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aegisrt/internal/llm"
	"aegisrt/internal/scheduler"
)

type plannerTestClient struct {
	content string
	err     error
	request llm.Request
}

func (c *plannerTestClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.request = request
	return llm.Response{Content: c.content}, c.err
}

var plannerTestCapabilities = []Capability{
	{
		Agent:              "file_agent",
		Action:             "inspect_file",
		Description:        "Inspect one file",
		RequiredParameters: []string{"path"},
	},
	{
		Agent:              "analysis_agent",
		Action:             "analyze",
		Description:        "Analyze upstream results",
		RequiresDependency: true,
	},
	{
		Agent:              "writer_agent",
		Action:             "summarize",
		Description:        "Summarize upstream results",
		RequiresDependency: true,
	},
}

func TestPlannerCreatesValidPlan(t *testing.T) {
	client := &plannerTestClient{content: `{
  "goal":"inspect and summarize",
  "tasks":[
    {"id":"task-1","name":"inspect","description":"inspect file","agent":"file_agent","action":"inspect_file","depends_on":[],"parameters":{"path":"examples/sales.txt"}},
    {"id":"task-2","name":"analyze","description":"analyze content","agent":"analysis_agent","action":"analyze","depends_on":["task-1"]},
    {"id":"task-3","name":"summarize","description":"write summary","agent":"writer_agent","action":"summarize","depends_on":["task-2"]}
  ]
}`}

	planner, err := New(client, plannerTestCapabilities)
	if err != nil {
		t.Fatalf("create planner: %v", err)
	}

	plan, err := planner.Create(context.Background(), "inspect examples/sales.txt")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.Goal != "inspect and summarize" || len(plan.Tasks) != 3 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(client.request.Messages) != 2 ||
		!strings.Contains(client.request.Messages[0].Content, "file_agent") {
		t.Fatalf("registered capabilities were not included in the prompt")
	}
}

func TestPlannerRejectsInvalidJSON(t *testing.T) {
	client := &plannerTestClient{content: "```json\n{}\n```"}
	planner, _ := New(client, plannerTestCapabilities)

	_, err := planner.Create(context.Background(), "task")
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

func TestPlannerRejectsDuplicateTaskID(t *testing.T) {
	plan := validTestPlan()
	plan.Tasks[1].ID = plan.Tasks[0].ID

	_, err := Validate(plan, plannerTestCapabilities)
	if !errors.Is(err, ErrDuplicateTaskID) {
		t.Fatalf("expected duplicate task error, got %v", err)
	}
}

func TestPlannerRejectsInvalidDependency(t *testing.T) {
	plan := validTestPlan()
	plan.Tasks[1].DependsOn = []string{"missing-task"}

	_, err := Validate(plan, plannerTestCapabilities)
	if !errors.Is(err, scheduler.ErrDAGUnknownDependency) {
		t.Fatalf("expected unknown dependency error, got %v", err)
	}
}

func TestPlannerRejectsDAGCycle(t *testing.T) {
	plan := validTestPlan()
	plan.Tasks[0].DependsOn = []string{"task-2"}

	_, err := Validate(plan, plannerTestCapabilities)
	if !errors.Is(err, scheduler.ErrDAGCycle) {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestPlannerRejectsUnknownCapability(t *testing.T) {
	plan := validTestPlan()
	plan.Tasks[1].Action = "run_shell"

	_, err := Validate(plan, plannerTestCapabilities)
	if !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("expected unknown capability error, got %v", err)
	}
}

func validTestPlan() Plan {
	return Plan{
		Goal: "test goal",
		Tasks: []Task{
			{
				ID:          "task-1",
				Name:        "inspect",
				Description: "inspect input",
				Agent:       "file_agent",
				Action:      "inspect_file",
				Parameters:  map[string]string{"path": "examples/sales.txt"},
			},
			{
				ID:          "task-2",
				Name:        "analyze",
				Description: "analyze input",
				Agent:       "analysis_agent",
				Action:      "analyze",
				DependsOn:   []string{"task-1"},
			},
		},
	}
}
