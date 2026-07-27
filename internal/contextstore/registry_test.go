package contextstore

import "testing"

func TestRegistryCalculatesAffinity(t *testing.T) {
	registry := NewRegistry()

	err := registry.Add([]Ref{
		{
			Key:       "context-A",
			SizeBytes: 100,
		},
	})
	if err != nil {
		t.Fatalf("add context: %v", err)
	}

	snapshot := registry.Snapshot()

	refs := []Ref{
		{
			Key:       "context-A",
			SizeBytes: 100,
		},
		{
			Key:       "context-B",
			SizeBytes: 100,
		},
	}

	if reusable := snapshot.ReusableBytes(refs); reusable != 100 {
		t.Fatalf("expected 100 reusable bytes, got %d", reusable)
	}

	if affinity := snapshot.Affinity(refs); affinity != 0.5 {
		t.Fatalf("expected affinity 0.5, got %.2f", affinity)
	}
}

func TestNormalizeRefsDeduplicatesKeys(t *testing.T) {
	refs, err := NormalizeRefs([]Ref{
		{Key: "context-A", SizeBytes: 50},
		{Key: "context-A", SizeBytes: 100},
	})
	if err != nil {
		t.Fatalf("normalize contexts: %v", err)
	}

	if len(refs) != 1 {
		t.Fatalf("expected one context, got %d", len(refs))
	}

	if refs[0].SizeBytes != 100 {
		t.Fatalf(
			"expected maximum size 100, got %d",
			refs[0].SizeBytes,
		)
	}
}
