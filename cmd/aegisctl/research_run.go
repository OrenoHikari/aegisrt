package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
	"aegisrt/internal/llm"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/outputtxn"
	"aegisrt/internal/planner"
	"aegisrt/internal/pressure"
	"aegisrt/internal/research"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

func runResearchAgent(arguments []string) error {
	flags := flag.NewFlagSet("agent research", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	task := flags.String("task", "", "natural-language research goal")
	mock := flags.Bool("mock", false, "use deterministic offline research provider and LLM")
	mockScenario := flags.String("mock-scenario", research.MockScenarioNormal, "normal, search-replan, unavailable, or evidence-rejection")
	provider := flags.String("provider", "multi", "real literature provider: multi, arxiv, or crossref")
	root := flags.String("root", "var/research-agent", "research run data root")
	reportPath := flags.String("report", "", "final Markdown report path (default: ROOT/report.md)")
	workers := flags.Int("workers", 4, "maximum concurrent CAPSuleRT research tasks")
	maxReplans := flags.Int("max-replans", 3, "maximum revised research plans")
	maxSearchRounds := flags.Int("max-search-rounds", 3, "maximum distinct literature queries")
	maxPapers := flags.Int("max-papers", 5, "maximum fetched/analyzed papers in a plan")
	maxPDFMB := flags.Int64("max-pdf-mb", research.DefaultPaperDownloadLimitBytes/(1024*1024), "maximum downloaded and parsed bytes per paper, in MiB")
	maxLLMCalls := flags.Int("max-llm-calls", 16, "hard maximum real LLM calls including planning and paper analysis")
	maxAnalysisCalls := flags.Int("max-analysis-calls-per-paper", 1, "hard maximum LLM analysis/support calls per paper")
	maxContextBytes := flags.Int("max-context-bytes", 60*1024, "maximum selected section bytes per paper analysis")
	taskTimeout := flags.Duration("task-timeout", 45*time.Second, "timeout for each research capability")
	loopTimeout := flags.Duration("loop-timeout", 5*time.Minute, "hard timeout for the complete research loop")
	llmTimeout := flags.Duration("llm-timeout", 60*time.Second, "LLM HTTP timeout")
	enableCgroup := flags.Bool("enable-cgroup", false, "enable delegated cgroup v2 isolation")
	parserMode := flags.String("paper-parser", "auto", "paper parser: auto, python, or basic")
	pythonExecutable := flags.String("python", defaultResearchPython(), "Python interpreter for the optional pypdf parser")
	analysisMode := flags.String("analysis-mode", "auto", "paper analysis: auto, basic, or llm")
	claimSupportMode := flags.String("claim-support", "deterministic", "claim support: deterministic or llm")
	cacheTTL := flags.Duration("cache-ttl", 24*time.Hour, "literature query cache TTL")
	noCache := flags.Bool("no-cache", false, "disable the literature query cache")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	goal := strings.TrimSpace(*task)
	if goal == "" && flags.NArg() > 0 {
		goal = strings.TrimSpace(strings.Join(flags.Args(), " "))
	}
	if goal == "" {
		return fmt.Errorf("agent research requires -task TEXT")
	}
	if *workers <= 0 || *maxReplans <= 0 || *maxSearchRounds <= 0 || *maxPapers <= 0 {
		return fmt.Errorf("workers, max-replans, max-search-rounds, and max-papers must be greater than zero")
	}
	if *maxPapers > research.MaximumSearchResults {
		return fmt.Errorf("max-papers must be at most %d", research.MaximumSearchResults)
	}
	maxPDFBytes := *maxPDFMB * 1024 * 1024
	if *maxPDFMB <= 0 || maxPDFBytes/1024/1024 != *maxPDFMB {
		return fmt.Errorf("max-pdf-mb must be greater than zero")
	}
	if err := research.ValidatePaperDownloadLimit(maxPDFBytes); err != nil {
		return fmt.Errorf("max-pdf-mb: %w", err)
	}
	budget := research.ResearchBudget{
		MaxPapers: *maxPapers, MaxLLMCalls: *maxLLMCalls,
		MaxAnalysisCallsPerPaper: *maxAnalysisCalls, MaxReplans: *maxReplans,
		MaxRuntime: *loopTimeout, MaxContextBytes: *maxContextBytes, MaxPDFBytes: maxPDFBytes,
	}
	if err := budget.Validate(); err != nil {
		return err
	}
	if *cacheTTL <= 0 {
		return fmt.Errorf("cache-ttl must be greater than zero")
	}
	resolvedParserMode := strings.ToLower(strings.TrimSpace(*parserMode))
	if resolvedParserMode != "auto" && resolvedParserMode != "basic" && resolvedParserMode != "python" {
		return fmt.Errorf("paper-parser must be auto, basic, or python")
	}
	if strings.TrimSpace(*reportPath) == "" {
		*reportPath = filepath.Join(*root, "report.md")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	providerName := strings.ToLower(strings.TrimSpace(*provider))
	if *mock {
		providerName = "mock"
	} else if providerName != "multi" && providerName != "arxiv" && providerName != "crossref" {
		return fmt.Errorf("provider must be multi, arxiv, or crossref")
	}
	resolvedAnalysisMode := strings.ToLower(strings.TrimSpace(*analysisMode))
	if resolvedAnalysisMode == "auto" {
		if *mock {
			resolvedAnalysisMode = "basic"
		} else {
			resolvedAnalysisMode = "llm"
		}
	}
	if resolvedAnalysisMode != "basic" && resolvedAnalysisMode != "llm" {
		return fmt.Errorf("analysis-mode must be auto, basic, or llm")
	}
	if *mock && resolvedAnalysisMode == "llm" {
		return fmt.Errorf("mock research mode is offline and requires basic analysis")
	}
	resolvedClaimSupportMode := strings.ToLower(strings.TrimSpace(*claimSupportMode))
	if resolvedClaimSupportMode != "deterministic" && resolvedClaimSupportMode != "llm" {
		return fmt.Errorf("claim-support must be deterministic or llm")
	}
	if resolvedClaimSupportMode == "llm" && *maxAnalysisCalls < 2 {
		return fmt.Errorf("claim-support llm requires max-analysis-calls-per-paper of at least 2")
	}
	pythonParserScript, err := filepath.Abs(filepath.Join("worker", "python", "paper_parser.py"))
	if err != nil {
		return err
	}
	cacheDirectory, err := filepath.Abs(filepath.Join(*root, "search-cache"))
	if err != nil {
		return err
	}
	var realLLMConfig llm.Config
	if !*mock {
		realLLMConfig, err = llm.LoadConfig()
		if err != nil {
			return err
		}
		realLLMConfig.Timeout = *llmTimeout
		if err := llm.ValidateConfig(realLLMConfig, llm.ConfigRequirements{RequireExplicitEndpoint: true, RequireCredential: true}); err != nil {
			return fmt.Errorf("configure real research LLM: %w", err)
		}
	}
	registrations, err := research.Registrations(research.RegistrationOptions{
		Executable: executable, Provider: providerName, MockScenario: *mockScenario, TaskTimeout: *taskTimeout,
		CacheDirectory: cacheDirectory, CacheTTL: *cacheTTL, DisableCache: *noCache,
		ParserMode: resolvedParserMode, PythonExecutable: *pythonExecutable, PythonParserScript: pythonParserScript,
		AnalysisMode: resolvedAnalysisMode, ClaimSupportMode: resolvedClaimSupportMode,
		MaxAnalysisCallsPerPaper: *maxAnalysisCalls, MaxContextBytes: *maxContextBytes,
		MaxPDFBytes:   maxPDFBytes,
		LLMConfigFile: realLLMConfig.SourceFile,
	})
	if err != nil {
		return err
	}
	registry, err := orchestrator.NewRegistry(registrations)
	if err != nil {
		return err
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancelRun := context.WithTimeout(signalCtx, *loopTimeout)
	defer cancelRun()
	started := time.Now()

	var modelClient llm.Client
	var usageTracker *llm.BudgetClient
	if *mock {
		modelClient, err = research.NewMockLLMClient(*mockScenario, goal)
		if err != nil {
			return err
		}
	} else {
		modelClient, err = llm.NewOpenAICompatibleClient(realLLMConfig)
		if err != nil {
			return fmt.Errorf("configure research LLM (set CAPSULE_LLM_MODEL and compatible endpoint credentials, or use -mock): %w", err)
		}
		parentCallBudget := *maxLLMCalls - (*maxPapers * *maxAnalysisCalls)
		usageTracker = &llm.BudgetClient{Client: modelClient, MaxCalls: parentCallBudget}
		modelClient = usageTracker
		checkCtx, cancelCheck := context.WithTimeout(ctx, *llmTimeout)
		connectivity, checkErr := llm.CheckConnectivity(checkCtx, modelClient, llm.SanitizedEndpoint(realLLMConfig), realLLMConfig.Model)
		cancelCheck()
		if checkErr != nil {
			return fmt.Errorf("research LLM connectivity check: %w", checkErr)
		}
		fmt.Printf("[LLM CONNECTIVITY] PASS endpoint=%s model=%s structured=%t latency_ms=%d\n\n",
			connectivity.Endpoint, connectivity.Model, connectivity.StructuredResponse, connectivity.LatencyMillis)
	}
	taskPlanner, err := planner.New(modelClient, registry.Capabilities())
	if err != nil {
		return err
	}
	baseController, err := orchestrator.NewLLMController(modelClient, registry.Capabilities())
	if err != nil {
		return err
	}
	controller, err := research.NewGuardedControllerWithLimits(baseController, research.ReplanLimits{
		MaxPapers: *maxPapers, MaxSearchRounds: *maxSearchRounds,
	})
	if err != nil {
		return err
	}
	queryPolicy, err := research.NewResearchPlanPolicy(*maxSearchRounds, *maxPapers)
	if err != nil {
		return err
	}

	store, err := contextfs.Open(filepath.Join(*root, "contextfs"))
	if err != nil {
		return err
	}
	workspaceManager, err := contextfs.NewWorkspaceManager(store, filepath.Join(*root, "workspaces"))
	if err != nil {
		return err
	}
	if err := workspaceManager.CleanupStaging(); err != nil {
		return err
	}
	outputManager, err := outputtxn.Open(filepath.Join(*root, "outputs"), outputtxn.DefaultLimits())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*root, 0o755); err != nil {
		return err
	}
	jsonlSink, err := telemetry.OpenJSONLSink(filepath.Join(*root, "runtime-events.jsonl"))
	if err != nil {
		return err
	}
	eventBus, err := telemetry.NewBus(2048, jsonlSink)
	if err != nil {
		_ = jsonlSink.Close()
		return err
	}
	busClosed := false
	defer func() {
		if busClosed {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = eventBus.Close(closeCtx)
	}()
	agentLog, err := os.OpenFile(filepath.Join(*root, "agent-events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer agentLog.Close()

	var resourceManager *resource.Manager
	if *enableCgroup {
		resourceManager, err = resource.NewManagerFromCurrent()
		if err != nil {
			return err
		}
		if err := resourceManager.Initialize(); err != nil {
			return err
		}
	}
	baseRunner := &agentRuntime.Runner{Log: agentLog, Resources: resourceManager}
	outputExecutor, err := agentRuntime.NewTransactionalOutputExecutor(baseRunner, outputManager, agentRuntime.OutputDiscardOnFailure)
	if err != nil {
		return err
	}
	executor, err := agentRuntime.NewWorkspaceExecutor(outputExecutor, workspaceManager, agentRuntime.WorkspaceCleanupAlways)
	if err != nil {
		return err
	}
	runtimeScheduler, err := scheduler.NewWithOptions(executor, scheduler.Options{
		WorkerCount: *workers, QueueSize: 128, Policy: scheduler.NewCAPSPolicy(), PressureSource: pressure.NewReader(),
		ContextRegistry: contextstore.NewRegistry(), ContextResolver: contextstore.PassthroughResolver{},
		OutputVerifier: outputManager, EventPublisher: eventBus,
	})
	if err != nil {
		return err
	}
	agentOrchestrator, err := orchestrator.New(runtimeScheduler, registry, eventBus)
	if err != nil {
		return err
	}
	loop, err := orchestrator.NewAgentLoop(taskPlanner, controller, agentOrchestrator, registry, eventBus, orchestrator.LoopOptions{
		MaxReplans: *maxReplans, Timeout: *loopTimeout, PlanValidator: queryPolicy,
	})
	if err != nil {
		return err
	}

	fmt.Println("[RESEARCH GOAL]")
	fmt.Println(goal)
	fmt.Println()
	result, runErr := loop.Run(ctx, goal)
	duration := time.Since(started)
	metrics := research.Evaluate(result, runErr, duration)
	if usageTracker != nil {
		stats := usageTracker.Stats()
		if stats.UsageKnown {
			input, output := stats.InputTokens, stats.OutputTokens
			if metrics.InputTokens != nil {
				input += *metrics.InputTokens
			}
			if metrics.OutputTokens != nil {
				output += *metrics.OutputTokens
			}
			metrics.InputTokens = &input
			metrics.OutputTokens = &output
		}
	}
	exportedReport := ""
	if runErr == nil {
		exportedReport, err = research.ExportReport(result, *reportPath)
		if err != nil {
			runErr = err
		}
	}
	runMode := "real"
	if *mock {
		runMode = "mock"
	}
	summary, failures := research.BuildRunSummary(result, runErr, duration, budget, runMode)
	if usageTracker != nil {
		stats := usageTracker.Stats()
		summary.LLM.Calls += stats.Calls
		summary.LLM.Failures += stats.Failures
		if stats.UsageKnown {
			input, output := stats.InputTokens, stats.OutputTokens
			if summary.LLM.InputTokens != nil {
				input += *summary.LLM.InputTokens
			}
			if summary.LLM.OutputTokens != nil {
				output += *summary.LLM.OutputTokens
			}
			summary.LLM.InputTokens = &input
			summary.LLM.OutputTokens = &output
		}
	}
	summaryPath, failurePath, artifactErr := research.WriteRunArtifacts(*root, summary, failures)
	if artifactErr != nil && runErr == nil {
		runErr = fmt.Errorf("write research run artifacts: %w", artifactErr)
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := eventBus.Close(closeCtx)
	cancelClose()
	busClosed = true
	if closeErr != nil {
		return closeErr
	}
	printResearchResult(result, metrics, summary, exportedReport, summaryPath, failurePath, runErr)
	return runErr
}

func defaultResearchPython() string {
	if configured := strings.TrimSpace(os.Getenv("CAPSULE_RESEARCH_PYTHON")); configured != "" {
		return configured
	}
	candidate := filepath.Join(".venv-research", "bin", "python")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "python3"
}

func printResearchResult(result orchestrator.LoopResult, metrics research.RunMetrics, summary research.RunSummary, reportPath, summaryPath, failurePath string, runErr error) {
	for _, iteration := range result.Iterations {
		fmt.Printf("[PLAN v%d]\n", iteration.Version)
		for _, task := range iteration.Plan.Tasks {
			fmt.Printf("  %s  capability=%s", task.ID, task.Capability)
			if len(task.DependsOn) > 0 {
				fmt.Printf("  depends_on=%s", strings.Join(task.DependsOn, ","))
			}
			fmt.Println()
		}
		fmt.Println()
		for _, observation := range iteration.Observations {
			switch observation.Capability {
			case "literature.search":
				fmt.Println("[SEARCH]")
				fmt.Printf("Query: %v\nResults: %v\n", observation.Output["query"], observation.Output["total_results"])
				if papers, ok := observation.Output["papers"].([]any); ok {
					for _, raw := range papers {
						if paper, ok := raw.(map[string]any); ok {
							fmt.Printf("  - %v (%v) [%v]\n", paper["title"], paper["year"], paper["id"])
						}
					}
				}
			case "paper.fetch":
				fmt.Println("[PAPER]")
				fmt.Printf("Fetch %s available=%v bytes=%v reason=%v\n", observation.TaskID, observation.Output["available"], observation.Output["bytes"], observation.Output["reason"])
			case "paper.parse":
				fmt.Println("[PAPER]")
				if paper, ok := observation.Output["paper"].(map[string]any); ok {
					fmt.Printf("Title: %v\n", paper["title"])
				}
				if diagnostics, ok := observation.Output["diagnostics"].(map[string]any); ok {
					fmt.Printf("Parser: %v pages=%v sections=%v chars=%v fallback=%v truncated=%v warnings=%v\n",
						diagnostics["selected"], diagnostics["page_count"], diagnostics["detected_sections"],
						diagnostics["extracted_characters"], diagnostics["fallback_used"], diagnostics["truncated"], diagnostics["warning_count"])
				}
			case "paper.analyze":
				candidates := outputArrayLength(observation.Output["candidate_findings"])
				supported, rejected := countFindingStatuses(observation.Output["findings"])
				fmt.Println("[ANALYSIS]")
				fmt.Printf("%s method=%v candidates=%d llm_calls=%v\n", observation.TaskID, observation.Output["method"], candidates, observation.Output["llm_calls"])
				fmt.Println("[EVIDENCE]")
				fmt.Printf("source_verified=%d supported=%d rejected=%d\n", outputArrayLength(observation.Output["evidence"]), supported, rejected)
			case "research.synthesize":
				fmt.Println("[SYNTHESIS]")
				fmt.Printf("facts=%d inferences=%d references=%d directions=%d\n",
					outputArrayLength(observation.Output["facts"]), outputArrayLength(observation.Output["inferences"]),
					outputArrayLength(observation.Output["references"]), outputArrayLength(observation.Output["research_directions"]))
			case "experiment.design":
				fmt.Println("[EXPERIMENT DESIGN]")
				fmt.Printf("baselines=%d datasets=%d metrics=%d ablations=%d\n",
					outputArrayLength(observation.Output["baseline_suggestions"]), outputArrayLength(observation.Output["datasets"]),
					outputArrayLength(observation.Output["metrics"]), outputArrayLength(observation.Output["ablation_plan"]))
			case "research.report":
				fmt.Println("[REPORT]")
				fmt.Printf("references=%v citation_closure=%v hallucinated_references=%v\n",
					observation.Output["references"], observation.Output["citation_closed"], observation.Output["hallucinated_references"])
			}
		}
		fmt.Println("[DECISION]")
		fmt.Println(iteration.Decision.Type)
		fmt.Println("Reason:", iteration.Decision.Reason)
		fmt.Println()
	}
	if runErr != nil {
		fmt.Println("[RESEARCH FAILED]")
		fmt.Println(runErr)
	} else {
		fmt.Println("[RESEARCH COMPLETE]")
		fmt.Println("Report:", reportPath)
	}
	encoded, _ := json.MarshalIndent(metrics, "", "  ")
	fmt.Println("Metrics:")
	fmt.Println(string(encoded))
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println("[RUN SUMMARY]")
	fmt.Println(string(summaryJSON))
	if summaryPath != "" {
		fmt.Println("Run summary:", summaryPath)
	}
	if failurePath != "" {
		fmt.Println("Failure cases:", failurePath)
	}
	if runErr == nil {
		var finalReportTask string
		for _, iteration := range result.Iterations {
			for _, task := range iteration.Plan.Tasks {
				if task.Capability == "research.report" {
					finalReportTask = task.ID
				}
			}
		}
		if finalReportTask != "" {
			fmt.Printf("Final task: %s\n", finalReportTask)
			return
		}
		ids := make([]string, 0, len(result.FinalOutputs))
		for id := range result.FinalOutputs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) > 0 {
			fmt.Printf("Final task: %s\n", ids[len(ids)-1])
		}
	}
}

func outputArrayLength(value any) int {
	items, _ := value.([]any)
	return len(items)
}

func countFindingStatuses(value any) (int, int) {
	items, _ := value.([]any)
	supported, rejected := 0, 0
	for _, item := range items {
		finding, _ := item.(map[string]any)
		if finding["status"] == string(research.FindingSupported) {
			supported++
		} else if finding["status"] == string(research.FindingUnsupported) {
			rejected++
		}
	}
	return supported, rejected
}
