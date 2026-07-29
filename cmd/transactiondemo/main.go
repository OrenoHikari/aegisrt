package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
	"aegisrt/internal/outputtxn"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
)

type plan struct {
	ID          string
	Label       string
	Mode        string
	ExpectError bool
}

type result struct {
	ID                   string            `json:"id"`
	Mode                 string            `json:"mode"`
	Error                string            `json:"error,omitempty"`
	ExpectationSatisfied bool              `json:"expectation_satisfied"`
	OutputState          agent.OutputState `json:"output_state"`
	OutputCommitted      bool              `json:"output_committed"`
	OutputRetained       bool              `json:"output_retained"`
	OutputCommitPath     string            `json:"output_commit_path,omitempty"`
	OutputManifestPath   string            `json:"output_manifest_path,omitempty"`
	OutputFileCount      int               `json:"output_file_count"`
	OutputBytes          uint64            `json:"output_bytes"`
	WorkspaceCleaned     bool              `json:"workspace_cleaned"`
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/transaction_agent.py",
		"path to the transaction Agent",
	)

	root := flag.String(
		"root",
		"var/output-transaction-demo",
		"demo root",
	)

	reset := flag.Bool(
		"reset",
		true,
		"remove previous demo data",
	)

	disableCgroup := flag.Bool(
		"disable-cgroup",
		false,
		"disable cgroup isolation",
	)

	flag.Parse()

	absoluteWorker, err := filepath.Abs(*workerPath)
	if err != nil {
		log.Fatalf("resolve worker path: %v", err)
	}

	if *reset {
		if err := os.RemoveAll(*root); err != nil {
			log.Fatalf("reset demo root: %v", err)
		}
	}

	store, err := contextfs.Open(
		filepath.Join(*root, "contextfs"),
	)
	if err != nil {
		log.Fatalf("open ContextFS: %v", err)
	}

	contextPayload := bytes.Repeat(
		[]byte("transaction demo context\n"),
		1024,
	)

	contextObject, err := store.PutBytes(
		context.Background(),
		contextPayload,
	)
	if err != nil {
		log.Fatalf("put ContextFS object: %v", err)
	}

	workspaceManager, err :=
		contextfs.NewWorkspaceManager(
			store,
			filepath.Join(*root, "workspaces"),
		)
	if err != nil {
		log.Fatalf("create workspace manager: %v", err)
	}

	outputManager, err := outputtxn.Open(
		filepath.Join(*root, "outputs"),
		outputtxn.DefaultLimits(),
	)
	if err != nil {
		log.Fatalf(
			"open transactional output manager: %v",
			err,
		)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create logs directory: %v", err)
	}

	logFile, err := os.OpenFile(
		"logs/transaction-events.jsonl",
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o644,
	)
	if err != nil {
		log.Fatalf("open transaction log: %v", err)
	}
	defer logFile.Close()

	output := io.MultiWriter(os.Stdout, logFile)

	var resourceManager *resource.Manager

	if !*disableCgroup {
		resourceManager, err =
			resource.NewManagerFromCurrent()
		if err != nil {
			log.Fatalf(
				"discover delegated cgroup: %v",
				err,
			)
		}

		if err := resourceManager.Initialize(); err != nil {
			log.Fatalf(
				"initialize cgroup manager: %v",
				err,
			)
		}
	}

	baseRunner := &agentRuntime.Runner{
		Log:       output,
		Resources: resourceManager,
	}

	outputExecutor, err :=
		agentRuntime.NewTransactionalOutputExecutor(
			baseRunner,
			outputManager,
			agentRuntime.OutputDiscardOnFailure,
		)
	if err != nil {
		log.Fatalf(
			"create output executor: %v",
			err,
		)
	}

	executor, err := agentRuntime.NewWorkspaceExecutor(
		outputExecutor,
		workspaceManager,
		agentRuntime.WorkspaceCleanupAlways,
	)
	if err != nil {
		log.Fatalf(
			"create workspace executor: %v",
			err,
		)
	}

	plans := []plan{
		{
			ID:          "agent-output-success",
			Label:       "successful-agent",
			Mode:        "success",
			ExpectError: false,
		},
		{
			ID:          "agent-output-failure",
			Label:       "failing-agent",
			Mode:        "fail",
			ExpectError: true,
		},
		{
			ID:          "agent-output-symlink",
			Label:       "invalid-output-agent",
			Mode:        "symlink",
			ExpectError: true,
		},
	}

	results := make([]result, 0, len(plans))
	allPassed := true

	for _, item := range plans {
		acb := agent.New(
			item.ID,
			"transaction-worker",
			"python3",
			[]string{
				absoluteWorker,
				"--mode",
				item.Mode,
				"--label",
				item.Label,
			},
		)

		acb.Contexts = []contextstore.Ref{
			{
				Key:       "context://transaction-demo",
				Digest:    contextObject.Digest,
				SizeBytes: contextObject.SizeBytes,
			},
		}

		acb.Resources = resource.Spec{
			CPUQuotaPercent: 50,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		}

		ctx, cancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)

		runErr := executor.Run(ctx, acb)
		cancel()

		expectationSatisfied :=
			(runErr != nil) == item.ExpectError

		if item.Mode == "success" {
			expectationSatisfied =
				expectationSatisfied &&
					acb.OutputCommitted &&
					acb.OutputState ==
						agent.OutputStateCommitted

			if expectationSatisfied {
				_, artifactErr := os.Stat(
					filepath.Join(
						acb.OutputCommitPath,
						"results",
						"answer.json",
					),
				)

				_, manifestErr := os.Stat(
					acb.OutputManifestPath,
				)

				expectationSatisfied =
					artifactErr == nil &&
						manifestErr == nil
			}
		} else {
			expectationSatisfied =
				expectationSatisfied &&
					!acb.OutputCommitted &&
					acb.OutputState ==
						agent.OutputStateDiscarded

			if expectationSatisfied {
				_, stagingErr := os.Stat(
					acb.OutputStagingPath,
				)

				expectationSatisfied =
					errors.Is(
						stagingErr,
						os.ErrNotExist,
					)
			}
		}

		_, workspaceErr := os.Stat(
			acb.WorkspacePath,
		)

		workspaceCleaned :=
			errors.Is(workspaceErr, os.ErrNotExist)

		expectationSatisfied =
			expectationSatisfied && workspaceCleaned

		errorMessage := ""

		if runErr != nil {
			errorMessage = runErr.Error()
		}

		results = append(results, result{
			ID:                   item.ID,
			Mode:                 item.Mode,
			Error:                errorMessage,
			ExpectationSatisfied: expectationSatisfied,
			OutputState:          acb.OutputState,
			OutputCommitted:      acb.OutputCommitted,
			OutputRetained:       acb.OutputRetained,
			OutputCommitPath:     acb.OutputCommitPath,
			OutputManifestPath:   acb.OutputManifestPath,
			OutputFileCount:      acb.OutputFileCount,
			OutputBytes:          acb.OutputBytes,
			WorkspaceCleaned:     workspaceCleaned,
		})

		if !expectationSatisfied {
			allPassed = false
		}
	}

	blobAfter, err := os.ReadFile(contextObject.Path)
	if err != nil {
		log.Fatalf("read ContextFS object: %v", err)
	}

	contextUnchanged :=
		bytes.Equal(blobAfter, contextPayload)

	if !contextUnchanged {
		allPassed = false
	}

	emit(output, map[string]any{
		"source":                     "transaction-demo",
		"event":                      "summary",
		"results":                    results,
		"contextfs_unchanged":        contextUnchanged,
		"committed_root":             outputManager.CommittedRoot(),
		"staging_root":               outputManager.StagingRoot(),
		"all_expectations_satisfied": allPassed,
	})

	if !allPassed {
		os.Exit(1)
	}
}

func emit(writer io.Writer, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	_, _ = writer.Write(append(data, '\n'))
}
