package main

import (
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
	"aegisrt/internal/contextstore"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
)

type plan struct {
	ID       string
	Seconds  uint64
	Contexts []contextstore.Ref
}

type zeroPressure struct{}

func (zeroPressure) Sample() (pressure.Snapshot, error) {
	return pressure.Snapshot{
		Timestamp: time.Now().UTC(),
	}, nil
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/hello_agent.py",
		"path to the Python worker",
	)

	disableCgroup := flag.Bool(
		"disable-cgroup",
		false,
		"disable cgroup isolation",
	)

	flag.Parse()

	absoluteWorkerPath, err := filepath.Abs(*workerPath)
	if err != nil {
		log.Fatalf("resolve worker path: %v", err)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create logs directory: %v", err)
	}

	logFile, err := os.OpenFile(
		"logs/affinity-events.jsonl",
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
			log.Fatalf("discover delegated cgroup: %v", err)
		}

		if err := manager.Initialize(); err != nil {
			log.Fatalf("initialize cgroup manager: %v", err)
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
		},
	)
	if err != nil {
		log.Fatalf("create Scheduler: %v", err)
	}

	sharedContext := []contextstore.Ref{
		{
			Key:       "dataset://aegisrt/shared-corpus",
			SizeBytes: 64 * 1024 * 1024,
		},
	}

	coldContext := []contextstore.Ref{
		{
			Key:       "dataset://aegisrt/unrelated-corpus",
			SizeBytes: 64 * 1024 * 1024,
		},
	}

	plans := []plan{
		{
			ID:       "agent-context-seed",
			Seconds:  2,
			Contexts: sharedContext,
		},
		{
			ID:       "agent-context-cold",
			Seconds:  1,
			Contexts: coldContext,
		},
		{
			ID:       "agent-context-reuse",
			Seconds:  1,
			Contexts: sharedContext,
		},
	}

	for _, item := range plans {
		acb := agent.New(
			item.ID,
			"context-demo-worker",
			"python3",
			[]string{
				absoluteWorkerPath,
				"--seconds",
				strconv.FormatUint(item.Seconds, 10),
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
			Contexts: item.Contexts,
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
			"source": "affinity-demo",
			"event":  "dispatch_record",
			"record": record,
		})

		if record.Phase != scheduler.PhaseSucceeded {
			failed++
		}
	}

	emit(output, map[string]any{
		"source": "affinity-demo",
		"event":  "summary",
		"submitted_order": []string{
			"agent-context-seed",
			"agent-context-cold",
			"agent-context-reuse",
		},
		"dispatch_order":   order,
		"context_registry": registry.Snapshot(),
		"failed":           failed,
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
