package outputtxn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSuccessfulTransactionCommitsAtomically(
	t *testing.T,
) {
	manager, err := Open(
		t.TempDir(),
		DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	transaction, err := manager.Begin("agent-success")
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	outputPath := filepath.Join(
		transaction.StagingDir,
		"results",
		"answer.json",
	)

	if err := os.MkdirAll(
		filepath.Dir(outputPath),
		0o755,
	); err != nil {
		t.Fatalf("create output directory: %v", err)
	}

	if err := os.WriteFile(
		outputPath,
		[]byte(`{"answer":42}`),
		0o644,
	); err != nil {
		t.Fatalf("write staged output: %v", err)
	}

	result, err := manager.Commit(
		context.Background(),
		transaction,
	)
	if err != nil {
		t.Fatalf("commit output: %v", err)
	}

	if _, err := os.Stat(
		transaction.StagingDir,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"staging directory still exists: %v",
			err,
		)
	}

	committedPath := filepath.Join(
		result.FinalDir,
		"results",
		"answer.json",
	)

	data, err := os.ReadFile(committedPath)
	if err != nil {
		t.Fatalf("read committed output: %v", err)
	}

	if string(data) != `{"answer":42}` {
		t.Fatalf(
			"unexpected committed output: %s",
			data,
		)
	}

	info, err := os.Stat(committedPath)
	if err != nil {
		t.Fatalf("stat committed output: %v", err)
	}

	if info.Mode().Perm() != 0o444 {
		t.Fatalf(
			"expected committed mode 0444, got %04o",
			info.Mode().Perm(),
		)
	}

	if _, err := os.Stat(
		result.ManifestPath,
	); err != nil {
		t.Fatalf("commit manifest is missing: %v", err)
	}
}

func TestTransactionRejectsSymbolicLinks(t *testing.T) {
	manager, err := Open(
		t.TempDir(),
		DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	transaction, err := manager.Begin("agent-symlink")
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	linkPath := filepath.Join(
		transaction.StagingDir,
		"host-link",
	)

	if err := os.Symlink(
		"/etc/hostname",
		linkPath,
	); err != nil {
		t.Fatalf("create symbolic link: %v", err)
	}

	_, err = manager.Commit(
		context.Background(),
		transaction,
	)

	if err == nil {
		t.Fatal("expected symbolic-link validation failure")
	}

	if _, err := os.Stat(
		transaction.FinalDir,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"invalid transaction was committed: %v",
			err,
		)
	}
}

func TestAbortRemovesStagingOnly(t *testing.T) {
	manager, err := Open(
		t.TempDir(),
		DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	transaction, err := manager.Begin("agent-abort")
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(transaction.StagingDir, "partial.txt"),
		[]byte("partial"),
		0o644,
	); err != nil {
		t.Fatalf("write partial output: %v", err)
	}

	if err := manager.Abort(transaction); err != nil {
		t.Fatalf("abort transaction: %v", err)
	}

	if _, err := os.Stat(
		transaction.StagingDir,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"aborted staging directory still exists: %v",
			err,
		)
	}

	if _, err := os.Stat(
		transaction.FinalDir,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"aborted transaction was committed: %v",
			err,
		)
	}
}

func TestTransactionEnforcesByteLimit(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxBytes = 4

	manager, err := Open(t.TempDir(), limits)
	if err != nil {
		t.Fatalf("open output manager: %v", err)
	}

	transaction, err := manager.Begin("agent-limit")
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(transaction.StagingDir, "large.txt"),
		[]byte("12345"),
		0o644,
	); err != nil {
		t.Fatalf("write output: %v", err)
	}

	if _, err := manager.Commit(
		context.Background(),
		transaction,
	); err == nil {
		t.Fatal("expected byte-limit validation failure")
	}
}
