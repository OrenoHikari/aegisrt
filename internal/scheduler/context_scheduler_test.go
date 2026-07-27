package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextstore"
)

type orderedExecutor struct {
	mu    sync.Mutex
	order []string
}

func (e *orderedExecutor) Run(
	_ context.Context,
	acb *agent.ACB,
) error {
	e.mu.Lock()
	e.order = append(e.order, acb.ID)
	e.mu.Unlock()

	acb.Transition(agent.StateRunning)
	time.Sleep(20 * time.Millisecond)
	acb.Transition(agent.StateCompleted)

	return nil
}

func TestSchedulerUsesNewlyWarmContext(t *testing.T) {
	executor := &orderedExecutor{}
	registry := contextstore.NewRegistry()

	s, err := NewWithOptions(
		executor,
		Options{
			WorkerCount:     1,
			QueueSize:       8,
			Policy:          NewCAPSPolicy(),
			ContextRegistry: registry,
		},
	)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	shared := []contextstore.Ref{
		{
			Key:       "shared-context",
			SizeBytes: 1024,
		},
	}

	unrelated := []contextstore.Ref{
		{
			Key:       "unrelated-context",
			SizeBytes: 1024,
		},
	}

	jobs := []Job{
		{
			Agent: agent.New(
				"seed-agent",
				"test",
				"fake",
				nil,
			),
			Timeout:  time.Second,
			Demand:   balancedDemand(),
			Contexts: shared,
		},
		{
			Agent: agent.New(
				"cold-agent",
				"test",
				"fake",
				nil,
			),
			Timeout:  time.Second,
			Demand:   balancedDemand(),
			Contexts: unrelated,
		},
		{
			Agent: agent.New(
				"reuse-agent",
				"test",
				"fake",
				nil,
			),
			Timeout:  time.Second,
			Demand:   balancedDemand(),
			Contexts: shared,
		},
	}

	for _, job := range jobs {
		if err := s.Submit(job); err != nil {
			t.Fatalf("submit Agent: %v", err)
		}
	}

	s.Start()
	s.Wait()
	s.Stop()

	executor.mu.Lock()
	order := append([]string(nil), executor.order...)
	executor.mu.Unlock()

	expected := []string{
		"seed-agent",
		"reuse-agent",
		"cold-agent",
	}

	if len(order) != len(expected) {
		t.Fatalf(
			"expected %d executions, got %d",
			len(expected),
			len(order),
		)
	}

	for index := range expected {
		if order[index] != expected[index] {
			t.Fatalf(
				"expected order %v, got %v",
				expected,
				order,
			)
		}
	}
}
