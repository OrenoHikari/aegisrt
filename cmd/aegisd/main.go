package main

import (
	"context"
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
)

func main() {
	workerPath := flag.String(
		"worker",
		"worker/python/hello_agent.py",
		"path to the Python Agent worker",
	)

	timeout := flag.Duration(
		"timeout",
		15*time.Second,
		"maximum Agent execution time",
	)

	agentSeconds := flag.Uint64(
		"agent-seconds",
		3,
		"number of Agent heartbeat iterations",
	)

	cpuPercent := flag.Uint64(
		"cpu-percent",
		25,
		"Agent CPU quota as a percentage of one CPU",
	)

	memoryMiB := flag.Uint64(
		"memory-mib",
		128,
		"Agent memory limit in MiB",
	)

	pidsMax := flag.Uint64(
		"pids-max",
		16,
		"maximum number of processes in the Agent resource domain",
	)

	disableCgroup := flag.Bool(
		"disable-cgroup",
		false,
		"run without cgroup isolation for local development only",
	)

	flag.Parse()

	absoluteWorkerPath, err := filepath.Abs(*workerPath)
	if err != nil {
		log.Fatalf("resolve worker path: %v", err)
	}

	if _, err := os.Stat(absoluteWorkerPath); err != nil {
		log.Fatalf("worker file is unavailable: %v", err)
	}

	if err := os.MkdirAll("logs", 0o755); err != nil {
		log.Fatalf("create logs directory: %v", err)
	}

	logFile, err := os.OpenFile(
		"logs/events.jsonl",
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		log.Fatalf("open event log: %v", err)
	}
	defer logFile.Close()

	output := io.MultiWriter(os.Stdout, logFile)

	var resourceManager *resource.Manager

	if !*disableCgroup {
		resourceManager, err = resource.NewManagerFromCurrent()
		if err != nil {
			log.Fatalf("discover delegated cgroup: %v", err)
		}

		if err := resourceManager.Initialize(); err != nil {
			log.Fatalf(
				"initialize cgroup resource manager: %v",
				err,
			)
		}
	}

	acb := agent.New(
		"agent-hello-001",
		"hello-worker",
		"python3",
		[]string{
			absoluteWorkerPath,
			"--seconds",
			strconv.FormatUint(*agentSeconds, 10),
		},
	)

	acb.Resources = resource.Spec{
		CPUQuotaPercent: *cpuPercent,
		MemoryMaxBytes:  *memoryMiB * 1024 * 1024,
		PidsMax:         *pidsMax,
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		*timeout,
	)
	defer cancel()

	runner := agentRuntime.Runner{
		Log:       output,
		Resources: resourceManager,
	}

	if err := runner.Run(ctx, acb); err != nil {
		fmt.Fprintf(os.Stderr, "Runtime error: %v\n", err)
		os.Exit(1)
	}
}
