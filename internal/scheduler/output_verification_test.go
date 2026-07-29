package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"aegisrt/internal/agent"
)

type testOutputVerifier struct {
	failAgent string
}

func (v testOutputVerifier) Verify(
	_ context.Context,
	output agent.DependencyOutput,
) (agent.OutputVerification, error) {
	if output.AgentID == v.failAgent {
		return agent.OutputVerification{},
			errors.New("simulated SHA-256 mismatch")
	}

	return agent.OutputVerification{
		Method: "sha256-manifest",
		ManifestSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		VerifiedAt: time.Now().UTC(),
		FileCount:  output.FileCount,
		TotalBytes: output.TotalBytes,
	}, nil
}

type verificationTestExecutor struct {
	mu    sync.Mutex
	order []string
}

func (e *verificationTestExecutor) Run(
	_ context.Context,
	acb *agent.ACB,
) error {
	e.mu.Lock()
	e.order = append(e.order, acb.ID)
	e.mu.Unlock()

	acb.OutputCommitted = true
	acb.OutputTransactionID =
		acb.ID + "-transaction"
	acb.OutputCommitPath =
		"/committed/" + acb.ID
	acb.OutputManifestPath =
		acb.OutputCommitPath + "/.aegis-commit.json"
	acb.OutputFileCount = 1
	acb.OutputBytes = 64

	return nil
}

func TestIntegrityFailureBlocksDependents(t *testing.T) {
	executor := &verificationTestExecutor{}

	s, err := NewWithOptions(
		executor,
		Options{
			WorkerCount: 2,
			QueueSize:   8,
			Policy:      FIFOPolicy{},
			OutputVerifier: testOutputVerifier{
				failAgent: "tampered-producer",
			},
		},
	)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	jobs := []Job{
		{
			Agent: agent.New(
				"tampered-producer",
				"producer",
				"fake",
				nil,
			),
			Timeout: time.Second,
		},
		{
			Agent: agent.New(
				"blocked-consumer",
				"consumer",
				"fake",
				nil,
			),
			Timeout: time.Second,
			DependsOn: []string{
				"tampered-producer",
			},
		},
		{
			Agent: agent.New(
				"blocked-grandchild",
				"consumer",
				"fake",
				nil,
			),
			Timeout: time.Second,
			DependsOn: []string{
				"blocked-consumer",
			},
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

	if len(order) != 1 ||
		order[0] != "tampered-producer" {
		t.Fatalf(
			"unexpected execution order %v",
			order,
		)
	}

	phases := make(map[string]Phase)

	for _, record := range s.Snapshot() {
		phases[record.ID] = record.Phase
	}

	if phases["tampered-producer"] != PhaseFailed {
		t.Fatalf(
			"expected producer FAILED, got %s",
			phases["tampered-producer"],
		)
	}

	if phases["blocked-consumer"] != PhaseBlocked {
		t.Fatalf(
			"expected consumer BLOCKED, got %s",
			phases["blocked-consumer"],
		)
	}

	if phases["blocked-grandchild"] != PhaseBlocked {
		t.Fatalf(
			"expected grandchild BLOCKED, got %s",
			phases["blocked-grandchild"],
		)
	}
}

func TestVerifiedOutputReachesConsumer(t *testing.T) {
	executor := &verificationTestExecutor{}

	s, err := NewWithOptions(
		executor,
		Options{
			WorkerCount:    1,
			QueueSize:      4,
			Policy:         FIFOPolicy{},
			OutputVerifier: testOutputVerifier{},
		},
	)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	if err := s.Submit(Job{
		Agent: agent.New(
			"verified-producer",
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
			"verified-consumer",
			"consumer",
			"fake",
			nil,
		),
		Timeout: time.Second,
		DependsOn: []string{
			"verified-producer",
		},
	}); err != nil {
		t.Fatalf("submit consumer: %v", err)
	}

	s.Start()
	s.Wait()
	s.Stop()

	for _, record := range s.Snapshot() {
		if record.ID != "verified-producer" {
			continue
		}

		if !record.OutputVerified {
			t.Fatal("producer output was not verified")
		}

		if record.OutputVerificationMethod !=
			"sha256-manifest" {
			t.Fatalf(
				"unexpected verification method %q",
				record.OutputVerificationMethod,
			)
		}
	}
}
