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
	"sort"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
)

type zeroPressure struct{}

func (zeroPressure) Sample() (pressure.Snapshot, error) {
	return pressure.Snapshot{
		Timestamp: time.Now().UTC(),
	}, nil
}

type plan struct {
	ID         string
	Label      string
	ContextKey string
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/workspace_agent.py",
		"path to the workspace Agent",
	)

	root := flag.String(
		"root",
		"var/contextfs-execution-demo",
		"demo root",
	)

	reset := flag.Bool(
		"reset",
		true,
		"remove existing demo data",
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
		filepath.Join(*root, "store"),
	)
	if err != nil {
		log.Fatalf("open ContextFS: %v", err)
	}

	workspaceManager, err :=
		contextfs.NewWorkspaceManager(
			store,
			filepath.Join(*root, "agents"),
		)
	if err != nil {
		log.Fatalf("create workspace manager: %v", err)
	}

	if err := workspaceManager.CleanupStaging(); err != nil {
		log.Fatalf("cleanup staging workspaces: %v", err)
	}

	originalPayload := bytes.Repeat(
		[]byte(
			"shared immutable execution context\n",
		),
		4096,
	)

	object, err := store.PutBytes(
		context.Background(),
		originalPayload,
	)
	if err != nil {
		log.Fatalf("put ContextFS object: %v", err)
	}

	bindings := []string{
		"agent://workspace/first",
		"agent://workspace/second",
	}

	for _, name := range bindings {
		if _, err := store.Bind(
			name,
			object.Digest,
		); err != nil {
			log.Fatalf("bind %s: %v", name, err)
		}
	}

	resolver, err :=
		contextstore.NewContextFSResolver(store)
	if err != nil {
		log.Fatalf("create ContextFS resolver: %v", err)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create logs directory: %v", err)
	}

	logFile, err := os.OpenFile(
		"logs/workspace-execution-events.jsonl",
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o644,
	)
	if err != nil {
		log.Fatalf("open event log: %v", err)
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

	executor, err :=
		agentRuntime.NewWorkspaceExecutor(
			baseRunner,
			workspaceManager,
			agentRuntime.WorkspaceCleanupAlways,
		)
	if err != nil {
		log.Fatalf(
			"create workspace executor: %v",
			err,
		)
	}

	registry := contextstore.NewRegistry()

	s, err := scheduler.NewWithOptions(
		executor,
		scheduler.Options{
			WorkerCount:     1,
			QueueSize:       8,
			Policy:          scheduler.NewCAPSPolicy(),
			PressureSource:  zeroPressure{},
			ContextRegistry: registry,
			ContextResolver: resolver,
		},
	)
	if err != nil {
		log.Fatalf("create Scheduler: %v", err)
	}

	plans := []plan{
		{
			ID:         "agent-workspace-exec-001",
			Label:      "first-agent",
			ContextKey: bindings[0],
		},
		{
			ID:         "agent-workspace-exec-002",
			Label:      "second-agent",
			ContextKey: bindings[1],
		},
	}

	for _, item := range plans {
		acb := agent.New(
			item.ID,
			"workspace-worker",
			"python3",
			[]string{
				absoluteWorker,
				"--agent-label",
				item.Label,
			},
		)

		acb.Resources = resource.Spec{
			CPUQuotaPercent: 25,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		}

		err := s.Submit(scheduler.Job{
			Agent:   acb,
			Timeout: 10 * time.Second,
			Demand: scheduler.Demand{
				CPU:    0.2,
				Memory: 0.2,
				IO:     0.1,
			},
			Contexts: []contextstore.Ref{
				{
					Key: item.ContextKey,
				},
			},
		})
		if err != nil {
			log.Fatalf(
				"submit %s: %v",
				item.ID,
				err,
			)
		}
	}

	s.Start()
	s.Wait()
	s.Stop()

	records := s.Snapshot()

	sort.Slice(records, func(i, j int) bool {
		if records[i].StartedAt == nil {
			return false
		}

		if records[j].StartedAt == nil {
			return true
		}

		return records[i].StartedAt.Before(
			*records[j].StartedAt,
		)
	})

	failed := 0
	allCleaned := true

	for _, record := range records {
		emit(output, map[string]any{
			"source": "workspace-execution-demo",
			"event":  "dispatch_record",
			"record": record,
		})

		if record.Phase != scheduler.PhaseSucceeded {
			failed++
		}

		if record.WorkspaceRetained {
			allCleaned = false
			continue
		}

		if record.WorkspacePath == "" {
			allCleaned = false
			continue
		}

		_, statErr := os.Stat(record.WorkspacePath)

		if !errors.Is(statErr, os.ErrNotExist) {
			allCleaned = false
		}
	}

	blobAfter, err := os.ReadFile(object.Path)
	if err != nil {
		log.Fatalf("read ContextFS object: %v", err)
	}

	blobUnchanged :=
		bytes.Equal(blobAfter, originalPayload)

	emit(output, map[string]any{
		"source": "workspace-execution-demo",
		"event":  "summary",
		"agents": len(records),
		"failed": failed,
		"contextfs_object": map[string]any{
			"digest":     object.Digest,
			"size_bytes": object.SizeBytes,
			"unchanged":  blobUnchanged,
		},
		"workspaces_cleaned": allCleaned,
		"warm_registry":      registry.Snapshot(),
	})

	if failed > 0 || !allCleaned || !blobUnchanged {
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
