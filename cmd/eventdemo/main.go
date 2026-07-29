package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/outputtxn"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

type plan struct {
	ID        string
	Role      string
	Command   string
	Args      []string
	DependsOn []string
}

func main() {
	integrityWorker := flag.String(
		"integrity-worker",
		"worker/python/integrity_dag_agent.py",
		"path to the integrity DAG Agent",
	)

	transactionWorker := flag.String(
		"transaction-worker",
		"worker/python/transaction_agent.py",
		"path to the transaction Agent",
	)

	root := flag.String(
		"root",
		"var/event-demo",
		"event demo root",
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

	integrityPath, err := filepath.Abs(
		*integrityWorker,
	)
	if err != nil {
		log.Fatalf(
			"resolve integrity worker: %v",
			err,
		)
	}

	transactionPath, err := filepath.Abs(
		*transactionWorker,
	)
	if err != nil {
		log.Fatalf(
			"resolve transaction worker: %v",
			err,
		)
	}

	if *reset {
		if err := os.RemoveAll(*root); err != nil {
			log.Fatalf("reset demo root: %v", err)
		}
	}

	if err := os.MkdirAll(*root, 0o755); err != nil {
		log.Fatalf("create demo root: %v", err)
	}

	outputManager, err := outputtxn.Open(
		filepath.Join(*root, "outputs"),
		outputtxn.DefaultLimits(),
	)
	if err != nil {
		log.Fatalf("open output manager: %v", err)
	}

	memorySink := telemetry.NewMemorySink(10000)

	jsonlSink, err := telemetry.OpenJSONLSink(
		filepath.Join(
			*root,
			"runtime-events.jsonl",
		),
	)
	if err != nil {
		log.Fatalf("open JSONL event sink: %v", err)
	}

	eventBus, err := telemetry.NewBus(
		4096,
		memorySink,
		jsonlSink,
	)
	if err != nil {
		log.Fatalf("create event bus: %v", err)
	}

	agentLog, err := os.OpenFile(
		filepath.Join(*root, "agent-events.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o644,
	)
	if err != nil {
		log.Fatalf("open Agent log: %v", err)
	}
	defer agentLog.Close()

	output := io.MultiWriter(os.Stdout, agentLog)

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
				"initialize resource manager: %v",
				err,
			)
		}
	}

	baseRunner := &agentRuntime.Runner{
		Log:       output,
		Resources: resourceManager,
	}

	executor, err :=
		agentRuntime.NewTransactionalOutputExecutor(
			baseRunner,
			outputManager,
			agentRuntime.OutputDiscardOnFailure,
		)
	if err != nil {
		log.Fatalf(
			"create transactional executor: %v",
			err,
		)
	}

	s, err := scheduler.NewWithOptions(
		executor,
		scheduler.Options{
			WorkerCount:    2,
			QueueSize:      16,
			Policy:         scheduler.NewCAPSPolicy(),
			PressureSource: pressure.NewReader(),
			OutputVerifier: outputManager,
			EventPublisher: eventBus,
		},
	)
	if err != nil {
		log.Fatalf("create Scheduler: %v", err)
	}

	plans := []plan{
		{
			ID:      "event-producer-success",
			Role:    "producer",
			Command: "python3",
			Args: []string{
				integrityPath,
				"--role",
				"producer",
				"--label",
				"event-producer",
			},
		},
		{
			ID:      "event-consumer-success",
			Role:    "consumer",
			Command: "python3",
			Args: []string{
				integrityPath,
				"--role",
				"consumer",
				"--label",
				"event-consumer",
			},
			DependsOn: []string{
				"event-producer-success",
			},
		},
		{
			ID:      "event-producer-fail",
			Role:    "producer",
			Command: "python3",
			Args: []string{
				transactionPath,
				"--mode",
				"fail",
				"--label",
				"event-failing-producer",
			},
		},
		{
			ID:      "event-child-blocked",
			Role:    "consumer",
			Command: "python3",
			Args: []string{
				transactionPath,
				"--mode",
				"success",
				"--label",
				"event-blocked-child",
			},
			DependsOn: []string{
				"event-producer-fail",
			},
		},
	}

	for _, item := range plans {
		acb := agent.New(
			item.ID,
			item.Role,
			item.Command,
			item.Args,
		)

		acb.Resources = resource.Spec{
			CPUQuotaPercent: 50,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		}

		if err := s.Submit(scheduler.Job{
			Agent:     acb,
			Timeout:   15 * time.Second,
			DependsOn: item.DependsOn,
			Demand: scheduler.Demand{
				CPU:    0.3,
				Memory: 0.2,
				IO:     0.2,
			},
		}); err != nil {
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

	closeCtx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	if err := eventBus.Close(closeCtx); err != nil {
		log.Fatalf("close event bus: %v", err)
	}

	records := s.Snapshot()

	sort.Slice(records, func(i, j int) bool {
		return records[i].SubmittedAt.Before(
			records[j].SubmittedAt,
		)
	})

	expected := map[string]scheduler.Phase{
		"event-producer-success": scheduler.PhaseSucceeded,
		"event-consumer-success": scheduler.PhaseSucceeded,
		"event-producer-fail":    scheduler.PhaseFailed,
		"event-child-blocked":    scheduler.PhaseBlocked,
	}

	allPassed := true

	for _, record := range records {
		if record.Phase != expected[record.ID] {
			allPassed = false
		}
	}

	events := memorySink.Snapshot()
	eventCounts := make(map[string]int)

	for _, event := range events {
		eventCounts[string(event.Kind)]++
	}

	requiredKinds := []telemetry.Kind{
		telemetry.KindAgentSubmitted,
		telemetry.KindPressureSampled,
		telemetry.KindAgentDispatched,
		telemetry.KindAgentBlocked,
		telemetry.KindOutputCommitted,
		telemetry.KindOutputVerified,
		telemetry.KindAgentFinished,
	}

	for _, kind := range requiredKinds {
		if eventCounts[string(kind)] == 0 {
			allPassed = false
		}
	}

	summary := map[string]any{
		"source": "event-demo",
		"event":  "summary",

		"expected_phases": expected,
		"records":         records,

		"event_count":  len(events),
		"event_counts": eventCounts,
		"bus_stats":    eventBus.Stats(),

		"jsonl_path": jsonlSink.Path(),

		"all_expectations_satisfied": allPassed,
	}

	data, err := json.Marshal(summary)
	if err != nil {
		log.Fatalf("encode summary: %v", err)
	}

	fmt.Println(string(data))

	if !allPassed {
		os.Exit(1)
	}
}
