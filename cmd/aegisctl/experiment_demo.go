package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"aegisrt/internal/experiment"
)

func runExperimentDemo(arguments []string) error {
	flags := flag.NewFlagSet("experiment demo", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	task := flags.String("task", experiment.DefaultGoal, "natural-language experiment goal")
	root := flags.String("root", "var/experiment-demo", "experiment Runtime data root")
	workspaceRoot := flags.String("workspace-root", ".", "allowed dataset workspace root")
	dataset := flags.String("dataset", "examples/experiment/classification.csv", "root-scoped local CSV dataset")
	experimentDirectory := flags.String("experiment-dir", experiment.DefaultExperimentDirectory, "workspace-relative directory containing capsule-experiment.json (empty uses the legacy fixed fixture)")
	workers := flags.Int("workers", 3, "maximum concurrent CAPSuleRT experiment workers")
	maxReplans := flags.Int("max-replans", 3, "maximum revised plans before safe abort")
	taskTimeout := flags.Duration("task-timeout", 15*time.Second, "timeout for each experiment task")
	loopTimeout := flags.Duration("loop-timeout", time.Minute, "hard timeout for the complete experiment loop")
	workScale := flags.Int("work-scale", experiment.DefaultWorkScale, fmt.Sprintf("deterministic CPU work multiplier (1-%d)", experiment.MaximumWorkScale))
	enableCgroup := flags.Bool("enable-cgroup", false, "enable delegated cgroup v2 isolation")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	goal := strings.TrimSpace(*task)
	if flags.NArg() > 0 {
		goal = strings.TrimSpace(strings.Join(flags.Args(), " "))
	}
	if goal == "" {
		return fmt.Errorf("experiment demo requires a goal")
	}
	if *workers <= 0 || *maxReplans <= 0 {
		return fmt.Errorf("workers and max-replans must be greater than zero")
	}
	if *workScale < 1 || *workScale > experiment.MaximumWorkScale {
		return fmt.Errorf("work-scale must be between 1 and %d", experiment.MaximumWorkScale)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Println("[GOAL]")
	fmt.Println(goal)
	fmt.Println()
	fmt.Println("[MODE]")
	fmt.Println("Cognitive plane: deterministic offline LLM fixture with production validation")
	fmt.Println("Execution plane: LOCAL REAL CAPSuleRT worker processes (CPU-only)")
	if strings.TrimSpace(*experimentDirectory) != "" {
		fmt.Printf("Environment discovery: %s/%s via a registered worker capability\n", strings.TrimSpace(*experimentDirectory), experiment.ManifestFilename)
	}
	fmt.Printf("Deterministic CPU work scale: %dx\n", *workScale)
	fmt.Println()
	result, runErr := experiment.RunDemo(ctx, goal, experiment.DemoOptions{
		Root: *root, WorkspaceRoot: *workspaceRoot, DatasetPath: *dataset,
		ExperimentDirectory: strings.TrimSpace(*experimentDirectory),
		Workers:             *workers, MaxReplans: *maxReplans, TaskTimeout: *taskTimeout,
		LoopTimeout: *loopTimeout, EnableCgroup: *enableCgroup, WorkScale: *workScale,
	})
	printExperimentResult(result, runErr)
	return runErr
}

func printExperimentResult(result experiment.DemoResult, runErr error) {
	for _, iteration := range result.Loop.Iterations {
		fmt.Printf("[PLAN v%d]\n", iteration.Version)
		for _, task := range iteration.Plan.Tasks {
			arguments, _ := json.Marshal(task.Arguments)
			fmt.Printf("  %s  capability=%s  arguments=%s", task.ID, task.Capability, arguments)
			if len(task.DependsOn) > 0 {
				fmt.Printf("  depends_on=%s", strings.Join(task.DependsOn, ","))
			}
			fmt.Println()
		}
		fmt.Println()
		fmt.Println("[SCHEDULER EXECUTION]")
		reused := make(map[string]bool)
		for _, id := range iteration.Execution.ReusedTaskIDs {
			reused[id] = true
		}
		for _, record := range iteration.Execution.Records {
			extra := ""
			if reused[record.ID] {
				extra = " REUSED_VERIFIED_OUTPUT"
			}
			fmt.Printf("  %-24s %-9s worker=%d%s", record.ID, record.Phase, record.WorkerID, extra)
			if record.ExitCode != nil {
				fmt.Printf(" exit=%d", *record.ExitCode)
			}
			fmt.Println()
		}
		fmt.Println()
		fmt.Println("[OBSERVATION]")
		for _, observation := range iteration.Observations {
			if observation.Capability == experiment.CapabilityRun || observation.Capability == experiment.CapabilityManifestInspect {
				encoded, _ := json.Marshal(observation.Output)
				fmt.Printf("  %s %s %s\n", observation.TaskID, observation.State, encoded)
			}
		}
		fmt.Println()
		fmt.Println("[DECISION]")
		fmt.Println(iteration.Decision.Type)
		fmt.Println("Reason:", iteration.Decision.Reason)
		fmt.Println()
	}
	fmt.Println("[RESULT]")
	if runErr != nil {
		fmt.Println("Experiment loop stopped safely:", runErr)
		return
	}
	for _, item := range result.Summary.Experiments {
		fmt.Printf("  %-20s accuracy=%.2f runtime=%dms memory=%.1fMiB attempt=%d\n",
			item.DisplayName, item.Accuracy, item.RuntimeMS,
			float64(item.MemoryPeakBytes)/(1024*1024), item.Attempt)
	}
	fmt.Println("Best method:", result.Summary.BestName)
	fmt.Println("Replans:", result.Summary.Replans)
	fmt.Println("Report:", result.Summary.ReportPath)
	fmt.Println("Telemetry:", result.Summary.TelemetryPath)
	if result.Loop.FinalAnswer != "" {
		fmt.Println(result.Loop.FinalAnswer)
	}
	fmt.Println()
}
