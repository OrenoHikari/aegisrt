package contextfs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"
)

func TestPutDeduplicatesContent(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	payload := bytes.Repeat(
		[]byte("shared-context\n"),
		1024,
	)

	first, err := store.PutBytes(
		context.Background(),
		payload,
	)
	if err != nil {
		t.Fatalf("put first object: %v", err)
	}

	second, err := store.PutBytes(
		context.Background(),
		payload,
	)
	if err != nil {
		t.Fatalf("put duplicate object: %v", err)
	}

	if first.Digest != second.Digest {
		t.Fatal("expected identical digests")
	}

	if first.Path != second.Path {
		t.Fatal("expected identical blob paths")
	}

	if first.Deduplicated {
		t.Fatal("first insertion must not be deduplicated")
	}

	if !second.Deduplicated {
		t.Fatal("second insertion must be deduplicated")
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}

	if stats.Objects != 1 {
		t.Fatalf(
			"expected one physical object, got %d",
			stats.Objects,
		)
	}
}

func TestReferencesAndRefCount(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	object, err := store.PutBytes(
		context.Background(),
		[]byte("context"),
	)
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	for _, name := range []string{
		"agent://one",
		"agent://two",
	} {
		if _, err := store.Bind(
			name,
			object.Digest,
		); err != nil {
			t.Fatalf("bind %s: %v", name, err)
		}
	}

	count, err := store.RefCount(object.Digest)
	if err != nil {
		t.Fatalf("count references: %v", err)
	}

	if count != 2 {
		t.Fatalf(
			"expected two references, got %d",
			count,
		)
	}

	reference, err := store.ResolveRef("agent://one")
	if err != nil {
		t.Fatalf("resolve reference: %v", err)
	}

	if reference.Digest != object.Digest {
		t.Fatal("reference resolved to the wrong digest")
	}

	released, err := store.Release("agent://one")
	if err != nil {
		t.Fatalf("release reference: %v", err)
	}

	if !released {
		t.Fatal("expected existing reference to be released")
	}
}

func TestGCRemovesOnlyUnreferencedObjects(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	live, err := store.PutBytes(
		context.Background(),
		[]byte("live"),
	)
	if err != nil {
		t.Fatalf("put live object: %v", err)
	}

	cold, err := store.PutBytes(
		context.Background(),
		[]byte("cold"),
	)
	if err != nil {
		t.Fatalf("put cold object: %v", err)
	}

	if _, err := store.Bind(
		"dataset://live",
		live.Digest,
	); err != nil {
		t.Fatalf("bind live object: %v", err)
	}

	report, err := store.GC()
	if err != nil {
		t.Fatalf("garbage collect: %v", err)
	}

	if report.RemovedObjects != 1 ||
		report.RetainedObjects != 1 {
		t.Fatalf("unexpected GC report: %+v", report)
	}

	if _, err := store.Resolve(live.Digest); err != nil {
		t.Fatalf("live object was removed: %v", err)
	}

	if _, err := store.Resolve(
		cold.Digest,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"expected cold object to be removed, got %v",
			err,
		)
	}
}

func TestConcurrentPutSameContent(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	payload := bytes.Repeat(
		[]byte("concurrent-context"),
		4096,
	)

	const workers = 8

	objects := make([]Object, workers)
	errorsByWorker := make([]error, workers)

	var wait sync.WaitGroup

	for index := 0; index < workers; index++ {
		wait.Add(1)

		go func(worker int) {
			defer wait.Done()

			objects[worker], errorsByWorker[worker] =
				store.PutBytes(
					context.Background(),
					payload,
				)
		}(index)
	}

	wait.Wait()

	for index, err := range errorsByWorker {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
	}

	for index := 1; index < len(objects); index++ {
		if objects[index].Digest != objects[0].Digest {
			t.Fatal(
				"concurrent writes produced different digests",
			)
		}
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}

	if stats.Objects != 1 {
		t.Fatalf(
			"expected one physical object, got %d",
			stats.Objects,
		)
	}
}
