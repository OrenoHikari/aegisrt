package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	Seconds    uint64
	ContextKey string
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/hello_agent.py",
		"path to the Python Agent worker",
	)

	storeRoot := flag.String(
		"contextfs-root",
		"var/contextfs-bridge-demo",
		"ContextFS root",
	)

	reset := flag.Bool(
		"reset",
		true,
		"reset ContextFS demo data before running",
	)

	disableCgroup := flag.Bool(
		"disable-cgroup",
		false,
		"disable cgroup isolation",
	)

	flag.Parse()

	absoluteWorkerPath, err :=
		filepath.Abs(*workerPath)
	if err != nil {
		log.Fatalf("resolve worker path: %v", err)
	}

	if *reset {
		if err := os.RemoveAll(*storeRoot); err != nil {
			log.Fatalf("reset ContextFS: %v", err)
		}
	}

	store, err := contextfs.Open(*storeRoot)
	if err != nil {
		log.Fatalf("open ContextFS: %v", err)
	}

	sharedPayload := bytes.Repeat(
		[]byte("shared-model-context\n"),
		32768,
	)

	coldPayload := bytes.Repeat(
		[]byte("unrelated-model-context\n"),
		32768,
	)

	sharedObject, err := store.PutBytes(
		context.Background(),
		sharedPayload,
	)
	if err != nil {
		log.Fatalf("put shared object: %v", err)
	}

	coldObject, err := store.PutBytes(
		context.Background(),
		coldPayload,
	)
	if err != nil {
		log.Fatalf("put cold object: %v", err)
	}

	bindings := map[string]string{
		"agent://seed/shared-model":    sharedObject.Digest,
		"agent://reuse/shared-model":   sharedObject.Digest,
		"agent://cold/unrelated-model": coldObject.Digest,
	}

	for name, digest := range bindings {
		if _, err := store.Bind(name, digest); err != nil {
			log.Fatalf("bind %s: %v", name, err)
		}
	}

	resolver, err :=
		contextstore.NewContextFSResolver(store)
	if err != nil {
		log.Fatalf("create ContextFS resolver: %v", err)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create log directory: %v", err)
	}

	logFile, err := os.OpenFile(
		"logs/contextfs-scheduler-events.jsonl",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		log.Fatalf("open event log: %v", err)
	}
	defer logFile.Close()

	output := io.MultiWriter(os.Stdout, logFile)

	var manager *resource.Manager

	if !*disableCgroup {
		manager, err = resource.NewManagerFromCurrent()
		if err != nil {
			log.Fatalf(
				"discover delegated cgroup: %v",
				err,
			)
		}

		if err := manager.Initialize(); err != nil {
			log.Fatalf(
				"initialize cgroup manager: %v",
				err,
			)
		}
	}

	runner := &agentRuntime.Runner{
		Log:       output,
		Resources: manager,
	}

	registry := contextstore.NewRegistry()

	s, err := scheduler.NewWithOptions(
		runner,
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
			ID:         "agent-contextfs-seed",
			Seconds:    2,
			ContextKey: "agent://seed/shared-model",
		},
		{
			ID:         "agent-contextfs-cold",
			Seconds:    1,
			ContextKey: "agent://cold/unrelated-model",
		},
		{
			ID:         "agent-contextfs-reuse",
			Seconds:    1,
			ContextKey: "agent://reuse/shared-model",
		},
	}

	for _, item := range plans {
		acb := agent.New(
			item.ID,
			"contextfs-worker",
			"python3",
			[]string{
				absoluteWorkerPath,
				"--seconds",
				strconv.FormatUint(
					item.Seconds,
					10,
				),
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
					// No manually supplied digest or size.
					Key: item.ContextKey,
				},
			},
		})
		if err != nil {
			log.Fatalf("submit %s: %v", item.ID, err)
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

	order := make([]string, 0, len(records))
	failed := 0

	for _, record := range records {
		order = append(order, record.ID)

		emit(output, map[string]any{
			"source": "contextfs-scheduler-demo",
			"event":  "dispatch_record",
			"record": record,
		})

		if record.Phase !=
			scheduler.PhaseSucceeded {
			failed++
		}
	}

	storeStats, err := store.Stats()
	if err != nil {
		log.Fatalf("read ContextFS stats: %v", err)
	}

	emit(output, map[string]any{
		"source": "contextfs-scheduler-demo",
		"event":  "summary",
		"submitted_order": []string{
			"agent-contextfs-seed",
			"agent-contextfs-cold",
			"agent-contextfs-reuse",
		},
		"dispatch_order": order,
		"shared_object": map[string]any{
			"digest":     sharedObject.Digest,
			"size_bytes": sharedObject.SizeBytes,
		},
		"cold_object": map[string]any{
			"digest":     coldObject.Digest,
			"size_bytes": coldObject.SizeBytes,
		},
		"contextfs_stats": storeStats,
		"warm_registry":   registry.Snapshot(),
		"failed":          failed,
	})

	if failed > 0 {
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
