package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegisrt/internal/llm"
	"aegisrt/internal/research"
	"aegisrt/internal/telemetry"
)

func runResearchEval(arguments []string) error {
	flags := flag.NewFlagSet("agent research-eval", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	suiteName := flags.String("suite", "fixture", "evaluation suite: fixture or real-small")
	corpusPath := flags.String("corpus", filepath.Join("eval", "research", "tasks.json"), "offline research eval corpus")
	suitePath := flags.String("suite-file", filepath.Join("eval", "research", "real-small.json"), "real evaluation suite configuration")
	goldPath := flags.String("gold", filepath.Join("eval", "research", "gold-annotations.json"), "human gold annotation file")
	outputDirectory := flags.String("output", filepath.Join("var", "research-eval"), "eval report directory")
	realMode := flags.Bool("real", false, "alias for --suite real-small; with --task, run one custom real goal")
	task := flags.String("task", "", "optional single real evaluation research goal")
	provider := flags.String("provider", "arxiv", "real literature provider: arxiv, multi, or crossref")
	maxPapers := flags.Int("max-papers", 0, "override suite paper limit (3-5; zero uses suite value)")
	maxLLMCalls := flags.Int("max-llm-calls", 16, "hard LLM call budget per real goal")
	maxReplans := flags.Int("max-replans", 3, "maximum replans per real goal")
	maxContextBytes := flags.Int("max-context-bytes", research.MaximumAnalysisContextBytes, "maximum selected section bytes per paper")
	noCache := flags.Bool("no-cache", false, "disable literature query cache")
	python := flags.String("python", defaultResearchPython(), "Python interpreter containing pypdf")
	timeout := flags.Duration("timeout", 10*time.Minute, "evaluation timeout")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *realMode {
		*suiteName = "real-small"
	}
	if *suiteName == "real-small" {
		return runRealResearchEval(*suitePath, *goldPath, *outputDirectory, strings.TrimSpace(*task), *python, strings.ToLower(strings.TrimSpace(*provider)), *maxPapers, *maxLLMCalls, *maxReplans, *maxContextBytes, *noCache, *timeout)
	}
	if *suiteName != "fixture" {
		return fmt.Errorf("research eval suite must be fixture or real-small")
	}

	corpus, err := research.LoadEvalCorpus(*corpusPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := research.RunOfflineEval(ctx, corpus)
	if err != nil {
		return err
	}
	jsonPath, markdownPath, err := research.WriteEvalReports(report, *outputDirectory)
	if err != nil {
		return err
	}
	if err := publishEvalCompleted(*outputDirectory, report); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(report.Metrics, "", "  ")
	fmt.Println("[OFFLINE RESEARCH EVAL]")
	fmt.Printf("Mode: %s\nCorpus: %s\n", report.Mode, report.Name)
	fmt.Println(string(encoded))
	for _, taskResult := range report.Tasks {
		switch taskResult.ID {
		case "unsupported-quantitative-claim":
			fmt.Printf("[EVIDENCE REJECTION] passed=%t unsupported=%d source_verified=%d\n",
				taskResult.Passed, taskResult.UnsupportedFindings, taskResult.VerifiedFindings)
		case "citation-hallucination":
			fmt.Printf("[CITATION HALLUCINATION REJECTION] passed=%t rejected=%d closure_checks=%d\n",
				taskResult.Passed, taskResult.HallucinatedReferences, taskResult.CitationChecks)
		}
	}
	fmt.Printf("JSON: %s\nMarkdown: %s\n", jsonPath, markdownPath)
	if report.Metrics.FailedTasks > 0 {
		return fmt.Errorf("offline research eval failed %d of %d tasks", report.Metrics.FailedTasks, report.Metrics.TotalTasks)
	}
	return nil
}

func runRealResearchEval(suitePath, goldPath, outputDirectory, customTask, python, provider string, maxPapers, maxLLMCalls, maxReplans, maxContextBytes int, noCache bool, timeout time.Duration) error {
	suite, err := research.LoadRealEvalSuite(suitePath)
	if err != nil {
		return err
	}
	annotations, err := research.LoadGoldAnnotations(goldPath, suite)
	if err != nil {
		return err
	}
	if customTask != "" {
		paperLimit := maxPapers
		if paperLimit == 0 {
			paperLimit = 3
		}
		suite.Name = "custom-real"
		suite.Goals = []research.RealEvalGoal{{ID: "custom-real-goal", Category: "custom", Goal: customTask, MaxPapers: paperLimit}}
		annotations = research.GoldAnnotationSet{
			Version: 1, Suite: suite.Name,
			Goals: []research.GoldGoalAnnotation{{GoalID: "custom-real-goal", Status: "PENDING", Papers: []research.GoldPaperAnnotation{}}},
		}
	}
	if maxPapers != 0 && (maxPapers < 3 || maxPapers > 5) {
		return fmt.Errorf("max-papers override must be between 3 and 5")
	}
	if timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if provider != "arxiv" && provider != "multi" && provider != "crossref" {
		return fmt.Errorf("provider must be arxiv, multi, or crossref")
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	reviewPath := filepath.Join(outputDirectory, "real-eval-review.md")
	if err := research.WriteHumanReviewTemplate(suite, annotations, reviewPath); err != nil {
		return err
	}
	metrics := research.EvaluateReviewedEvidence(annotations)
	config, err := llm.LoadConfig()
	if err != nil {
		return err
	}
	config.Timeout = minDuration(timeout, 60*time.Second)
	if err := llm.ValidateConfig(config, llm.ConfigRequirements{RequireExplicitEndpoint: true, RequireCredential: true}); err != nil {
		fmt.Printf("[REAL LLM RESEARCH EVAL] SKIPPED: %v\n", err)
		fmt.Println("Human review template:", reviewPath)
		printLLMConfigurationTemplate()
		return nil
	}
	parserStatus := research.DetectPythonPDFParser(context.Background(), python, 10*time.Second)
	if !parserStatus.Available {
		return fmt.Errorf("real eval requires the reproducible pypdf parser: %s", parserStatus.Reason)
	}
	connectivity, err := llm.CheckOpenAICompatibleConnectivity(context.Background(), config)
	if err != nil {
		return fmt.Errorf("real eval LLM connectivity: %w", err)
	}
	fmt.Printf("[REAL LLM RESEARCH EVAL]\nSuite: %s\nGoals: %d\nModel: %s\nEndpoint: %s\nParser: %s %s\n",
		suite.Name, len(suite.Goals), connectivity.Model, connectivity.Endpoint, parserStatus.Parser, parserStatus.Version)
	failed := 0
	for _, goal := range suite.Goals {
		paperLimit := goal.MaxPapers
		if maxPapers != 0 {
			paperLimit = maxPapers
		}
		root := filepath.Join(outputDirectory, "runs", goal.ID)
		arguments := []string{
			"--task", goal.Goal, "--root", root, "--analysis-mode", "llm",
			"--provider", provider,
			"--paper-parser", "python", "--python", python, "--loop-timeout", timeout.String(),
			"--max-papers", fmt.Sprint(paperLimit), "--max-llm-calls", fmt.Sprint(maxLLMCalls),
			"--max-replans", fmt.Sprint(maxReplans), "--max-context-bytes", fmt.Sprint(maxContextBytes),
		}
		if noCache {
			arguments = append(arguments, "--no-cache")
		}
		fmt.Printf("\n[REAL EVAL GOAL] %s (%s)\n", goal.ID, goal.Category)
		if err := runResearchAgent(arguments); err != nil {
			failed++
			fmt.Printf("[REAL EVAL GOAL FAILED] %s: %v\n", goal.ID, err)
		}
	}
	encoded, _ := json.MarshalIndent(metrics, "", "  ")
	fmt.Println("[HUMAN EVIDENCE REVIEW]")
	fmt.Println(string(encoded))
	fmt.Println("Template:", reviewPath)
	if failed > 0 {
		return fmt.Errorf("real research eval failed %d of %d goals", failed, len(suite.Goals))
	}
	return nil
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}

func publishEvalCompleted(outputDirectory string, report research.EvalReport) error {
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	sink, err := telemetry.OpenJSONLSink(filepath.Join(outputDirectory, "eval-events.jsonl"))
	if err != nil {
		return err
	}
	bus, err := telemetry.NewBus(32, sink)
	if err != nil {
		_ = sink.Close()
		return err
	}
	event, err := telemetry.NewEvent(telemetry.KindEvalCompleted, "research-eval", "", "COMPLETED", map[string]any{
		"mode": report.Mode, "tasks": report.Metrics.TotalTasks,
		"passed": report.Metrics.PassedTasks, "duration_ms": report.Metrics.DurationMillis,
	})
	if err == nil {
		err = bus.Publish(context.Background(), event)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	closeErr := bus.Close(closeCtx)
	if err != nil {
		return err
	}
	return closeErr
}
