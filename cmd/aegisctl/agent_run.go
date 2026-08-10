package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"aegisrt/internal/contextfs"
	"aegisrt/internal/contextstore"
	"aegisrt/internal/llm"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/outputtxn"
	"aegisrt/internal/planner"
	"aegisrt/internal/pressure"
	"aegisrt/internal/resource"
	agentRuntime "aegisrt/internal/runtime"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

var mockPathPattern = regexp.MustCompile(`[A-Za-z0-9_./-]+\.[A-Za-z0-9_-]+`)

type mockAgentLLMClient struct {
	mu       sync.Mutex
	scenario string
	goal     string
	input    string
	replans  int
}

func (c *mockAgentLLMClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(request.Messages) == 0 {
		return llm.Response{}, fmt.Errorf("mock LLM request has no messages")
	}
	content := request.Messages[len(request.Messages)-1].Content
	switch {
	case strings.HasPrefix(content, "CAPSULERT_DECISION_REQUEST\n"):
		return c.mockDecision(strings.TrimPrefix(content, "CAPSULERT_DECISION_REQUEST\n"))
	case strings.HasPrefix(content, "CAPSULERT_REPLAN_REQUEST\n"):
		return c.mockReplan(strings.TrimPrefix(content, "CAPSULERT_REPLAN_REQUEST\n"))
	default:
		return c.mockInitialPlan()
	}
}

func runCognitiveAgent(arguments []string) error {
	flags := flag.NewFlagSet("agent run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	task := flags.String("task", "", "natural-language goal to plan and execute")
	mock := flags.Bool("mock", false, "use a deterministic fully offline mock LLM")
	mockScenario := flags.String("mock-scenario", "normal", "offline scenario: normal, replan, or failure")
	input := flags.String("input", "", "input file for the normal offline scenario")
	root := flags.String("root", "var/cognitive-agent", "Agent loop runtime data root")
	workspaceRoot := flags.String("workspace-root", ".", "allowed root for file capabilities")
	workerPath := flags.String("worker", "worker/python/cognitive_agent.py", "path to the trusted capability worker")
	workers := flags.Int("workers", 3, "maximum concurrent CAPSuleRT Agents")
	taskTimeout := flags.Duration("task-timeout", 30*time.Second, "timeout for each Agent task")
	loopTimeout := flags.Duration("loop-timeout", 2*time.Minute, "hard timeout for the complete Agent loop")
	maxReplans := flags.Int("max-replans", 3, "maximum revised plans before safe abort")
	llmTimeout := flags.Duration("llm-timeout", 60*time.Second, "LLM HTTP timeout")
	enableCgroup := flags.Bool("enable-cgroup", false, "enable delegated cgroup v2 isolation")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	goal := strings.TrimSpace(*task)
	if goal == "" && flags.NArg() > 0 {
		goal = strings.TrimSpace(strings.Join(flags.Args(), " "))
	}
	if goal == "" {
		return fmt.Errorf("agent run requires -task TEXT")
	}
	if *workers <= 0 {
		return fmt.Errorf("workers must be greater than zero")
	}
	if *maxReplans <= 0 {
		return fmt.Errorf("max-replans must be greater than zero")
	}

	store, err := contextfs.Open(filepath.Join(*root, "contextfs"))
	if err != nil {
		return fmt.Errorf("open cognitive ContextFS: %w", err)
	}
	registry, err := orchestrator.NewBuiltinRegistry(orchestrator.BuiltinOptions{
		WorkerPath: *workerPath, ContextStore: store, InputRoot: *workspaceRoot, TaskTimeout: *taskTimeout,
	})
	if err != nil {
		return err
	}

	var modelClient llm.Client
	if *mock {
		scenario := strings.ToLower(strings.TrimSpace(*mockScenario))
		if scenario != "normal" && scenario != "replan" && scenario != "failure" {
			return fmt.Errorf("mock-scenario must be normal, replan, or failure")
		}
		mockInput := strings.TrimSpace(*input)
		if scenario == "normal" {
			mockInput, err = findMockInput(goal, mockInput)
			if err != nil {
				return err
			}
		}
		modelClient = &mockAgentLLMClient{scenario: scenario, goal: goal, input: mockInput}
	} else {
		config, configErr := llm.LoadConfig()
		if configErr != nil {
			return configErr
		}
		config.Timeout = *llmTimeout
		modelClient, err = llm.NewOpenAICompatibleClient(config)
		if err != nil {
			return fmt.Errorf("configure LLM (set CAPSULE_LLM_MODEL and endpoint credentials, or use -mock): %w", err)
		}
	}

	taskPlanner, err := planner.New(modelClient, registry.Capabilities())
	if err != nil {
		return fmt.Errorf("create task planner: %w", err)
	}
	controller, err := orchestrator.NewLLMController(modelClient, registry.Capabilities())
	if err != nil {
		return fmt.Errorf("create decision controller: %w", err)
	}
	workspaceManager, err := contextfs.NewWorkspaceManager(store, filepath.Join(*root, "workspaces"))
	if err != nil {
		return fmt.Errorf("create cognitive workspace manager: %w", err)
	}
	if err := workspaceManager.CleanupStaging(); err != nil {
		return fmt.Errorf("cleanup stale cognitive workspaces: %w", err)
	}
	outputManager, err := outputtxn.Open(filepath.Join(*root, "outputs"), outputtxn.DefaultLimits())
	if err != nil {
		return fmt.Errorf("open cognitive output store: %w", err)
	}
	if err := os.MkdirAll(*root, 0o755); err != nil {
		return fmt.Errorf("create cognitive data root: %w", err)
	}

	jsonlSink, err := telemetry.OpenJSONLSink(filepath.Join(*root, "runtime-events.jsonl"))
	if err != nil {
		return fmt.Errorf("open cognitive telemetry log: %w", err)
	}
	eventBus, err := telemetry.NewBus(1024, jsonlSink)
	if err != nil {
		_ = jsonlSink.Close()
		return fmt.Errorf("create cognitive telemetry bus: %w", err)
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
		return fmt.Errorf("open cognitive Agent log: %w", err)
	}
	defer agentLog.Close()
	var resourceManager *resource.Manager
	if *enableCgroup {
		resourceManager, err = resource.NewManagerFromCurrent()
		if err != nil {
			return fmt.Errorf("discover delegated cgroup: %w", err)
		}
		if err := resourceManager.Initialize(); err != nil {
			return fmt.Errorf("initialize cgroup manager: %w", err)
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
		return fmt.Errorf("create CAPSuleRT Scheduler: %w", err)
	}
	agentOrchestrator, err := orchestrator.New(runtimeScheduler, registry, eventBus)
	if err != nil {
		return err
	}
	loop, err := orchestrator.NewAgentLoop(taskPlanner, controller, agentOrchestrator, registry, eventBus, orchestrator.LoopOptions{
		MaxReplans: *maxReplans, Timeout: *loopTimeout,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Println("[GOAL]")
	fmt.Println(goal)
	fmt.Println()
	result, runErr := loop.Run(ctx, goal)

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	closeErr := eventBus.Close(closeCtx)
	cancelClose()
	busClosed = true
	if closeErr != nil {
		return fmt.Errorf("close cognitive telemetry: %w", closeErr)
	}
	printAgentLoopResult(result, runErr)
	return runErr
}

func (c *mockAgentLLMClient) mockInitialPlan() (llm.Response, error) {
	var tasks []planner.Task
	switch c.scenario {
	case "normal":
		tasks = []planner.Task{
			mockTask("inspect-input", "Inspect input", "Read the requested input file.", "file.inspect", map[string]any{"path": c.input}),
			mockDependentTask("analyze-input", "Analyze input", "Analyze the verified file content.", "text.analyze", map[string]any{"question": c.goal}, "inspect-input"),
			mockDependentTask("summarize-input", "Summarize input", "Produce the final answer.", "text.summarize", map[string]any{"question": c.goal}, "analyze-input"),
		}
	case "replan":
		tasks = []planner.Task{
			mockTask("csv-probe", "Check assumed CSV", "Check the initially assumed CSV path.", "filesystem.stat", map[string]any{"path": "examples/workspace/sales.csv"}),
			mockTask("workspace-list", "Inspect workspace", "List real workspace contents.", "filesystem.list", map[string]any{"path": "examples/workspace"}),
		}
	case "failure":
		tasks = []planner.Task{mockTask("missing-probe-1", "Check missing input", "Inspect an unavailable candidate.", "filesystem.stat", map[string]any{"path": "examples/workspace/never-1.csv"})}
	}
	return encodeMockPlan(planner.Plan{Goal: c.goal, Tasks: tasks})
}

func (c *mockAgentLLMClient) mockDecision(payload string) (llm.Response, error) {
	var request orchestrator.DecisionRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return llm.Response{}, fmt.Errorf("decode mock decision request: %w", err)
	}
	allSuccessful := true
	for _, observation := range request.Observations {
		allSuccessful = allSuccessful && observation.Success
	}
	decision := orchestrator.Decision{Type: orchestrator.DecisionGoalCompleted, Reason: "verified execution outputs satisfy the goal"}
	switch c.scenario {
	case "normal":
		if !allSuccessful {
			decision = orchestrator.Decision{Type: orchestrator.DecisionFailed, Reason: "normal scenario execution failed"}
		}
	case "replan":
		dataSucceeded := false
		for _, observation := range request.Observations {
			if observation.Capability == "data.inspect" && observation.Success {
				dataSucceeded = true
			}
		}
		if !dataSucceeded {
			decision = orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "planned sales.csv does not exist; workspace observation found sales.json"}
		}
	case "failure":
		decision = orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "no inspected candidate exists; try another bounded candidate"}
	}
	encoded, _ := json.Marshal(decision)
	return llm.Response{Content: string(encoded)}, nil
}

func (c *mockAgentLLMClient) mockReplan(payload string) (llm.Response, error) {
	var request orchestrator.ReplanRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return llm.Response{}, fmt.Errorf("decode mock replan request: %w", err)
	}
	c.replans++
	tasks := append([]planner.Task(nil), request.CompletedTask...)
	if c.scenario == "replan" {
		tasks = append(tasks,
			mockDependentTask("json-data", "Inspect discovered JSON", "Profile the discovered sales JSON data.", "data.inspect", map[string]any{"path": "examples/workspace/sales.json"}, "csv-probe", "workspace-list"),
			mockDependentTask("json-analysis", "Analyze discovered data", "Analyze the verified JSON profile.", "text.analyze", map[string]any{"question": c.goal}, "json-data"),
			mockDependentTask("json-summary", "Summarize discovered data", "Produce the final evidence-based answer.", "text.summarize", map[string]any{"question": c.goal}, "json-analysis"),
		)
	} else {
		id := fmt.Sprintf("missing-probe-%d", c.replans+1)
		path := fmt.Sprintf("examples/workspace/never-%d.csv", c.replans+1)
		tasks = append(tasks, mockTask(id, "Check another missing input", "Inspect another unavailable candidate.", "filesystem.stat", map[string]any{"path": path}))
	}
	return encodeMockPlan(planner.Plan{Goal: c.goal, Tasks: tasks})
}

func mockTask(id, name, description, capability string, arguments map[string]any) planner.Task {
	return planner.Task{ID: id, Name: name, Description: description, Capability: capability, Arguments: arguments, DependsOn: []string{}}
}

func mockDependentTask(id, name, description, capability string, arguments map[string]any, dependencies ...string) planner.Task {
	task := mockTask(id, name, description, capability, arguments)
	task.DependsOn = dependencies
	return task
}

func encodeMockPlan(plan planner.Plan) (llm.Response, error) {
	data, err := json.Marshal(plan)
	if err != nil {
		return llm.Response{}, fmt.Errorf("encode mock LLM plan: %w", err)
	}
	return llm.Response{Content: string(data)}, nil
}

func findMockInput(task, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("inspect mock input %q: %w", explicit, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("mock input %q is not a regular file", explicit)
		}
		return explicit, nil
	}
	for _, candidate := range mockPathPattern.FindAllString(task, -1) {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("offline normal scenario could not find an input file in the task; pass -input PATH")
}

func printAgentLoopResult(result orchestrator.LoopResult, runErr error) {
	for _, iteration := range result.Iterations {
		fmt.Printf("[PLAN v%d]\n", iteration.Version)
		for _, task := range iteration.Plan.Tasks {
			arguments, _ := json.Marshal(task.Arguments)
			fmt.Printf("  %s  %s  capability=%s  arguments=%s\n", task.ID, task.Name, task.Capability, arguments)
			if len(task.DependsOn) > 0 {
				fmt.Printf("    depends_on=%s\n", strings.Join(task.DependsOn, ","))
			}
		}
		fmt.Println()
		fmt.Println("[EXECUTION]")
		reused := make(map[string]bool, len(iteration.Execution.ReusedTaskIDs))
		for _, id := range iteration.Execution.ReusedTaskIDs {
			reused[id] = true
		}
		for _, record := range iteration.Execution.Records {
			suffix := ""
			if reused[record.ID] {
				suffix = " (reused verified output)"
			}
			fmt.Printf("  %s  %s%s\n", record.ID, record.Phase, suffix)
		}
		fmt.Println()
		fmt.Println("[OBSERVATION]")
		for _, observation := range iteration.Observations {
			output, _ := json.Marshal(observation.Output)
			fmt.Printf("  %s  capability=%s  state=%s  output=%s", observation.TaskID, observation.Capability, observation.State, output)
			if observation.Error != "" {
				fmt.Printf("  error=%s", observation.Error)
			}
			fmt.Println()
		}
		fmt.Println()
		fmt.Println("[DECISION]")
		fmt.Println(iteration.Decision.Type)
		fmt.Println("Reason:", iteration.Decision.Reason)
		fmt.Println()
	}
	fmt.Println("[RESULT]")
	if runErr != nil {
		fmt.Println("Agent loop stopped safely:", runErr)
		fmt.Println()
		return
	}
	if result.FinalAnswer != "" {
		fmt.Println(result.FinalAnswer)
	} else {
		ids := make([]string, 0, len(result.FinalOutputs))
		for id := range result.FinalOutputs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Printf("%s:\n%s\n", id, result.FinalOutputs[id])
		}
	}
	fmt.Println()
}
