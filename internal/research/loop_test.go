package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

type researchLoopExecutor struct {
	root string
	mu   sync.Mutex
	runs map[string]int
}

func (e *researchLoopExecutor) Run(ctx context.Context, acb *agent.ACB) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	e.runs[acb.ID]++
	e.mu.Unlock()
	output := map[string]any{"task_id": acb.ID}
	switch acb.Role {
	case "literature.search":
		var arguments map[string]any
		if err := json.Unmarshal([]byte(acb.Environment["CAPSULE_TASK_ARGUMENTS_JSON"]), &arguments); err != nil {
			return err
		}
		query, _ := arguments["query"].(string)
		count := 3
		if acb.ID == "search-sparse" {
			count = 1
		}
		papers := make([]map[string]any, 0, count)
		for index := 0; index < count; index++ {
			papers = append(papers, map[string]any{"id": fmt.Sprintf("mock.%04d", index+1)})
		}
		output = map[string]any{"query": query, "total_results": count, "papers": papers, "provider": "mock"}
	case "paper.fetch":
		available := acb.ID != "closed-fetch"
		output = map[string]any{"available": available, "reason": ""}
		if !available {
			output["reason"] = ErrFullTextUnavailable.Error()
		}
	case "paper.analyze":
		output = map[string]any{"method": "fixture method", "evidence": []map[string]any{{"id": acb.ID + "-e1"}}}
	case "research.report":
		output = map[string]any{"report_file": "report.md", "references": 2, "unsupported_claims": 0}
	}
	commit := filepath.Join(e.root, acb.ID)
	if err := os.MkdirAll(commit, 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(commit, "result.json"), encoded, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(commit, "result.txt"), []byte("verified research result\n"), 0o600); err != nil {
		return err
	}
	if acb.Role == "research.report" {
		if err := os.WriteFile(filepath.Join(commit, "report.md"), []byte("# Verified report\n"), 0o600); err != nil {
			return err
		}
	}
	acb.OutputCommitted = true
	acb.OutputTransactionID = acb.ID + "-transaction"
	acb.OutputCommitPath = commit
	acb.OutputManifestPath = filepath.Join(commit, ".aegis-commit.json")
	acb.OutputFileCount = 2
	acb.OutputBytes = uint64(len(encoded) + len("verified research result\n"))
	return nil
}

func (e *researchLoopExecutor) count(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[id]
}

func newResearchLoopHarness(t *testing.T, scenario string, maxSearchRounds int) (*orchestrator.AgentLoop, *researchLoopExecutor) {
	t.Helper()
	goal := "compare methods and design an experiment"
	client, err := NewMockLLMClient(scenario, goal)
	if err != nil {
		t.Fatal(err)
	}
	registrations, err := Registrations(RegistrationOptions{Executable: "research-test-worker", Provider: "mock", MockScenario: scenario})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := orchestrator.NewRegistry(registrations)
	if err != nil {
		t.Fatal(err)
	}
	planCreator, err := planner.New(client, registry.Capabilities())
	if err != nil {
		t.Fatal(err)
	}
	baseController, err := orchestrator.NewLLMController(client, registry.Capabilities())
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewGuardedController(baseController)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewQueryPolicy(maxSearchRounds)
	if err != nil {
		t.Fatal(err)
	}
	executor := &researchLoopExecutor{root: t.TempDir(), runs: make(map[string]int)}
	runtimeScheduler, err := scheduler.New(executor, 4, 64)
	if err != nil {
		t.Fatal(err)
	}
	agentOrchestrator, err := orchestrator.New(runtimeScheduler, registry, telemetry.NopPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	loop, err := orchestrator.NewAgentLoop(planCreator, controller, agentOrchestrator, registry, telemetry.NopPublisher{}, orchestrator.LoopOptions{
		MaxReplans: 3, Timeout: 10 * time.Second, PlanValidator: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return loop, executor
}

func TestResearchLoopNormal(t *testing.T) {
	loop, executor := newResearchLoopHarness(t, MockScenarioNormal, 3)
	result, err := loop.Run(context.Background(), "compare methods and design an experiment")
	if err != nil {
		t.Fatal(err)
	}
	if result.Replans != 0 || len(result.Iterations) != 1 || result.FinalDecision.Type != orchestrator.DecisionGoalCompleted {
		t.Fatalf("unexpected normal result: %+v", result)
	}
	if executor.count("search-1") != 1 || executor.count("normal-report") != 1 {
		t.Fatalf("normal tasks did not execute once: %+v", executor.runs)
	}
}

func TestResearchLoopSearchInsufficientThenReplan(t *testing.T) {
	loop, executor := newResearchLoopHarness(t, MockScenarioSearchReplan, 3)
	result, err := loop.Run(context.Background(), "compare methods and design an experiment")
	if err != nil {
		t.Fatal(err)
	}
	if result.Replans != 1 || len(result.Iterations) != 2 || result.Iterations[0].Decision.Type != orchestrator.DecisionReplan {
		t.Fatalf("expected one search replan: %+v", result)
	}
	if executor.count("search-sparse") != 1 || executor.count("search-expanded") != 1 || executor.count("expanded-report") != 1 {
		t.Fatalf("search result was not reused or recovery did not execute: %+v", executor.runs)
	}
}

func TestResearchLoopInaccessiblePaperRecovery(t *testing.T) {
	loop, executor := newResearchLoopHarness(t, MockScenarioUnavailable, 3)
	result, err := loop.Run(context.Background(), "compare methods and design an experiment")
	if err != nil {
		t.Fatal(err)
	}
	if result.Replans != 1 || result.Iterations[0].Decision.Type != orchestrator.DecisionReplan {
		t.Fatalf("expected inaccessible-paper replan: %+v", result)
	}
	if executor.count("closed-fetch") != 1 || executor.count("alternative-p2-fetch") != 1 || executor.count("alternative-report") != 1 {
		t.Fatalf("metadata was not reused or alternatives did not execute: %+v", executor.runs)
	}
}

func TestResearchLoopMaximumSearchIteration(t *testing.T) {
	loop, executor := newResearchLoopHarness(t, MockScenarioSearchReplan, 1)
	_, err := loop.Run(context.Background(), "compare methods and design an experiment")
	if !errors.Is(err, ErrMaximumSearchRounds) {
		t.Fatalf("expected maximum search rounds error, got %v", err)
	}
	if executor.count("search-sparse") != 1 || executor.count("search-expanded") != 0 {
		t.Fatalf("invalid revised search was executed: %+v", executor.runs)
	}
}
