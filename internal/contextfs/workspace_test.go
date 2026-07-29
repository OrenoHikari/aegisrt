package contextfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePrivateCopyDoesNotModifyBlob(
	t *testing.T,
) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	original := []byte("immutable shared context\n")

	object, err := store.PutBytes(
		context.Background(),
		original,
	)
	if err != nil {
		t.Fatalf("put context: %v", err)
	}

	manager, err := NewWorkspaceManager(
		store,
		filepath.Join(t.TempDir(), "workspaces"),
	)
	if err != nil {
		t.Fatalf("create workspace manager: %v", err)
	}

	workspace, err := manager.Prepare(
		context.Background(),
		"agent-test-001",
		[]MaterializeRequest{
			{
				Name:   "shared.txt",
				Digest: object.Digest,
				Access: AccessReadOnly,
			},
			{
				Name:   "editable.txt",
				Digest: object.Digest,
				Access: AccessPrivate,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare workspace: %v", err)
	}

	privatePath := filepath.Join(
		workspace.PrivateDir,
		"editable.txt",
	)

	if err := os.WriteFile(
		privatePath,
		[]byte("Agent private modification\n"),
		0o644,
	); err != nil {
		t.Fatalf("modify private context: %v", err)
	}

	blobData, err := os.ReadFile(object.Path)
	if err != nil {
		t.Fatalf("read ContextFS blob: %v", err)
	}

	if string(blobData) != string(original) {
		t.Fatalf(
			"ContextFS blob was modified: %q",
			string(blobData),
		)
	}

	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatalf("read private context: %v", err)
	}

	if string(privateData) ==
		string(blobData) {
		t.Fatal(
			"private modification did not diverge from blob",
		)
	}
}

func TestWorkspaceReadOnlyMaterialization(
	t *testing.T,
) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	object, err := store.PutBytes(
		context.Background(),
		[]byte("read-only context"),
	)
	if err != nil {
		t.Fatalf("put context: %v", err)
	}

	manager, err := NewWorkspaceManager(
		store,
		filepath.Join(t.TempDir(), "workspaces"),
	)
	if err != nil {
		t.Fatalf("create workspace manager: %v", err)
	}

	workspace, err := manager.Prepare(
		context.Background(),
		"agent-readonly",
		[]MaterializeRequest{
			{
				Name:   "dataset/input.txt",
				Digest: object.Digest,
				Access: AccessReadOnly,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare workspace: %v", err)
	}

	path := filepath.Join(
		workspace.InputsDir,
		"dataset",
		"input.txt",
	)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat materialized file: %v", err)
	}

	if info.Mode().Perm() != 0o444 {
		t.Fatalf(
			"expected permissions 0444, got %o",
			info.Mode().Perm(),
		)
	}

	file, err := os.OpenFile(
		path,
		os.O_WRONLY,
		0,
	)

	if err == nil {
		_ = file.Close()

		t.Fatal(
			"expected direct write to read-only context to fail",
		)
	}
}

func TestWorkspaceRejectsPathTraversal(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	object, err := store.PutBytes(
		context.Background(),
		[]byte("context"),
	)
	if err != nil {
		t.Fatalf("put context: %v", err)
	}

	manager, err := NewWorkspaceManager(
		store,
		filepath.Join(t.TempDir(), "workspaces"),
	)
	if err != nil {
		t.Fatalf("create workspace manager: %v", err)
	}

	_, err = manager.Prepare(
		context.Background(),
		"agent-traversal",
		[]MaterializeRequest{
			{
				Name:   "../escape.txt",
				Digest: object.Digest,
				Access: AccessPrivate,
			},
		},
	)

	if err == nil {
		t.Fatal("expected path traversal rejection")
	}
}

func TestWorkspacePublicationAndCleanup(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	object, err := store.PutBytes(
		context.Background(),
		[]byte("context"),
	)
	if err != nil {
		t.Fatalf("put context: %v", err)
	}

	root := filepath.Join(t.TempDir(), "workspaces")

	manager, err := NewWorkspaceManager(store, root)
	if err != nil {
		t.Fatalf("create workspace manager: %v", err)
	}

	workspace, err := manager.Prepare(
		context.Background(),
		"agent-cleanup",
		[]MaterializeRequest{
			{
				Name:   "context.txt",
				Digest: object.Digest,
				Access: AccessPrivate,
			},
		},
	)
	if err != nil {
		t.Fatalf("prepare workspace: %v", err)
	}

	opened, err := manager.OpenWorkspace(
		"agent-cleanup",
	)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	if opened.AgentID != workspace.AgentID {
		t.Fatal("opened the wrong workspace")
	}

	if err := manager.Cleanup(
		"agent-cleanup",
	); err != nil {
		t.Fatalf("cleanup workspace: %v", err)
	}

	_, err = os.Stat(workspace.Root)

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"workspace still exists after cleanup: %v",
			err,
		)
	}

	if _, err := store.Resolve(
		object.Digest,
	); err != nil {
		t.Fatalf(
			"workspace cleanup removed ContextFS blob: %v",
			err,
		)
	}
}
