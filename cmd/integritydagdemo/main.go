package main

import (
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
	"aegisrt/internal/outputtxn"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
)

type tamperAfterCommitExecutor struct {
	next     agentRuntime.AgentExecutor
	targetID string
}

func (e *tamperAfterCommitExecutor) Run(
	ctx context.Context,
	acb *agent.ACB,
) error {
	err := e.next.Run(ctx, acb)
	if err != nil || acb.ID != e.targetID {
		return err
	}

	path := filepath.Join(
		acb.OutputCommitPath,
		"results",
		"payload.json",
	)

	if err := os.Chmod(path, 0o644); err != nil {
		return fmt.Errorf(
			"make committed output writable: %w",
			err,
		)
	}

	if err := os.WriteFile(
		path,
		[]byte(
			"{\"producer\":\"tampered\",\"value\":999}\n",
		),
		0o644,
	); err != nil {
		return fmt.Errorf(
			"tamper committed output: %w",
			err,
		)
	}

	return nil
}

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/integrity_dag_agent.py",
		"path to the DAG Agent",
	)

	root := flag.String(
		"root",
		"var/integrity-dag-demo",
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
		log.Fatalf("resolve worker: %v", err)
	}

	if *reset {
		if err := os.RemoveAll(*root); err != nil {
			log.Fatalf("reset demo root: %v", err)
		}
	}

	outputManager, err := outputtxn.Open(
		filepath.Join(*root, "outputs"),
		outputtxn.DefaultLimits(),
	)
	if err != nil {
		log.Fatalf("open output manager: %v", err)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create logs directory: %v", err)
	}

	logFile, err := os.OpenFile(
		"logs/integrity-dag-events.jsonl",
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
				"initialize resource manager: %v",
				err,
			)
		}
	}

	baseRunner := &agentRuntime.Runner{
		Log:       output,
		Resources: resourceManager,
	}

	transactionExecutor, err :=
		agentRuntime.NewTransactionalOutputExecutor(
			baseRunner,
			outputManager,
			agentRuntime.OutputDiscardOnFailure,
		)
	if err != nil {
		log.Fatalf(
			"create transaction executor: %v",
			err,
		)
	}

	executor := &tamperAfterCommitExecutor{
		next:     transactionExecutor,
		targetID: "producer-tampered",
	}

	s, err := scheduler.NewWithOptions(
		executor,
		scheduler.Options{
			WorkerCount:    2,
			QueueSize:      16,
			Policy:         scheduler.FIFOPolicy{},
			OutputVerifier: outputManager,
		},
	)
	if err != nil {
		log.Fatalf("create Scheduler: %v", err)
	}

	type plan struct {
		ID        string
		Role      string
		Label     string
		DependsOn []string
	}

	plans := []plan{
		{
			ID:    "producer-valid",
			Role:  "producer",
			Label: "valid-producer",
		},
		{
			ID:    "producer-tampered",
			Role:  "producer",
			Label: "tampered-producer",
		},
		{
			ID:    "consumer-valid",
			Role:  "consumer",
			Label: "valid-consumer",
			DependsOn: []string{
				"producer-valid",
			},
		},
		{
			ID:    "consumer-blocked",
			Role:  "consumer",
			Label: "blocked-consumer",
			DependsOn: []string{
				"producer-tampered",
			},
		},
		{
			ID:    "grandchild-blocked",
			Role:  "consumer",
			Label: "blocked-grandchild",
			DependsOn: []string{
				"consumer-blocked",
			},
		},
	}

	for _, item := range plans {
		acb := agent.New(
			item.ID,
			item.Role,
			"python3",
			[]string{
				absoluteWorker,
				"--role",
				item.Role,
				"--label",
				item.Label,
			},
		)

		acb.Resources = resource.Spec{
			CPUQuotaPercent: 50,
			MemoryMaxBytes:  128 * 1024 * 1024,
			PidsMax:         16,
		}

		if err := s.Submit(scheduler.Job{
			Agent:     acb,
			Timeout:   10 * time.Second,
			DependsOn: item.DependsOn,
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

	records := s.Snapshot()

	sort.Slice(records, func(i, j int) bool {
		return records[i].SubmittedAt.Before(
			records[j].SubmittedAt,
		)
	})

	expectedPhases := map[string]scheduler.Phase{
		"producer-valid":     scheduler.PhaseSucceeded,
		"consumer-valid":     scheduler.PhaseSucceeded,
		"producer-tampered":  scheduler.PhaseFailed,
		"consumer-blocked":   scheduler.PhaseBlocked,
		"grandchild-blocked": scheduler.PhaseBlocked,
	}

	allPassed := true

	for _, record := range records {
		expected := expectedPhases[record.ID]

		if record.Phase != expected {
			allPassed = false
		}

		if record.ID == "producer-valid" &&
			(!record.OutputVerified ||
				record.OutputVerificationMethod !=
					"sha256-manifest") {
			allPassed = false
		}

		if record.ID == "producer-tampered" &&
			record.OutputVerified {
			allPassed = false
		}

		emit(output, map[string]any{
			"source":         "integrity-dag-demo",
			"event":          "record",
			"expected_phase": expected,
			"record":         record,
		})
	}

	_, blockedConsumerCommitErr := os.Stat(
		filepath.Join(
			outputManager.CommittedRoot(),
			"consumer-blocked",
		),
	)

	blockedConsumerNeverCommitted :=
		errors.Is(
			blockedConsumerCommitErr,
			os.ErrNotExist,
		)

	if !blockedConsumerNeverCommitted {
		allPassed = false
	}

	emit(output, map[string]any{
		"source":                           "integrity-dag-demo",
		"event":                            "summary",
		"expected_phases":                  expectedPhases,
		"blocked_consumer_never_committed": blockedConsumerNeverCommitted,
		"all_expectations_satisfied":       allPassed,
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
