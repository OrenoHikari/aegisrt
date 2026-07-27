package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"aegisrt/internal/agent"
)

type fakeExecutor struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (f *fakeExecutor) Run(
	ctx context.Context,
	acb *agent.ACB,
) error {
	f.mu.Lock()
	f.active++

	if f.active > f.maxActive {
		f.maxActive = f.active
	}

	f.mu.Unlock()

	acb.Transition(agent.StateRunning)

	select {
	case <-time.After(50 * time.Millisecond):
		acb.Transition(agent.StateCompleted)

	case <-ctx.Done():
		acb.Transition(agent.StateFailed)

		f.mu.Lock()
		f.active--
		f.mu.Unlock()

		return ctx.Err()
	}

	f.mu.Lock()
	f.active--
	f.mu.Unlock()

	return nil
}

func TestSchedulerLimitsConcurrency(t *testing.T) {
	executor := &fakeExecutor{}

	s, err := New(executor, 2, 8)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	s.Start()

	for index := 0; index < 5; index++ {
		acb := agent.New(
			"agent-test-"+string(rune('A'+index)),
			"test-worker",
			"fake",
			nil,
		)

		err = s.Submit(Job{
			Agent:   acb,
			Timeout: time.Second,
		})
		if err != nil {
			t.Fatalf("submit job %d: %v", index, err)
		}
	}

	s.Wait()
	s.Stop()

	executor.mu.Lock()
	maxActive := executor.maxActive
	executor.mu.Unlock()

	if maxActive != 2 {
		t.Fatalf(
			"expected maximum concurrency 2, got %d",
			maxActive,
		)
	}

	for _, record := range s.Snapshot() {
		if record.Phase != PhaseSucceeded {
			t.Fatalf(
				"Agent %s finished in phase %s",
				record.ID,
				record.Phase,
			)
		}
	}
}

func TestSchedulerRejectsDuplicateAgentID(t *testing.T) {
	executor := &fakeExecutor{}

	s, err := New(executor, 1, 4)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	s.Start()

	first := agent.New("duplicate-agent", "test", "fake", nil)
	second := agent.New("duplicate-agent", "test", "fake", nil)

	if err := s.Submit(Job{
		Agent:   first,
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("submit first Agent: %v", err)
	}

	if err := s.Submit(Job{
		Agent:   second,
		Timeout: time.Second,
	}); err == nil {
		t.Fatal("expected duplicate Agent ID error")
	}

	s.Wait()
	s.Stop()
}
