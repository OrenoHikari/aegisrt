package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
)

type agentPlan struct {
	ID      string
	Seconds uint64
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/hello_agent.py",
		"path to the Python Agent worker",
	)

	concurrency := flag.Int(
		"concurrency",
		2,
		"maximum number of concurrently running Agents",
	)

	disableCgroup := flag.Bool(
		"disable-cgroup",
		false,
		"disable cgroup isolation for local testing",
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
		"logs/scheduler-events.jsonl",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		log.Fatalf("open Scheduler log: %v", err)
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

	s, err := scheduler.New(runner, *concurrency, 16)
	if err != nil {
		log.Fatalf("create Scheduler: %v", err)
	}

	s.Start()

	plans := []agentPlan{
		{ID: "agent-batch-001", Seconds: 4},
		{ID: "agent-batch-002", Seconds: 4},
		{ID: "agent-batch-003", Seconds: 2},
		{ID: "agent-batch-004", Seconds: 2},
		{ID: "agent-batch-005", Seconds: 1},
	}

	startedAt := time.Now()

	for _, plan := range plans {
		acb := agent.New(
			plan.ID,
			"batch-hello-worker",
			"python3",
			[]string{
				absoluteWorkerPath,
				"--seconds",
				strconv.FormatUint(plan.Seconds, 10),
			},
		)

		acb.Resources = resource.Spec{
			CPUQuotaPercent: 25,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		}

		if err := s.Submit(scheduler.Job{
			Agent:   acb,
			Timeout: 10 * time.Second,
		}); err != nil {
			log.Fatalf("submit %s: %v", plan.ID, err)
		}

		emit(output, map[string]any{
			"source":   "scheduler",
			"event":    "submitted",
			"agent_id": plan.ID,
			"seconds":  plan.Seconds,
		})
	}

	s.Wait()
	s.Stop()

	records := s.Snapshot()
	failed := 0

	for _, record := range records {
		emit(output, map[string]any{
			"source": "scheduler",
			"event":  "final_record",
			"record": record,
		})

		if record.Phase != scheduler.PhaseSucceeded {
			failed++
		}
	}

	elapsed := time.Since(startedAt)

	emit(output, map[string]any{
		"source":          "scheduler",
		"event":           "summary",
		"agents":          len(records),
		"failed":          failed,
		"concurrency":     *concurrency,
		"elapsed_seconds": elapsed.Seconds(),
	})

	if failed > 0 {
		os.Exit(1)
	}
}

func emit(writer io.Writer, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal event: %v\n", err)
		return
	}

	_, _ = writer.Write(append(data, '\n'))
}
