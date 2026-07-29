package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"aegisrt/internal/contextfs"
)

func main() {
	root := flag.String(
		"root",
		"var/contextfs-workspace-demo",
		"demo root",
	)

	reset := flag.Bool(
		"reset",
		true,
		"remove demo data before running",
	)

	flag.Parse()

	if *reset {
		if err := os.RemoveAll(*root); err != nil {
			fatal(err)
		}
	}

	store, err := contextfs.Open(
		filepath.Join(*root, "store"),
	)
	if err != nil {
		fatal(err)
	}

	manager, err := contextfs.NewWorkspaceManager(
		store,
		filepath.Join(*root, "agents"),
	)
	if err != nil {
		fatal(err)
	}

	if err := manager.CleanupStaging(); err != nil {
		fatal(err)
	}

	originalData := []byte(
		"shared model context\n" +
			"system prompt: safe and deterministic\n",
	)

	object, err := store.PutBytes(
		context.Background(),
		originalData,
	)
	if err != nil {
		fatal(err)
	}

	agentA, err := manager.Prepare(
		context.Background(),
		"agent-workspace-a",
		[]contextfs.MaterializeRequest{
			{
				Name:   "model/context.txt",
				Digest: object.Digest,
				Access: contextfs.AccessReadOnly,
			},
			{
				Name:   "model/context.txt",
				Digest: object.Digest,
				Access: contextfs.AccessPrivate,
			},
		},
	)
	if err != nil {
		fatal(err)
	}

	privatePath := filepath.Join(
		agentA.PrivateDir,
		"model",
		"context.txt",
	)

	modifiedData := []byte(
		"Agent A private context modification\n",
	)

	if err := os.WriteFile(
		privatePath,
		modifiedData,
		0o644,
	); err != nil {
		fatal(err)
	}

	agentB, err := manager.Prepare(
		context.Background(),
		"agent-workspace-b",
		[]contextfs.MaterializeRequest{
			{
				Name:   "model/context.txt",
				Digest: object.Digest,
				Access: contextfs.AccessReadOnly,
			},
		},
	)
	if err != nil {
		fatal(err)
	}

	blobData, err := os.ReadFile(object.Path)
	if err != nil {
		fatal(err)
	}

	agentBData, err := os.ReadFile(
		filepath.Join(
			agentB.InputsDir,
			"model",
			"context.txt",
		),
	)
	if err != nil {
		fatal(err)
	}

	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		fatal(err)
	}

	emit(map[string]any{
		"source": "workspace-demo",
		"event":  "summary",
		"contextfs_object": map[string]any{
			"digest":                        object.Digest,
			"size_bytes":                    object.SizeBytes,
			"hash_after_agent_modification": hash(blobData),
			"unchanged":                     string(blobData) == string(originalData),
		},
		"agent_a": map[string]any{
			"workspace":                  agentA,
			"private_hash":               hash(privateData),
			"private_diverged_from_blob": string(privateData) != string(blobData),
		},
		"agent_b": map[string]any{
			"workspace":          agentB,
			"readonly_hash":      hash(agentBData),
			"still_matches_blob": string(agentBData) == string(blobData),
		},
	})
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func emit(payload any) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fatal(err)
	}

	fmt.Println(string(data))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "workspace demo error:", err)
	os.Exit(1)
}
