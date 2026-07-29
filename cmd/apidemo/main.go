package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/controlapi"
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
	listenAddress := flag.String(
		"listen",
		"127.0.0.1:18080",
		"HTTP listen address",
	)

	integrityWorker := flag.String(
		"integrity-worker",
		"worker/python/integrity_dag_agent.py",
		"path to the integrity DAG worker",
	)

	transactionWorker := flag.String(
		"transaction-worker",
		"worker/python/transaction_agent.py",
		"path to the transaction worker",
	)

	root := flag.String(
		"root",
		"var/api-demo",
		"API demo root",
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

	shutdownAfter := flag.Duration(
		"shutdown-after",
		0,
		"automatically stop after this duration",
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
			log.Fatalf("reset API root: %v", err)
		}
	}

	if err := os.MkdirAll(*root, 0o755); err != nil {
		log.Fatalf("create API root: %v", err)
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
		filepath.Join(*root, "runtime-events.jsonl"),
	)
	if err != nil {
		log.Fatalf("open event log: %v", err)
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

	api, err := controlapi.New(
		s,
		memorySink,
		eventBus,
	)
	if err != nil {
		log.Fatalf("create Runtime API: %v", err)
	}

	httpServer := &http.Server{
		Addr:              *listenAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	plans := []plan{
		{
			ID:      "api-producer-success",
			Role:    "producer",
			Command: "python3",
			Args: []string{
				integrityPath,
				"--role",
				"producer",
				"--label",
				"api-producer",
			},
		},
		{
			ID:      "api-consumer-success",
			Role:    "consumer",
			Command: "python3",
			Args: []string{
				integrityPath,
				"--role",
				"consumer",
				"--label",
				"api-consumer",
			},
			DependsOn: []string{
				"api-producer-success",
			},
		},
		{
			ID:      "api-producer-fail",
			Role:    "producer",
			Command: "python3",
			Args: []string{
				transactionPath,
				"--mode",
				"fail",
				"--label",
				"api-failing-producer",
			},
		},
		{
			ID:      "api-child-blocked",
			Role:    "consumer",
			Command: "python3",
			Args: []string{
				transactionPath,
				"--mode",
				"success",
				"--label",
				"api-blocked-child",
			},
			DependsOn: []string{
				"api-producer-fail",
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

	serverErrors := make(chan error, 1)

	go func() {
		err := httpServer.ListenAndServe()

		if err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	startedEvent := map[string]any{
		"source": "api-demo",
		"event":  "api_started",
		"listen": *listenAddress,
		"root":   *root,
	}

	startedData, _ := json.Marshal(startedEvent)
	fmt.Println(string(startedData))

	s.Start()

	workDone := make(chan struct{})

	go func() {
		s.Wait()
		s.Stop()
		close(workDone)
	}()

	runContext, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stopSignals()

	if *shutdownAfter > 0 {
		var cancel context.CancelFunc

		runContext, cancel = context.WithTimeout(
			runContext,
			*shutdownAfter,
		)

		defer cancel()
	}

	select {
	case <-runContext.Done():

	case serverErr := <-serverErrors:
		log.Printf("HTTP server failed: %v", serverErr)
	}

	shutdownContext, shutdownCancel :=
		context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
	defer shutdownCancel()

	if err := httpServer.Shutdown(
		shutdownContext,
	); err != nil {
		log.Printf(
			"HTTP shutdown failed: %v",
			err,
		)
	}

	<-workDone

	if err := eventBus.Close(
		shutdownContext,
	); err != nil {
		log.Printf(
			"event bus shutdown failed: %v",
			err,
		)
	}

	stoppedEvent := map[string]any{
		"source": "api-demo",
		"event":  "api_stopped",
		"status": s.Status(),
	}

	stoppedData, _ := json.Marshal(stoppedEvent)
	fmt.Println(string(stoppedData))
}
