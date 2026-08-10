package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aegisrt/internal/dashboard"
)

func runDashboard(arguments []string) error {
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	listenAddress := flags.String("listen", "127.0.0.1:8080", "local Dashboard listen address")
	root := flags.String("root", "var/dashboard", "Dashboard run history root")
	mock := flags.Bool("mock", false, "default new runs to deterministic offline mode")
	preset := flags.String("preset-file", "examples/dashboard/presets.json", "competition demo preset JSON file")
	python := flags.String("python", defaultResearchPython(), "Python interpreter used by real PDF parsing")
	maxPapers := flags.Int("max-papers", 3, "maximum papers per Dashboard research plan")
	maxPDFMB := flags.Int64("max-pdf-mb", 32, "default maximum downloaded and parsed MiB per paper")
	maxLLMCalls := flags.Int("max-llm-calls", 16, "maximum real LLM calls per run")
	maxReplans := flags.Int("max-replans", 3, "maximum plan revisions per run")
	experimentWorkScale := flags.Int("experiment-work-scale", dashboard.DefaultExperimentWorkScale, "CPU work multiplier for observable local experiment execution")
	loopTimeout := flags.Duration("loop-timeout", 10*time.Minute, "hard timeout for each research run")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("dashboard does not accept positional arguments")
	}
	mode := "real"
	if *mock {
		mode = "mock"
	}
	controller, err := dashboard.NewController(dashboard.Options{
		Root: *root, Python: *python, PresetFile: *preset, DefaultMode: mode,
		MaxPapers: *maxPapers, MaxLLMCalls: *maxLLMCalls, MaxReplans: *maxReplans,
		ExperimentWorkScale: *experimentWorkScale,
		MaxPDFBytes:         *maxPDFMB * 1024 * 1024,
		LoopTimeout:         *loopTimeout,
	})
	if err != nil {
		return err
	}
	handler, err := dashboard.NewServer(controller)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("listen for Dashboard: %w", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Printf("Dashboard:\nhttp://%s\n", listener.Addr().String())
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalContext.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownContext)
	controllerErr := controller.Close(shutdownContext)
	return errors.Join(shutdownErr, controllerErr)
}
