package research

import (
	"errors"
	"testing"

	"aegisrt/internal/planner"
)

func searchPlan(tasks ...planner.Task) planner.Plan {
	return planner.Plan{Goal: "research", Tasks: tasks}
}

func searchTask(id, query string) planner.Task {
	return planner.Task{ID: id, Capability: "literature.search", Arguments: map[string]any{"query": query}}
}

func TestQueryPolicyTracksDistinctHistoryAndReuse(t *testing.T) {
	policy, _ := NewQueryPolicy(2)
	first := searchTask("search-1", "  Visual   Counting ")
	if err := policy.ValidatePlan(searchPlan(first)); err != nil {
		t.Fatal(err)
	}
	second := searchTask("search-2", "Grounded Counting")
	if err := policy.ValidatePlan(searchPlan(first, second)); err != nil {
		t.Fatal(err)
	}
	history := policy.History()
	if len(history) != 2 || history[0] != "visual counting" || history[1] != "grounded counting" {
		t.Fatalf("unexpected query history: %v", history)
	}
}

func TestQueryPolicyRejectsRepeatedQuery(t *testing.T) {
	policy, _ := NewQueryPolicy(3)
	if err := policy.ValidatePlan(searchPlan(searchTask("search-1", "Visual Counting"))); err != nil {
		t.Fatal(err)
	}
	err := policy.ValidatePlan(searchPlan(
		searchTask("search-1", "Visual Counting"), searchTask("search-2", " visual  counting "),
	))
	if !errors.Is(err, ErrRepeatedSearchQuery) {
		t.Fatalf("expected repeated query rejection, got %v", err)
	}
	if len(policy.History()) != 1 {
		t.Fatalf("failed plan polluted query history: %v", policy.History())
	}
}

func TestQueryPolicyRejectsMaximumRounds(t *testing.T) {
	policy, _ := NewQueryPolicy(1)
	if err := policy.ValidatePlan(searchPlan(searchTask("search-1", "first"))); err != nil {
		t.Fatal(err)
	}
	err := policy.ValidatePlan(searchPlan(searchTask("search-1", "first"), searchTask("search-2", "second")))
	if !errors.Is(err, ErrMaximumSearchRounds) {
		t.Fatalf("expected maximum rounds error, got %v", err)
	}
}

func TestResearchPlanPolicyRejectsPaperAndAnalysisCallBudget(t *testing.T) {
	policy, err := NewResearchPlanPolicy(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	plan := searchPlan(searchTask("search-1", "counting"),
		planner.Task{ID: "fetch-1", Capability: "paper.fetch"},
		planner.Task{ID: "fetch-2", Capability: "paper.fetch"},
		planner.Task{ID: "analysis-1", Capability: "paper.analyze"},
		planner.Task{ID: "analysis-2", Capability: "paper.analyze"},
	)
	if err := policy.ValidatePlan(plan); err == nil {
		t.Fatal("plan exceeding paper/analysis call budget was accepted")
	}
}

func TestResearchPlanPolicyAcceptsCapabilityChain(t *testing.T) {
	policy, err := NewResearchPlanPolicy(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidatePlan(validResearchPlan()); err != nil {
		t.Fatalf("valid research capability chain was rejected: %v", err)
	}
}

func TestResearchPlanPolicyAcceptsBoundedReplacementBranchesAcrossReplans(t *testing.T) {
	policy, err := NewResearchPlanPolicy(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	first := researchPlanForSearch("search-1", "narrow terms", "first")
	if err := policy.ValidatePlan(first); err != nil {
		t.Fatal(err)
	}
	second := researchPlanForSearch("search-2", "broader terms", "second")
	second.Tasks = append([]planner.Task{first.Tasks[0]}, second.Tasks...)
	if err := policy.ValidatePlan(second); err != nil {
		t.Fatalf("bounded replacement plan was rejected: %v", err)
	}
	third := researchPlanForSearch("search-3", "canonical field terms", "third")
	third.Tasks = append([]planner.Task{first.Tasks[0], second.Tasks[1]}, third.Tasks...)
	if err := policy.ValidatePlan(third); err != nil {
		t.Fatalf("second bounded replacement plan was rejected: %v", err)
	}
	if len(policy.History()) != 3 {
		t.Fatalf("search history = %v", policy.History())
	}
}

func TestResearchPlanPolicyRejectsInvalidCapabilityChains(t *testing.T) {
	tests := []struct {
		name string
		task planner.Task
	}{
		{"fetch without search", dependentPolicyTask("fetch-1", "paper.fetch", "other")},
		{"parse without fetch", dependentPolicyTask("parse-1", "paper.parse", "search")},
		{"analyze without parse", dependentPolicyTask("analyze-1", "paper.analyze", "fetch-1")},
		{"synthesis with one analysis", dependentPolicyTask("synthesis", "research.synthesize", "analyze-1")},
		{"experiment without synthesis", dependentPolicyTask("experiment", "experiment.design", "analyze-1")},
		{"report without experiment", dependentPolicyTask("report", "research.report", "synthesis")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := NewResearchPlanPolicy(2, 3)
			if err != nil {
				t.Fatal(err)
			}
			plan := validResearchPlan()
			for index := range plan.Tasks {
				if plan.Tasks[index].ID == test.task.ID {
					plan.Tasks[index] = test.task
				}
			}
			err = policy.ValidatePlan(plan)
			if !errors.Is(err, ErrInvalidResearchDAG) {
				t.Fatalf("expected invalid research DAG, got %v", err)
			}
			if len(policy.History()) != 0 {
				t.Fatalf("invalid plan polluted query history: %v", policy.History())
			}
		})
	}
}

func validResearchPlan() planner.Plan {
	return searchPlan(
		searchTask("search", "counting"),
		dependentPolicyTask("fetch-1", "paper.fetch", "search"),
		dependentPolicyTask("parse-1", "paper.parse", "fetch-1"),
		dependentPolicyTask("analyze-1", "paper.analyze", "parse-1"),
		dependentPolicyTask("fetch-2", "paper.fetch", "search"),
		dependentPolicyTask("parse-2", "paper.parse", "fetch-2"),
		dependentPolicyTask("analyze-2", "paper.analyze", "parse-2"),
		dependentPolicyTask("synthesis", "research.synthesize", "analyze-1", "analyze-2"),
		dependentPolicyTask("experiment", "experiment.design", "synthesis"),
		dependentPolicyTask("report", "research.report", "synthesis", "experiment"),
	)
}

func dependentPolicyTask(id, capability string, dependencies ...string) planner.Task {
	return planner.Task{ID: id, Capability: capability, DependsOn: dependencies}
}

func researchPlanForSearch(searchID, query, prefix string) planner.Plan {
	return searchPlan(
		searchTask(searchID, query),
		dependentPolicyTask(prefix+"-fetch-1", "paper.fetch", searchID),
		dependentPolicyTask(prefix+"-parse-1", "paper.parse", prefix+"-fetch-1"),
		dependentPolicyTask(prefix+"-analyze-1", "paper.analyze", prefix+"-parse-1"),
		dependentPolicyTask(prefix+"-fetch-2", "paper.fetch", searchID),
		dependentPolicyTask(prefix+"-parse-2", "paper.parse", prefix+"-fetch-2"),
		dependentPolicyTask(prefix+"-analyze-2", "paper.analyze", prefix+"-parse-2"),
		dependentPolicyTask(prefix+"-synthesis", "research.synthesize", prefix+"-analyze-1", prefix+"-analyze-2"),
		dependentPolicyTask(prefix+"-experiment", "experiment.design", prefix+"-synthesis"),
		dependentPolicyTask(prefix+"-report", "research.report", prefix+"-synthesis", prefix+"-experiment"),
	)
}
