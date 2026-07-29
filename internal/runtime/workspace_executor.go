package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextfs"
)

// AgentExecutor is implemented by Runner and execution wrappers.
type AgentExecutor interface {
	Run(ctx context.Context, acb *agent.ACB) error
}

// WorkspaceRetention defines workspace lifecycle policy.
type WorkspaceRetention string

const (
	// WorkspaceCleanupAlways removes the workspace after every run.
	WorkspaceCleanupAlways WorkspaceRetention = "cleanup-always"

	// WorkspaceRetainOnFailure preserves failed Agent workspaces.
	WorkspaceRetainOnFailure WorkspaceRetention = "retain-on-failure"

	// WorkspaceRetainAlways preserves every Agent workspace.
	WorkspaceRetainAlways WorkspaceRetention = "retain-always"
)

// WorkspaceExecutor prepares ContextFS workspaces before delegating
// process execution to another executor, normally Runner.
type WorkspaceExecutor struct {
	next       AgentExecutor
	workspaces *contextfs.WorkspaceManager
	retention  WorkspaceRetention
}

// NewWorkspaceExecutor creates a workspace-aware execution wrapper.
func NewWorkspaceExecutor(
	next AgentExecutor,
	workspaces *contextfs.WorkspaceManager,
	retention WorkspaceRetention,
) (*WorkspaceExecutor, error) {
	if next == nil {
		return nil, fmt.Errorf("next Agent executor is required")
	}

	if workspaces == nil {
		return nil, fmt.Errorf("workspace manager is required")
	}

	switch retention {
	case "":
		retention = WorkspaceCleanupAlways

	case WorkspaceCleanupAlways,
		WorkspaceRetainOnFailure,
		WorkspaceRetainAlways:
	default:
		return nil, fmt.Errorf(
			"unsupported workspace retention policy %q",
			retention,
		)
	}

	return &WorkspaceExecutor{
		next:       next,
		workspaces: workspaces,
		retention:  retention,
	}, nil
}

// Run prepares an Agent-local execution environment and delegates the
// actual process lifecycle to the wrapped executor.
func (e *WorkspaceExecutor) Run(
	ctx context.Context,
	acb *agent.ACB,
) error {
	if acb == nil {
		return fmt.Errorf("Agent control block is required")
	}

	// Agents without contexts remain fully backward-compatible.
	if len(acb.Contexts) == 0 {
		return e.next.Run(ctx, acb)
	}

	requests, digests, err :=
		buildMaterializationRequests(acb)
	if err != nil {
		return err
	}

	workspace, err := e.workspaces.Prepare(
		ctx,
		acb.ID,
		requests,
	)
	if err != nil {
		return fmt.Errorf(
			"prepare Agent workspace: %w",
			err,
		)
	}

	acb.WorkspacePath = workspace.Root
	acb.WorkspaceRetained = true
	acb.WorkingDirectory = workspace.Root

	if acb.Environment == nil {
		acb.Environment = make(map[string]string)
	}

	acb.Environment["AEGIS_AGENT_ID"] = acb.ID
	acb.Environment["AEGIS_WORKSPACE_ROOT"] = workspace.Root
	acb.Environment["AEGIS_CONTEXT_INPUTS"] = workspace.InputsDir
	acb.Environment["AEGIS_CONTEXT_PRIVATE"] = workspace.PrivateDir
	acb.Environment["AEGIS_CONTEXT_MANIFEST"] = workspace.Manifest
	acb.Environment["AEGIS_CONTEXT_COUNT"] = strconv.Itoa(len(digests))
	acb.Environment["AEGIS_CONTEXT_DIGESTS"] =
		strings.Join(digests, ",")

	runErr := e.next.Run(ctx, acb)

	retain := e.retention == WorkspaceRetainAlways ||
		(e.retention == WorkspaceRetainOnFailure &&
			runErr != nil)

	if retain {
		acb.WorkspaceRetained = true
		return runErr
	}

	cleanupErr := e.workspaces.Cleanup(acb.ID)

	if cleanupErr != nil {
		// The directory may still exist, so report it as retained.
		acb.WorkspaceRetained = true

		wrappedCleanupErr := fmt.Errorf(
			"cleanup Agent workspace: %w",
			cleanupErr,
		)

		if runErr != nil {
			return errors.Join(
				runErr,
				wrappedCleanupErr,
			)
		}

		return wrappedCleanupErr
	}

	acb.WorkspaceRetained = false

	return runErr
}

func buildMaterializationRequests(
	acb *agent.ACB,
) ([]contextfs.MaterializeRequest, []string, error) {
	requests := make(
		[]contextfs.MaterializeRequest,
		0,
		len(acb.Contexts)*2,
	)

	digests := make([]string, 0, len(acb.Contexts))

	for _, ref := range acb.Contexts {
		digest := strings.ToLower(
			strings.TrimSpace(ref.Digest),
		)

		if digest == "" {
			return nil, nil, fmt.Errorf(
				"Agent context %q has no resolved digest",
				ref.Key,
			)
		}

		name := filepath.ToSlash(
			filepath.Join(
				"sha256",
				digest+".ctx",
			),
		)

		requests = append(
			requests,
			contextfs.MaterializeRequest{
				Name:   name,
				Digest: digest,
				Access: contextfs.AccessReadOnly,
			},
			contextfs.MaterializeRequest{
				Name:   name,
				Digest: digest,
				Access: contextfs.AccessPrivate,
			},
		)

		digests = append(digests, digest)
	}

	return requests, digests, nil
}
