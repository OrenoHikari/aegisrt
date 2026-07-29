package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"aegisrt/internal/agent"
)

type dagTestExecutor struct {
	mu       sync.Mutex
	order    []string
	failures map[string]bool
}

func (e *dagTestExecutor) Run(
	_ context.Context,
	acb *agent.ACB,
) error {
	e.mu.Lock()
	e.order = append(e.order, acb.ID)
	e.mu.Unlock()

	if e.failures[acb.ID] {
		return errors.New("simulated upstream failure")
	}

	if acb.ID == "consumer-agent" {
		raw := acb.Environment["AEGIS_DEPENDENCY_OUTPUTS_JSON"]

		if raw == "" {
			return errors.New(
				"dependency outputs were not injected",
			)
		}

		var outputs map[string]agent.DependencyOutput

		if err := json.Unmarshal(
			[]byte(raw),
			&outputs,
		); err != nil {
			return err
		}

		if outputs["producer-agent"].CommitPath == "" {
			return errors.New(
				"producer commit path is missing",
			)
		}
	}

	acb.OutputCommitted = true
	acb.OutputTransactionID = acb.ID + "-transaction"
	acb.OutputCommitPath = "/committed/" + acb.ID
	acb.OutputManifestPath =
		acb.OutputCommitPath + "/.aegis-commit.json"
	acb.OutputFileCount = 1
	acb.OutputBytes = 128

	return nil
}

func TestDAGWaitsForCommittedDependency(t *testing.T) {
	executor := &dagTestExecutor{
		failures: make(map[string]bool),
	}

	s, err := New(executor, 2, 8)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	producer := agent.New(
		"producer-agent",
		"producer",
		"fake",
		nil,
	)

	consumer := agent.New(
		"consumer-agent",
		"consumer",
		"fake",
		nil,
	)

	if err := s.Submit(Job{
		Agent:   producer,
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("submit producer: %v", err)
	}

	if err := s.Submit(Job{
		Agent:     consumer,
		Timeout:   time.Second,
		DependsOn: []string{"producer-agent"},
	}); err != nil {
		t.Fatalf("submit consumer: %v", err)
	}

	s.Start()
	s.Wait()
	s.Stop()

	executor.mu.Lock()
	order := append([]string(nil), executor.order...)
	executor.mu.Unlock()

	expected := []string{
		"producer-agent",
		"consumer-agent",
	}

	if len(order) != len(expected) {
		t.Fatalf("expected order %v, got %v", expected, order)
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

	records := s.Snapshot()

	for _, record := range records {
		if record.ID == "consumer-agent" &&
			record.Phase != PhaseSucceeded {
			t.Fatalf(
				"consumer phase is %s",
				record.Phase,
			)
		}
	}
}

func TestDAGBlocksChildAfterDependencyFailure(t *testing.T) {
	executor := &dagTestExecutor{
		failures: map[string]bool{
			"failed-producer": true,
		},
	}

	s, err := New(executor, 2, 8)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	if err := s.Submit(Job{
		Agent: agent.New(
			"failed-producer",
			"producer",
			"fake",
			nil,
		),
		Timeout: time.Second,
	}); err != nil {
		t.Fatalf("submit producer: %v", err)
	}

	if err := s.Submit(Job{
		Agent: agent.New(
			"blocked-consumer",
			"consumer",
			"fake",
			nil,
		),
		Timeout:   time.Second,
		DependsOn: []string{"failed-producer"},
	}); err != nil {
		t.Fatalf("submit consumer: %v", err)
	}

	s.Start()
	s.Wait()
	s.Stop()

	executor.mu.Lock()
	order := append([]string(nil), executor.order...)
	executor.mu.Unlock()

	if len(order) != 1 ||
		order[0] != "failed-producer" {
		t.Fatalf(
			"blocked child was unexpectedly executed: %v",
			order,
		)
	}

	for _, record := range s.Snapshot() {
		if record.ID != "blocked-consumer" {
			continue
		}

		if record.Phase != PhaseBlocked {
			t.Fatalf(
				"expected BLOCKED, got %s",
				record.Phase,
			)
		}
	}
}

func TestDAGRejectsUnknownDependency(t *testing.T) {
	executor := &dagTestExecutor{
		failures: make(map[string]bool),
	}

	s, err := New(executor, 1, 4)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	err = s.Submit(Job{
		Agent: agent.New(
			"orphan-agent",
			"consumer",
			"fake",
			nil,
		),
		Timeout:   time.Second,
		DependsOn: []string{"missing-agent"},
	})

	if !errors.Is(err, ErrUnknownDependency) {
		t.Fatalf(
			"expected ErrUnknownDependency, got %v",
			err,
		)
	}
}
