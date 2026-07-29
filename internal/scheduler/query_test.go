package scheduler

import (
	"context"
	"testing"
	"time"

	"aegisrt/internal/agent"
)

type queryTestExecutor struct{}

func (queryTestExecutor) Run(
	_ context.Context,
	_ *agent.ACB,
) error {
	return nil
}

func TestSchedulerStatusAndRecordQuery(t *testing.T) {
	s, err := New(queryTestExecutor{}, 1, 4)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	for _, id := range []string{
		"query-agent-001",
		"query-agent-002",
	} {
		if err := s.Submit(Job{
			Agent: agent.New(
				id,
				"query-test",
				"fake",
				nil,
			),
			Timeout: time.Second,
		}); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
	}

	before := s.Status()

	if before.Started {
		t.Fatal("Scheduler should not be started yet")
	}

	if before.QueueDepth != 2 {
		t.Fatalf(
			"expected queue depth 2, got %d",
			before.QueueDepth,
		)
	}

	record, exists := s.Record("query-agent-001")
	if !exists {
		t.Fatal("Agent record was not found")
	}

	if record.Phase != PhaseQueued {
		t.Fatalf(
			"expected QUEUED, got %s",
			record.Phase,
		)
	}

	s.Start()
	s.Wait()
	s.Stop()

	after := s.Status()

	if !after.Started || !after.Stopped {
		t.Fatalf(
			"unexpected Scheduler state: %+v",
			after,
		)
	}

	if after.PhaseCounts[PhaseSucceeded] != 2 {
		t.Fatalf(
			"expected two successful Agents, got %+v",
			after.PhaseCounts,
		)
	}
}
