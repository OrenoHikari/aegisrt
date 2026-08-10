package research

import (
	"context"
	"strings"
	"testing"

	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

type recordingResearchController struct {
	replan orchestrator.ReplanRequest
}

type scriptedResearchController struct {
	plans    []planner.Plan
	requests []orchestrator.ReplanRequest
}

type completingResearchController struct{}

func (completingResearchController) Decide(context.Context, orchestrator.DecisionRequest) (orchestrator.Decision, error) {
	return orchestrator.Decision{Type: orchestrator.DecisionGoalCompleted, Reason: "model says complete"}, nil
}

func (completingResearchController) Replan(_ context.Context, request orchestrator.ReplanRequest) (planner.Plan, error) {
	return request.PreviousPlan, nil
}

func (c *scriptedResearchController) Decide(context.Context, orchestrator.DecisionRequest) (orchestrator.Decision, error) {
	return orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "test"}, nil
}

func (c *scriptedResearchController) Replan(_ context.Context, request orchestrator.ReplanRequest) (planner.Plan, error) {
	c.requests = append(c.requests, request)
	index := len(c.requests) - 1
	if index >= len(c.plans) {
		index = len(c.plans) - 1
	}
	return c.plans[index], nil
}

func TestGuardedControllerAddsExplicitReplanBudgets(t *testing.T) {
	inner := &recordingResearchController{}
	controller, err := NewGuardedControllerWithLimits(inner, ReplanLimits{MaxPapers: 5, MaxSearchRounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	request := orchestrator.ReplanRequest{
		PreviousPlan: planner.Plan{Goal: "goal"},
		CompletedTask: []planner.Task{
			{ID: "fetch-done", Capability: "paper.fetch"},
			{ID: "analysis-done", Capability: "paper.analyze"},
		},
		FailedTask: []planner.Task{{ID: "fetch-failed", Capability: "paper.fetch"}},
		Observations: []orchestrator.Observation{{
			TaskID: "search-1", Capability: "literature.search", Success: true,
			Output: map[string]any{"query": "narrow query", "total_results": float64(0)},
		}},
	}
	if _, err := controller.Replan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	constraints := strings.Join(inner.replan.Constraints, "\n")
	for _, required := range []string{
		"at most 5 paper.fetch", "at most 5 paper.analyze", "no more than 4 new fetch",
		"Remove failed paper.fetch", "never exceed 3 distinct search",
	} {
		if !strings.Contains(constraints, required) {
			t.Fatalf("replan constraints missing %q:\n%s", required, constraints)
		}
	}
}

func TestGuardedControllerRejectsInvalidReplanLimits(t *testing.T) {
	inner := &recordingResearchController{}
	for _, limits := range []ReplanLimits{{MaxPapers: 0, MaxSearchRounds: 3}, {MaxPapers: 5, MaxSearchRounds: 0}} {
		if _, err := NewGuardedControllerWithLimits(inner, limits); err == nil {
			t.Fatalf("invalid limits were accepted: %+v", limits)
		}
	}
}

func TestGuardedControllerReportsAnswerCompletenessSeparately(t *testing.T) {
	controller, err := NewGuardedController(completingResearchController{})
	if err != nil {
		t.Fatal(err)
	}
	request := orchestrator.DecisionRequest{Observations: []orchestrator.Observation{{
		Capability: "research.report", Success: true, Metadata: orchestrator.ObservationMeta{OutputVerified: true},
		Output: map[string]any{
			"unsupported_claims": float64(0),
			"quality":            map[string]any{"status": QualityPartial, "gaps": []any{"no verified evaluation metric coverage"}},
		},
	}}}
	decision, err := controller.Decide(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Type != orchestrator.DecisionGoalCompleted || !strings.Contains(decision.Reason, "PARTIAL") || !strings.Contains(decision.Reason, "metric") {
		t.Fatalf("partial quality was hidden: %+v", decision)
	}
	request.Observations[0].Output["quality"] = map[string]any{"status": QualityInsufficient, "gaps": []any{"no verified dataset coverage"}}
	decision, err = controller.Decide(context.Background(), request)
	if err != nil || decision.Type != orchestrator.DecisionReplan {
		t.Fatalf("insufficient quality did not request recovery: decision=%+v err=%v", decision, err)
	}
}

func (c *recordingResearchController) Decide(context.Context, orchestrator.DecisionRequest) (orchestrator.Decision, error) {
	return orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "test"}, nil
}

func (c *recordingResearchController) Replan(_ context.Context, request orchestrator.ReplanRequest) (planner.Plan, error) {
	c.replan = request
	return request.PreviousPlan, nil
}

func TestGuardedControllerAddsInsufficientSearchReplanConstraint(t *testing.T) {
	inner := &recordingResearchController{}
	controller, err := NewGuardedController(inner)
	if err != nil {
		t.Fatal(err)
	}
	request := orchestrator.ReplanRequest{
		PreviousPlan: planner.Plan{Goal: "goal"},
		Observations: []orchestrator.Observation{{
			TaskID: "search-1", Capability: "literature.search", Success: true,
			Output: map[string]any{"query": "narrow query", "total_results": float64(0)},
		}},
	}
	if _, err := controller.Replan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(inner.replan.Constraints) != 3 {
		t.Fatalf("missing research replan constraints: %v", inner.replan.Constraints)
	}
}

func TestGuardedControllerCorrectsRepeatedNonRetryablePaperSelection(t *testing.T) {
	search := planner.Task{ID: "search", Name: "Search", Description: "search", Capability: "literature.search", Arguments: map[string]any{"query": "topic", "max_results": float64(5)}}
	unavailable := planner.Task{ID: "fetch-2", Name: "Fetch rank 2", Description: "fetch", Capability: "paper.fetch", Arguments: map[string]any{"rank": float64(2)}, DependsOn: []string{"search"}}
	repeated := planner.Task{ID: "retry-2", Name: "Retry rank 2", Description: "retry", Capability: "paper.fetch", Arguments: map[string]any{"rank": float64(2)}, DependsOn: []string{"search"}}
	alternative := planner.Task{ID: "fetch-4", Name: "Fetch rank 4", Description: "alternative", Capability: "paper.fetch", Arguments: map[string]any{"rank": float64(4)}, DependsOn: []string{"search"}}
	inner := &scriptedResearchController{plans: []planner.Plan{
		{Goal: "goal", Tasks: []planner.Task{search, unavailable, repeated}},
		{Goal: "goal", Tasks: []planner.Task{search, unavailable, alternative}},
	}}
	controller, err := NewGuardedController(inner)
	if err != nil {
		t.Fatal(err)
	}
	request := orchestrator.ReplanRequest{
		PreviousPlan:  planner.Plan{Goal: "goal", Tasks: []planner.Task{search, unavailable}},
		CompletedTask: []planner.Task{search, unavailable},
		Observations: []orchestrator.Observation{{
			TaskID: "fetch-2", Capability: "paper.fetch", Success: true,
			Output: map[string]any{
				"available": false, "retryable": false, "failure_code": "PDF_LIMIT_EXCEEDED",
				"paper": map[string]any{"id": "2511.11313"}, "required_bytes": float64(22682393), "limit_bytes": float64(20971520),
			},
		}},
	}
	plan, err := controller.Replan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.requests) != 2 || len(plan.Tasks) != 3 || plan.Tasks[2].ID != "fetch-4" {
		t.Fatalf("bounded correction did not select the alternative: calls=%d plan=%+v", len(inner.requests), plan)
	}
	constraints := strings.Join(inner.requests[1].Constraints, "\n")
	for _, expected := range []string{"PDF_LIMIT_EXCEEDED", "rank=2", "previous revised-plan draft was rejected"} {
		if !strings.Contains(constraints, expected) {
			t.Fatalf("correction constraint missing %q:\n%s", expected, constraints)
		}
	}
}

func TestGuardedControllerRejectsParsingUnavailablePaper(t *testing.T) {
	search := planner.Task{ID: "search", Capability: "literature.search"}
	unavailable := planner.Task{ID: "fetch-2", Capability: "paper.fetch", Arguments: map[string]any{"rank": float64(2)}, DependsOn: []string{"search"}}
	parse := planner.Task{ID: "parse-2", Capability: "paper.parse", DependsOn: []string{"fetch-2"}}
	inner := &scriptedResearchController{plans: []planner.Plan{{Goal: "goal", Tasks: []planner.Task{search, unavailable, parse}}}}
	controller, _ := NewGuardedController(inner)
	_, err := controller.Replan(context.Background(), orchestrator.ReplanRequest{
		PreviousPlan: planner.Plan{Goal: "goal", Tasks: []planner.Task{search, unavailable}}, CompletedTask: []planner.Task{search, unavailable},
		Observations: []orchestrator.Observation{{TaskID: "fetch-2", Capability: "paper.fetch", Success: true, Output: map[string]any{"available": false, "retryable": false}}},
	})
	if err == nil || !strings.Contains(err.Error(), "parses non-retryable fetch") {
		t.Fatalf("unavailable fetch was allowed back into parse DAG: %v", err)
	}
}
