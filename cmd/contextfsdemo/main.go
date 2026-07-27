package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"aegisrt/internal/contextfs"
)

func main() {
	root := flag.String(
		"root",
		"var/contextfs-demo",
		"ContextFS demo root",
	)

	reset := flag.Bool(
		"reset",
		true,
		"remove the demo root before running",
	)

	flag.Parse()

	if *reset {
		if err := os.RemoveAll(*root); err != nil {
			fatal(err)
		}
	}

	store, err := contextfs.Open(*root)
	if err != nil {
		fatal(err)
	}

	ctx := context.Background()

	sharedPayload := bytes.Repeat(
		[]byte("shared-model-context\n"),
		65536,
	)

	coldPayload := bytes.Repeat(
		[]byte("cold-context\n"),
		32768,
	)

	sharedFirst, err := store.PutBytes(
		ctx,
		sharedPayload,
	)
	if err != nil {
		fatal(err)
	}

	sharedSecond, err := store.PutBytes(
		ctx,
		sharedPayload,
	)
	if err != nil {
		fatal(err)
	}

	cold, err := store.PutBytes(ctx, coldPayload)
	if err != nil {
		fatal(err)
	}

	for _, name := range []string{
		"agent://seed/shared-model",
		"agent://reuse/shared-model",
	} {
		if _, err := store.Bind(
			name,
			sharedFirst.Digest,
		); err != nil {
			fatal(err)
		}
	}

	beforeGC, err := store.Stats()
	if err != nil {
		fatal(err)
	}

	refCount, err := store.RefCount(sharedFirst.Digest)
	if err != nil {
		fatal(err)
	}

	gcReport, err := store.GC()
	if err != nil {
		fatal(err)
	}

	afterGC, err := store.Stats()
	if err != nil {
		fatal(err)
	}

	_, coldResolveErr := store.Resolve(cold.Digest)

	emit(map[string]any{
		"source": "contextfs-demo",
		"event":  "summary",
		"root":   store.Root(),
		"shared": map[string]any{
			"digest":                    sharedFirst.Digest,
			"same_digest_on_second_put": sharedFirst.Digest == sharedSecond.Digest,
			"second_put_deduplicated":   sharedSecond.Deduplicated,
			"reference_count":           refCount,
		},
		"cold": map[string]any{
			"digest":        cold.Digest,
			"removed_by_gc": coldResolveErr != nil,
		},
		"before_gc": beforeGC,
		"gc":        gcReport,
		"after_gc":  afterGC,
	})
}

func emit(payload any) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fatal(err)
	}

	fmt.Println(string(data))
}

func fatal(err error) {
	fmt.Fprintln(
		os.Stderr,
		"ContextFS demo error:",
		err,
	)
	os.Exit(1)
}
