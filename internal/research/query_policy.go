package research

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"aegisrt/internal/planner"
)

var (
	ErrRepeatedSearchQuery = errors.New("research plan repeated an earlier search query")
	ErrMaximumSearchRounds = errors.New("maximum research search rounds exceeded")
	ErrInvalidResearchDAG  = errors.New("research plan has an invalid capability dependency")
)

// QueryPolicy is a stateful application PlanValidator. Reused task IDs do not
// consume another round, while new tasks cannot repeat a normalized query.
type QueryPolicy struct {
	mu        sync.Mutex
	maximum   int
	byTaskID  map[string]string
	querySeen map[string]string
	history   []string
}

// ResearchPlanPolicy combines query iteration limits with paper/context
// budgets. It is evaluated before any revised plan reaches the Scheduler.
type ResearchPlanPolicy struct {
	queries   *QueryPolicy
	maxPapers int
}

func NewResearchPlanPolicy(maxSearchRounds, maxPapers int) (*ResearchPlanPolicy, error) {
	queries, err := NewQueryPolicy(maxSearchRounds)
	if err != nil {
		return nil, err
	}
	if maxPapers <= 0 || maxPapers > MaximumSearchResults {
		return nil, fmt.Errorf("maximum research papers must be between 1 and %d", MaximumSearchResults)
	}
	return &ResearchPlanPolicy{queries: queries, maxPapers: maxPapers}, nil
}

func (p *ResearchPlanPolicy) ValidatePlan(plan planner.Plan) error {
	var fetches, analyses int
	for _, task := range plan.Tasks {
		switch task.Capability {
		case "paper.fetch":
			fetches++
		case "paper.analyze":
			analyses++
		}
	}
	if fetches > p.maxPapers || analyses > p.maxPapers {
		return fmt.Errorf("research plan exceeds max-papers=%d (fetch=%d analyze=%d)", p.maxPapers, fetches, analyses)
	}
	if err := validateResearchDependencies(plan); err != nil {
		return err
	}
	return p.queries.ValidatePlan(plan)
}

func validateResearchDependencies(plan planner.Plan) error {
	capabilityByTask := make(map[string]string, len(plan.Tasks))
	for _, task := range plan.Tasks {
		capabilityByTask[task.ID] = task.Capability
	}

	for _, task := range plan.Tasks {
		required := map[string]int{}
		switch task.Capability {
		case "paper.fetch":
			required["literature.search"] = 1
		case "paper.parse":
			required["paper.fetch"] = 1
		case "paper.analyze":
			required["paper.parse"] = 1
		case "research.synthesize":
			required["paper.analyze"] = 2
		case "experiment.design":
			required["research.synthesize"] = 1
		case "research.report":
			required["research.synthesize"] = 1
			required["experiment.design"] = 1
		}
		if len(required) == 0 {
			continue
		}

		counts := make(map[string]int, len(required))
		for _, dependency := range task.DependsOn {
			counts[capabilityByTask[dependency]]++
		}
		for capability, minimum := range required {
			if counts[capability] < minimum {
				return fmt.Errorf("%w: task %s using %s requires at least %d direct %s dependency", ErrInvalidResearchDAG, task.ID, task.Capability, minimum, capability)
			}
		}
	}
	return nil
}

func (p *ResearchPlanPolicy) History() []string { return p.queries.History() }

func NewQueryPolicy(maximum int) (*QueryPolicy, error) {
	if maximum <= 0 {
		return nil, fmt.Errorf("maximum search rounds must be greater than zero")
	}
	return &QueryPolicy{maximum: maximum, byTaskID: make(map[string]string), querySeen: make(map[string]string)}, nil
}

func (p *QueryPolicy) ValidatePlan(plan planner.Plan) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	pendingByTask := make(map[string]string)
	pendingByQuery := make(map[string]string)
	for _, task := range plan.Tasks {
		if task.Capability != "literature.search" {
			continue
		}
		query, _ := task.Arguments["query"].(string)
		query = strings.Join(strings.Fields(strings.ToLower(query)), " ")
		if query == "" {
			return fmt.Errorf("literature.search query is required")
		}
		if previous, exists := p.byTaskID[task.ID]; exists {
			if previous != query {
				return fmt.Errorf("reused search task %s changed its query", task.ID)
			}
			continue
		}
		if priorTask, exists := p.querySeen[query]; exists {
			return fmt.Errorf("%w: %q already used by %s", ErrRepeatedSearchQuery, query, priorTask)
		}
		if priorTask, exists := pendingByQuery[query]; exists {
			return fmt.Errorf("%w: %q already used by %s", ErrRepeatedSearchQuery, query, priorTask)
		}
		if len(p.history)+len(pendingByTask) >= p.maximum {
			return fmt.Errorf("%w: %d", ErrMaximumSearchRounds, p.maximum)
		}
		pendingByTask[task.ID] = query
		pendingByQuery[query] = task.ID
	}
	if len(pendingByTask) > 1 {
		return fmt.Errorf("research plans may introduce only one new literature.search task per iteration")
	}
	for taskID, query := range pendingByTask {
		p.byTaskID[taskID] = query
		p.querySeen[query] = taskID
	}
	for _, task := range plan.Tasks {
		if query, exists := pendingByTask[task.ID]; exists {
			p.history = append(p.history, query)
		}
	}
	return nil
}

func (p *QueryPolicy) History() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.history...)
}
