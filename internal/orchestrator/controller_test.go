package orchestrator

import (
	"context"
	"errors"
	"testing"

	"aegisrt/internal/llm"
	"aegisrt/internal/planner"
)

type controllerTestClient struct {
	content string
}

func (c controllerTestClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Content: c.content}, nil
}

func TestParseDecisionTypes(t *testing.T) {
	for _, decisionType := range []DecisionType{DecisionGoalCompleted, DecisionContinue, DecisionReplan, DecisionFailed} {
		t.Run(string(decisionType), func(t *testing.T) {
			decision, err := ParseDecision(`{"decision":"` + string(decisionType) + `","reason":"evidence"}`)
			if err != nil || decision.Type != decisionType {
				t.Fatalf("parse decision: %+v err=%v", decision, err)
			}
		})
	}
}

func TestParseDecisionRejectsMalformedLLMOutput(t *testing.T) {
	for _, content := range []string{
		`{"decision":"MAYBE","reason":"unknown"}`,
		`{"decision":"REPLAN"}`,
		`{"decision":"FAILED","reason":"x","command":"rm"}`,
		"```json\n{}\n```",
	} {
		if _, err := ParseDecision(content); !errors.Is(err, ErrMalformedDecision) {
			t.Fatalf("expected malformed decision for %q, got %v", content, err)
		}
	}
}

func TestReplanPreservesAuthoritativeGoal(t *testing.T) {
	capability := planner.Capability{
		Name: "test.capability", Description: "test", InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{}},
	}
	client := controllerTestClient{content: `{"goal":"model rewrote the goal","tasks":[{"id":"new-task","name":"new","description":"new task","capability":"test.capability","arguments":{},"depends_on":[]}]}`}
	controller, err := NewLLMController(client, []planner.Capability{capability})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := controller.Replan(context.Background(), ReplanRequest{
		PreviousPlan: planner.Plan{Goal: "authoritative user goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Goal != "authoritative user goal" {
		t.Fatalf("replan changed the authoritative goal: %q", plan.Goal)
	}
}
