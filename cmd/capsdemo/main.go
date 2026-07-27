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
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
)

type agentPlan struct {
	ID      string
	Profile string
	Demand  scheduler.Demand
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/profile_agent.py",
		"path to the profile Agent worker",
	)

	policyName := flag.String(
		"policy",
		"caps",
		"scheduling policy: fifo or caps",
	)

	concurrency := flag.Int(
		"concurrency",
		1,
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

	if _, err := os.Stat(absoluteWorkerPath); err != nil {
		log.Fatalf("profile worker is unavailable: %v", err)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create logs directory: %v", err)
	}

	logPath := fmt.Sprintf(
		"logs/%s-events.jsonl",
		strings.ToLower(*policyName),
	)

	logFile, err := os.OpenFile(
		logPath,
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

	var policy scheduler.Policy

	switch strings.ToLower(*policyName) {
	case "fifo":
		policy = scheduler.FIFOPolicy{}

	case "caps":
		policy = scheduler.NewCAPSPolicy()

	default:
		log.Fatalf(
			"unsupported policy %q; expected fifo or caps",
			*policyName,
		)
	}

	s, err := scheduler.NewWithOptions(
		runner,
		scheduler.Options{
			WorkerCount:    *concurrency,
			QueueSize:      16,
			Policy:         policy,
			PressureSource: pressure.NewReader(),
		},
	)
	if err != nil {
		log.Fatalf("create Scheduler: %v", err)
	}

	plans := []agentPlan{
		{
			ID:      "agent-cpu-heavy",
			Profile: "cpu",
			Demand: scheduler.Demand{
				CPU:    1.0,
				Memory: 0.1,
				IO:     0.1,
			},
		},
		{
			ID:      "agent-memory-heavy",
			Profile: "memory",
			Demand: scheduler.Demand{
				CPU:    0.1,
				Memory: 1.0,
				IO:     0.1,
			},
		},
		{
			ID:      "agent-io-heavy",
			Profile: "io",
			Demand: scheduler.Demand{
				CPU:    0.1,
				Memory: 0.1,
				IO:     1.0,
			},
		},
	}

	// Submit all jobs before starting workers. This guarantees that the
	// policy can compare the complete candidate set.
	for _, plan := range plans {
		acb := agent.New(
			plan.ID,
			plan.Profile+"-worker",
			"python3",
			[]string{
				absoluteWorkerPath,
				"--profile",
				plan.Profile,
				"--seconds",
				"3",
			},
		)

		acb.Resources = resource.Spec{
			CPUQuotaPercent: 100,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		}

		err := s.Submit(scheduler.Job{
			Agent:   acb,
			Timeout: 10 * time.Second,
			Demand:  plan.Demand,
		})
		if err != nil {
			log.Fatalf("submit %s: %v", plan.ID, err)
		}

		emit(output, map[string]any{
			"source":   "caps-demo",
			"event":    "submitted",
			"policy":   strings.ToLower(*policyName),
			"agent_id": plan.ID,
			"profile":  plan.Profile,
			"demand":   plan.Demand,
		})
	}

	startedAt := time.Now()

	s.Start()
	s.Wait()
	s.Stop()

	records := s.Snapshot()
	failed := 0

	dispatchRecords := append(
		[]scheduler.Record(nil),
		records...,
	)

	sort.Slice(dispatchRecords, func(i, j int) bool {
		if dispatchRecords[i].StartedAt == nil {
			return false
		}

		if dispatchRecords[j].StartedAt == nil {
			return true
		}

		return dispatchRecords[i].StartedAt.Before(
			*dispatchRecords[j].StartedAt,
		)
	})

	dispatchOrder := make([]string, 0, len(dispatchRecords))

	for _, record := range dispatchRecords {
		dispatchOrder = append(dispatchOrder, record.ID)

		emit(output, map[string]any{
			"source": "caps-demo",
			"event":  "dispatch_record",
			"policy": strings.ToLower(*policyName),
			"record": record,
		})

		if record.Phase != scheduler.PhaseSucceeded {
			failed++
		}
	}

	emit(output, map[string]any{
		"source": "caps-demo",
		"event":  "summary",
		"policy": strings.ToLower(*policyName),
		"submitted_order": []string{
			"agent-cpu-heavy",
			"agent-memory-heavy",
			"agent-io-heavy",
		},
		"dispatch_order":  dispatchOrder,
		"failed":          failed,
		"elapsed_seconds": time.Since(startedAt).Seconds(),
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
