package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
)

func TestRegistryRegisterLookupAndDuplicate(t *testing.T) {
	registration := Registration{
		Capability: planner.Capability{
			Name: "test.echo",
			InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
				"text": {Type: planner.ArgumentString, Required: true},
			}},
		},
		Build: func(_ context.Context, task planner.Task) (scheduler.Job, error) {
			return scheduler.Job{Agent: agent.New(task.ID, task.Capability, "echo", nil), Timeout: time.Second}, nil
		},
	}
	registry, err := NewRegistry([]Registration{registration})
	if err != nil {
		t.Fatalf("register capability: %v", err)
	}
	definition, exists := registry.Lookup("test.echo")
	if !exists || definition.Name != "test.echo" || len(registry.Capabilities()) != 1 {
		t.Fatalf("unexpected lookup: %+v exists=%v", definition, exists)
	}
	if _, err := NewRegistry([]Registration{registration, registration}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("expected duplicate registration error, got %v", err)
	}
}

func TestRegistryRejectsUnknownCapabilityAndInvalidArguments(t *testing.T) {
	registry, err := NewRegistry([]Registration{{
		Capability: planner.Capability{
			Name: "test.echo",
			InputSchema: planner.ArgumentSchema{Fields: map[string]planner.ArgumentField{
				"text": {Type: planner.ArgumentString, Required: true},
			}},
		},
		Build: func(_ context.Context, task planner.Task) (scheduler.Job, error) {
			return scheduler.Job{Agent: agent.New(task.ID, task.Capability, "echo", nil), Timeout: time.Second}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	base := planner.Task{ID: "task", Name: "task", Description: "task"}
	base.Capability = "test.unknown"
	if _, err := registry.Build(context.Background(), base); err == nil || !strings.Contains(err.Error(), "unregistered") {
		t.Fatalf("expected unknown capability error, got %v", err)
	}
	base.Capability = "test.echo"
	base.Arguments = map[string]any{}
	if _, err := registry.Build(context.Background(), base); err == nil || !strings.Contains(err.Error(), "required argument") {
		t.Fatalf("expected required argument error, got %v", err)
	}
	base.Arguments = map[string]any{"text": true}
	if _, err := registry.Build(context.Background(), base); err == nil || !strings.Contains(err.Error(), "must be string") {
		t.Fatalf("expected argument type error, got %v", err)
	}
}
