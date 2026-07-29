package outputtxn

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aegisrt/internal/agent"
)

func createVerifiedTestOutput(
	t *testing.T,
	manager *Manager,
	agentID string,
) (
	Transaction,
	CommitResult,
	agent.DependencyOutput,
) {
	t.Helper()

	transaction, err := manager.Begin(agentID)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	path := filepath.Join(
		transaction.StagingDir,
		"results",
		"answer.json",
	)

	if err := os.MkdirAll(
		filepath.Dir(path),
		0o755,
	); err != nil {
		t.Fatalf("create result directory: %v", err)
	}

	if err := os.WriteFile(
		path,
		[]byte(`{"answer":42}`),
		0o644,
	); err != nil {
		t.Fatalf("write result: %v", err)
	}

	result, err := manager.Commit(
		context.Background(),
		transaction,
	)
	if err != nil {
		t.Fatalf("commit transaction: %v", err)
	}

	output := agent.DependencyOutput{
		AgentID:       agentID,
		TransactionID: result.TransactionID,
		CommitPath:    result.FinalDir,
		ManifestPath:  result.ManifestPath,
		FileCount:     result.FileCount,
		TotalBytes:    result.TotalBytes,
	}

	return transaction, result, output
}

func TestVerifyCommittedOutput(t *testing.T) {
	manager, err := Open(
		t.TempDir(),
		DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}

	_, _, output := createVerifiedTestOutput(
		t,
		manager,
		"agent-verified",
	)

	verification, err := manager.Verify(
		context.Background(),
		output,
	)
	if err != nil {
		t.Fatalf("verify output: %v", err)
	}

	if verification.Method != "sha256-manifest" {
		t.Fatalf(
			"unexpected verification method %q",
			verification.Method,
		)
	}

	if len(verification.ManifestSHA256) != 64 {
		t.Fatalf(
			"invalid manifest digest %q",
			verification.ManifestSHA256,
		)
	}

	if verification.FileCount != 1 {
		t.Fatalf(
			"expected one verified file, got %d",
			verification.FileCount,
		)
	}
}

func TestVerifyDetectsModifiedArtifact(t *testing.T) {
	manager, err := Open(
		t.TempDir(),
		DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}

	_, result, output := createVerifiedTestOutput(
		t,
		manager,
		"agent-tampered",
	)

	path := filepath.Join(
		result.FinalDir,
		"results",
		"answer.json",
	)

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("make output writable: %v", err)
	}

	if err := os.WriteFile(
		path,
		[]byte(`{"answer":99}`),
		0o644,
	); err != nil {
		t.Fatalf("modify committed output: %v", err)
	}

	if _, err := manager.Verify(
		context.Background(),
		output,
	); err == nil {
		t.Fatal("expected modified-output verification failure")
	}
}

func TestVerifyDetectsUnmanifestedFile(t *testing.T) {
	manager, err := Open(
		t.TempDir(),
		DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}

	_, result, output := createVerifiedTestOutput(
		t,
		manager,
		"agent-extra-file",
	)

	if err := os.WriteFile(
		filepath.Join(result.FinalDir, "extra.txt"),
		[]byte("unmanifested"),
		0o444,
	); err != nil {
		t.Fatalf("create extra file: %v", err)
	}

	if _, err := manager.Verify(
		context.Background(),
		output,
	); err == nil {
		t.Fatal("expected unmanifested-file verification failure")
	}
}
