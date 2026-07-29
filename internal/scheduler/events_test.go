package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/telemetry"
)

type eventTestExecutor struct {
	fail map[string]bool
}

func (e *eventTestExecutor) Run(
	_ context.Context,
	acb *agent.ACB,
) error {
	if e.fail[acb.ID] {
		return errors.New("simulated event-test failure")
	}

	acb.OutputCommitted = true
	acb.OutputTransactionID =
		acb.ID + "-transaction"
	acb.OutputCommitPath =
		"/committed/" + acb.ID
	acb.OutputManifestPath =
		acb.OutputCommitPath + "/.aegis-commit.json"
	acb.OutputFileCount = 1
	acb.OutputBytes = 32

	return nil
}

func TestSchedulerEmitsUnifiedLifecycleEvents(
	t *testing.T,
) {
	memory := telemetry.NewMemorySink(100)

	bus, err := telemetry.NewBus(32, memory)
	if err != nil {
		t.Fatalf("create event bus: %v", err)
	}

	s, err := NewWithOptions(
		&eventTestExecutor{
			fail: make(map[string]bool),
		},
		Options{
			WorkerCount:    1,
			QueueSize:      4,
			Policy:         FIFOPolicy{},
			OutputVerifier: TrustOutputVerifier{},
			EventPublisher: bus,
		},
	)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	if err := s.Submit(Job{
		Agent: agent.New(
			"event-agent",
			"test",
			"fake",
			nil,
		),
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("submit Agent: %v", err)
	}

	s.Start()
	s.Wait()
	s.Stop()

	closeCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := bus.Close(closeCtx); err != nil {
		t.Fatalf("close event bus: %v", err)
	}

	kinds := make(map[telemetry.Kind]int)

	for _, event := range memory.Snapshot() {
		kinds[event.Kind]++
	}

	required := []telemetry.Kind{
		telemetry.KindAgentSubmitted,
		telemetry.KindPressureSampled,
		telemetry.KindAgentDispatched,
		telemetry.KindOutputCommitted,
		telemetry.KindOutputVerified,
		telemetry.KindAgentFinished,
	}

	for _, kind := range required {
		if kinds[kind] == 0 {
			t.Fatalf(
				"required event %q was not emitted",
				kind,
			)
		}
	}
}

func TestSchedulerEmitsBlockedEvent(t *testing.T) {
	memory := telemetry.NewMemorySink(100)

	bus, err := telemetry.NewBus(32, memory)
	if err != nil {
		t.Fatalf("create event bus: %v", err)
	}

	s, err := NewWithOptions(
		&eventTestExecutor{
			fail: map[string]bool{
				"failed-parent": true,
			},
		},
		Options{
			WorkerCount:    1,
			QueueSize:      4,
			Policy:         FIFOPolicy{},
			EventPublisher: bus,
		},
	)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	if err := s.Submit(Job{
		Agent: agent.New(
			"failed-parent",
			"producer",
			"fake",
			nil,
		),
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("submit parent: %v", err)
	}

	if err := s.Submit(Job{
		Agent: agent.New(
			"blocked-child",
			"consumer",
			"fake",
			nil,
		),
		Timeout: time.Second,
		DependsOn: []string{
			"failed-parent",
		},
	}); err != nil {
		t.Fatalf("submit child: %v", err)
	}

	s.Start()
	s.Wait()
	s.Stop()

	closeCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := bus.Close(closeCtx); err != nil {
		t.Fatalf("close event bus: %v", err)
	}

	foundBlocked := false

	for _, event := range memory.Snapshot() {
		if event.Kind ==
			telemetry.KindAgentBlocked {
			foundBlocked = true
			break
		}
	}

	if !foundBlocked {
		t.Fatal("BLOCKED event was not emitted")
	}
}
