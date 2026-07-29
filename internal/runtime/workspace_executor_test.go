package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
)

type workspaceProbeExecutor struct {
	digest string
	fail   bool
}

func (e *workspaceProbeExecutor) Run(
	_ context.Context,
	acb *agent.ACB,
) error {
	root := acb.Environment["AEGIS_WORKSPACE_ROOT"]
	inputs := acb.Environment["AEGIS_CONTEXT_INPUTS"]
	private := acb.Environment["AEGIS_CONTEXT_PRIVATE"]

	if root == "" || inputs == "" || private == "" {
		return errors.New(
			"workspace environment is incomplete",
		)
	}

	if acb.WorkingDirectory != root {
		return errors.New(
			"working directory does not match workspace root",
		)
	}

	inputPath := filepath.Join(
		inputs,
		"sha256",
		e.digest+".ctx",
	)

	privatePath := filepath.Join(
		private,
		"sha256",
		e.digest+".ctx",
	)

	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}

	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		return err
	}

	if string(inputData) != string(privateData) {
		return errors.New(
			"private context does not match its initial input",
		)
	}

	if err := os.WriteFile(
		privatePath,
		[]byte("private Agent modification"),
		0o644,
	); err != nil {
		return err
	}

	if e.fail {
		return errors.New("simulated Agent failure")
	}

	return nil
}

func TestWorkspaceExecutorCleansSuccessfulWorkspace(
	t *testing.T,
) {
	store, err := contextfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	original := []byte("immutable ContextFS object")

	object, err := store.PutBytes(
		context.Background(),
		original,
	)
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	manager, err := contextfs.NewWorkspaceManager(
		store,
		filepath.Join(t.TempDir(), "agents"),
	)
	if err != nil {
		t.Fatalf("create workspace manager: %v", err)
	}

	executor, err := NewWorkspaceExecutor(
		&workspaceProbeExecutor{
			digest: object.Digest,
		},
		manager,
		WorkspaceCleanupAlways,
	)
	if err != nil {
		t.Fatalf("create workspace executor: %v", err)
	}

	acb := agent.New(
		"agent-workspace-success",
		"test",
		"fake",
		nil,
	)

	acb.Contexts = []contextstore.Ref{
		{
			Key:       "dataset://shared",
			Digest:    object.Digest,
			SizeBytes: object.SizeBytes,
		},
	}

	if err := executor.Run(
		context.Background(),
		acb,
	); err != nil {
		t.Fatalf("execute Agent: %v", err)
	}

	if acb.WorkspaceRetained {
		t.Fatal("successful workspace should be cleaned")
	}

	if acb.WorkspacePath == "" {
		t.Fatal("workspace path was not recorded")
	}

	if _, err := os.Stat(
		acb.WorkspacePath,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(
			"workspace still exists after cleanup: %v",
			err,
		)
	}

	blobData, err := os.ReadFile(object.Path)
	if err != nil {
		t.Fatalf("read ContextFS object: %v", err)
	}

	if string(blobData) != string(original) {
		t.Fatal("private Agent modification polluted ContextFS")
	}
}

func TestWorkspaceExecutorRetainsFailedWorkspace(
	t *testing.T,
) {
	store, err := contextfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open ContextFS: %v", err)
	}

	object, err := store.PutBytes(
		context.Background(),
		[]byte("failure-debug context"),
	)
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	manager, err := contextfs.NewWorkspaceManager(
		store,
		filepath.Join(t.TempDir(), "agents"),
	)
	if err != nil {
		t.Fatalf("create workspace manager: %v", err)
	}

	executor, err := NewWorkspaceExecutor(
		&workspaceProbeExecutor{
			digest: object.Digest,
			fail:   true,
		},
		manager,
		WorkspaceRetainOnFailure,
	)
	if err != nil {
		t.Fatalf("create workspace executor: %v", err)
	}

	acb := agent.New(
		"agent-workspace-failure",
		"test",
		"fake",
		nil,
	)

	acb.Contexts = []contextstore.Ref{
		{
			Key:       "dataset://failure",
			Digest:    object.Digest,
			SizeBytes: object.SizeBytes,
		},
	}

	err = executor.Run(context.Background(), acb)
	if err == nil {
		t.Fatal("expected simulated Agent failure")
	}

	if !acb.WorkspaceRetained {
		t.Fatal("failed Agent workspace should be retained")
	}

	if _, err := os.Stat(acb.WorkspacePath); err != nil {
		t.Fatalf(
			"retained workspace is unavailable: %v",
			err,
		)
	}

	if err := manager.Cleanup(acb.ID); err != nil {
		t.Fatalf("cleanup retained workspace: %v", err)
	}
}
