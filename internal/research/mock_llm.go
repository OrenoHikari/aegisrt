package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"aegisrt/internal/llm"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

const (
	MockScenarioNormal         = "normal"
	MockScenarioSearchReplan   = "search-replan"
	MockScenarioUnavailable    = "unavailable"
	MockScenarioEvidenceReject = "evidence-rejection"
)

// MockLLMClient deterministically exercises the real Planner/Decision/Re-plan
// validation path without network or model credentials.
type MockLLMClient struct {
	mu       sync.Mutex
	scenario string
	goal     string
}

func NewMockLLMClient(scenario, goal string) (*MockLLMClient, error) {
	scenario = strings.TrimSpace(scenario)
	switch scenario {
	case MockScenarioNormal, MockScenarioSearchReplan, MockScenarioUnavailable, MockScenarioEvidenceReject:
	default:
		return nil, fmt.Errorf("research mock scenario must be normal, search-replan, or unavailable")
	}
	return &MockLLMClient{scenario: scenario, goal: strings.TrimSpace(goal)}, nil
}

func (c *MockLLMClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(request.Messages) == 0 {
		return llm.Response{}, fmt.Errorf("mock research LLM request has no messages")
	}
	content := request.Messages[len(request.Messages)-1].Content
	switch {
	case strings.HasPrefix(content, "CAPSULERT_DECISION_REQUEST\n"):
		return c.decision(strings.TrimPrefix(content, "CAPSULERT_DECISION_REQUEST\n"))
	case strings.HasPrefix(content, "CAPSULERT_REPLAN_REQUEST\n"):
		return c.replan(strings.TrimPrefix(content, "CAPSULERT_REPLAN_REQUEST\n"))
	default:
		return c.initialPlan()
	}
}

func (c *MockLLMClient) initialPlan() (llm.Response, error) {
	var tasks []planner.Task
	switch c.scenario {
	case MockScenarioNormal, MockScenarioEvidenceReject:
		tasks = append(tasks, researchTask("search-1", "Search literature", "Search representative recent work.", "literature.search", map[string]any{
			"query": "referring expression counting visual grounding", "from_year": 2021, "to_year": 2026, "max_results": 3,
		}))
		tasks = appendResearchPipeline(tasks, "normal", "search-1", c.goal, []int{1, 2, 3}, "search-1")
	case MockScenarioSearchReplan:
		tasks = append(tasks, researchTask("search-sparse", "Run narrow search", "Test the initial narrow research formulation.", "literature.search", map[string]any{
			"query": "sparse referring expression counting", "from_year": 2021, "to_year": 2026, "max_results": 3,
		}))
	case MockScenarioUnavailable:
		tasks = append(tasks, researchTask("search-1", "Search literature", "Search representative recent work.", "literature.search", map[string]any{
			"query": "referring expression counting unavailable full text", "from_year": 2021, "to_year": 2026, "max_results": 3,
		}))
		tasks = append(tasks, dependentResearchTask("closed-fetch", "Fetch leading paper", "Attempt to retrieve the leading public full text.", "paper.fetch", map[string]any{"rank": 1}, "search-1"))
	}
	return encodeResearchPlan(planner.Plan{Goal: c.goal, Tasks: tasks})
}

func (c *MockLLMClient) decision(payload string) (llm.Response, error) {
	var request orchestrator.DecisionRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return llm.Response{}, err
	}
	reportComplete := false
	searchInsufficient := false
	fullTextUnavailable := false
	hasFailure := false
	for _, observation := range request.Observations {
		hasFailure = hasFailure || !observation.Success
		switch observation.Capability {
		case "research.report":
			reportComplete = observation.Success && observation.Metadata.OutputVerified
		case "literature.search":
			if count, ok := observation.Output["total_results"].(float64); ok && count < 2 {
				searchInsufficient = true
			}
		case "paper.fetch":
			if available, ok := observation.Output["available"].(bool); ok && !available {
				fullTextUnavailable = true
			}
		}
	}
	decision := orchestrator.Decision{Type: orchestrator.DecisionGoalCompleted, Reason: "citation-validated report and experiment design are complete"}
	if !reportComplete {
		switch {
		case searchInsufficient:
			decision = orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "only one relevant paper was found; broaden the research query"}
		case fullTextUnavailable:
			decision = orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "the leading paper has no accessible full text; retain its metadata and analyze alternative public papers"}
		case hasFailure:
			decision = orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "a research capability failed and the plan needs a recoverable replacement"}
		default:
			decision = orchestrator.Decision{Type: orchestrator.DecisionFailed, Reason: "research report was not produced and no supported recovery signal was observed"}
		}
	}
	encoded, _ := json.Marshal(decision)
	return llm.Response{Content: string(encoded)}, nil
}

func (c *MockLLMClient) replan(payload string) (llm.Response, error) {
	var request orchestrator.ReplanRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return llm.Response{}, err
	}
	tasks := append([]planner.Task(nil), request.CompletedTask...)
	switch c.scenario {
	case MockScenarioSearchReplan:
		tasks = append(tasks, researchTask("search-expanded", "Broaden literature search", "Expand grounding and density-estimation terminology.", "literature.search", map[string]any{
			"query": "referring expression counting visual grounding density estimation", "from_year": 2021, "to_year": 2026, "max_results": 3,
		}))
		tasks = appendResearchPipeline(tasks, "expanded", "search-expanded", c.goal, []int{1, 2, 3}, "search-sparse", "search-expanded")
	case MockScenarioUnavailable:
		tasks = appendResearchPipeline(tasks, "alternative", "search-1", c.goal, []int{2, 3}, "search-1")
	case MockScenarioEvidenceReject:
		return llm.Response{}, fmt.Errorf("mock evidence-rejection scenario does not replan")
	default:
		return llm.Response{}, fmt.Errorf("mock normal scenario does not replan")
	}
	return encodeResearchPlan(planner.Plan{Goal: c.goal, Tasks: tasks})
}

func appendResearchPipeline(tasks []planner.Task, prefix, searchID, goal string, ranks []int, searchHistoryIDs ...string) []planner.Task {
	analysisIDs := make([]string, 0, len(ranks))
	for _, rank := range ranks {
		base := fmt.Sprintf("%s-p%d", prefix, rank)
		fetchID := base + "-fetch"
		parseID := base + "-parse"
		analysisID := base + "-analysis"
		tasks = append(tasks,
			dependentResearchTask(fetchID, "Fetch paper", fmt.Sprintf("Fetch search result rank %d.", rank), "paper.fetch", map[string]any{"rank": rank}, searchID),
			dependentResearchTask(parseID, "Parse paper", "Parse the verified public paper artifact.", "paper.parse", map[string]any{}, fetchID),
			dependentResearchTask(analysisID, "Analyze paper", "Extract traceable research findings.", "paper.analyze", map[string]any{"question": goal}, parseID),
		)
		analysisIDs = append(analysisIDs, analysisID)
	}
	synthesisID := prefix + "-synthesis"
	experimentID := prefix + "-experiment"
	reportID := prefix + "-report"
	synthesisDependencies := append([]string(nil), analysisIDs...)
	for _, searchHistoryID := range searchHistoryIDs {
		if !containsString(synthesisDependencies, searchHistoryID) {
			synthesisDependencies = append(synthesisDependencies, searchHistoryID)
		}
	}
	tasks = append(tasks,
		dependentResearchTask(synthesisID, "Synthesize research", "Compare methods and preserve evidence links.", "research.synthesize", map[string]any{"goal": goal}, synthesisDependencies...),
		dependentResearchTask(experimentID, "Design experiment", "Create explicitly labeled experimental proposals.", "experiment.design", map[string]any{"goal": goal}, synthesisID),
		dependentResearchTask(reportID, "Write research report", "Produce a citation-validated Markdown report.", "research.report", map[string]any{"goal": goal}, synthesisID, experimentID),
	)
	return tasks
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func researchTask(id, name, description, capability string, arguments map[string]any) planner.Task {
	return planner.Task{ID: id, Name: name, Description: description, Capability: capability, Arguments: arguments, DependsOn: []string{}}
}

func dependentResearchTask(id, name, description, capability string, arguments map[string]any, dependencies ...string) planner.Task {
	task := researchTask(id, name, description, capability, arguments)
	task.DependsOn = append([]string(nil), dependencies...)
	return task
}

func encodeResearchPlan(plan planner.Plan) (llm.Response, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Content: string(encoded)}, nil
}
