package contextstore

import (
	"context"
	"testing"

	"aegisrt/internal/contextfs"
)

func TestContextFSResolverUsesAuthoritativeObjectData(
	t *testing.T,
) {
	store, err := contextfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	payload := []byte("authoritative context data")

	object, err := store.PutBytes(
		context.Background(),
		payload,
	)
	if err != nil {
		t.Fatalf("put context object: %v", err)
	}

	if _, err := store.Bind(
		"dataset://shared",
		object.Digest,
	); err != nil {
		t.Fatalf("bind ContextFS reference: %v", err)
	}

	resolver, err := NewContextFSResolver(store)
	if err != nil {
		t.Fatalf("create resolver: %v", err)
	}

	refs, err := resolver.Resolve([]Ref{
		{
			Key: "dataset://shared",
		},
	})
	if err != nil {
		t.Fatalf("resolve context: %v", err)
	}

	if len(refs) != 1 {
		t.Fatalf("expected one reference, got %d", len(refs))
	}

	if refs[0].Digest != object.Digest {
		t.Fatalf(
			"expected digest %s, got %s",
			object.Digest,
			refs[0].Digest,
		)
	}

	if refs[0].SizeBytes != uint64(len(payload)) {
		t.Fatalf(
			"expected size %d, got %d",
			len(payload),
			refs[0].SizeBytes,
		)
	}
}

func TestContextFSAliasesShareOneIdentity(
	t *testing.T,
) {
	store, err := contextfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	object, err := store.PutBytes(
		context.Background(),
		[]byte("shared immutable context"),
	)
	if err != nil {
		t.Fatalf("put context object: %v", err)
	}

	for _, name := range []string{
		"agent://seed/context",
		"agent://reuse/context",
	} {
		if _, err := store.Bind(
			name,
			object.Digest,
		); err != nil {
			t.Fatalf("bind %s: %v", name, err)
		}
	}

	resolver, err := NewContextFSResolver(store)
	if err != nil {
		t.Fatalf("create resolver: %v", err)
	}

	refs, err := resolver.Resolve([]Ref{
		{Key: "agent://seed/context"},
		{Key: "agent://reuse/context"},
	})
	if err != nil {
		t.Fatalf("resolve aliases: %v", err)
	}

	if len(refs) != 1 {
		t.Fatalf(
			"expected aliases to deduplicate into one object, got %d",
			len(refs),
		)
	}

	if refs[0].Digest != object.Digest {
		t.Fatal("aliases resolved to the wrong object")
	}
}
