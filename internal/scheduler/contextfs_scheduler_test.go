package scheduler

import (
	"context"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
)

func TestSchedulerReusesContextAcrossContextFSAliases(
	t *testing.T,
) {
	store, err := contextfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	sharedObject, err := store.PutBytes(
		context.Background(),
		[]byte("shared ContextFS object"),
	)
	if err != nil {
		t.Fatalf("put shared object: %v", err)
	}

	coldObject, err := store.PutBytes(
		context.Background(),
		[]byte("unrelated ContextFS object"),
	)
	if err != nil {
		t.Fatalf("put cold object: %v", err)
	}

	bindings := map[string]string{
		"agent://seed/shared":    sharedObject.Digest,
		"agent://reuse/shared":   sharedObject.Digest,
		"agent://cold/unrelated": coldObject.Digest,
	}

	for name, digest := range bindings {
		if _, err := store.Bind(name, digest); err != nil {
			t.Fatalf("bind %s: %v", name, err)
		}
	}

	resolver, err :=
		contextstore.NewContextFSResolver(store)
	if err != nil {
		t.Fatalf("create ContextFS resolver: %v", err)
	}

	executor := &orderedExecutor{}
	registry := contextstore.NewRegistry()

	s, err := NewWithOptions(
		executor,
		Options{
			WorkerCount:     1,
			QueueSize:       8,
			Policy:          NewCAPSPolicy(),
			ContextRegistry: registry,
			ContextResolver: resolver,
		},
	)
	if err != nil {
		t.Fatalf("create Scheduler: %v", err)
	}

	jobs := []Job{
		{
			Agent: agent.New(
				"seed-agent",
				"test",
				"fake",
				nil,
			),
			Timeout: time.Second,
			Demand:  balancedDemand(),
			Contexts: []contextstore.Ref{
				{Key: "agent://seed/shared"},
			},
		},
		{
			Agent: agent.New(
				"cold-agent",
				"test",
				"fake",
				nil,
			),
			Timeout: time.Second,
			Demand:  balancedDemand(),
			Contexts: []contextstore.Ref{
				{Key: "agent://cold/unrelated"},
			},
		},
		{
			Agent: agent.New(
				"reuse-agent",
				"test",
				"fake",
				nil,
			),
			Timeout: time.Second,
			Demand:  balancedDemand(),
			Contexts: []contextstore.Ref{
				{Key: "agent://reuse/shared"},
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

	expected := []string{
		"seed-agent",
		"reuse-agent",
		"cold-agent",
	}

	if len(order) != len(expected) {
		t.Fatalf(
			"expected order %v, got %v",
			expected,
			order,
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

	records := s.Snapshot()

	for _, record := range records {
		if record.ID != "reuse-agent" {
			continue
		}

		if record.ContextAffinity != 1 {
			t.Fatalf(
				"expected reuse affinity 1, got %.3f",
				record.ContextAffinity,
			)
		}

		if len(record.Contexts) != 1 {
			t.Fatalf(
				"expected one resolved context, got %d",
				len(record.Contexts),
			)
		}

		if record.Contexts[0].Digest !=
			sharedObject.Digest {
			t.Fatal(
				"reuse Agent has the wrong digest",
			)
		}
	}
}
