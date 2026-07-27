package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"aegisrt/internal/agent"
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
		10*time.Second,
		"maximum Agent execution time",
	)

	flag.Parse()

	if _, err := os.Stat(*workerPath); err != nil {
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

	acb := agent.New(
		"agent-hello-001",
		"hello-worker",
		"python3",
		[]string{*workerPath, "--seconds", "3"},
	)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	runner := agentRuntime.Runner{
		Log: output,
	}

	if err := runner.Run(ctx, acb); err != nil {
		fmt.Fprintf(os.Stderr, "runtime error: %v\n", err)
		os.Exit(1)
	}
}
