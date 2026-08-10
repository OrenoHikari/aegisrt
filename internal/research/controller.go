package research

import (
	"context"
	"fmt"
	"strings"

	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

// GuardedController applies deterministic evidence-completeness rules around
// an LLM Controller without making the research decisions itself.
type GuardedController struct {
	inner           orchestrator.Controller
	maxPapers       int
	maxSearchRounds int
}

type ReplanLimits struct {
	MaxPapers       int
	MaxSearchRounds int
}

func NewGuardedController(inner orchestrator.Controller) (*GuardedController, error) {
	if inner == nil {
		return nil, fmt.Errorf("research Decision controller is required")
	}
	return &GuardedController{inner: inner}, nil
}

func NewGuardedControllerWithLimits(inner orchestrator.Controller, limits ReplanLimits) (*GuardedController, error) {
	controller, err := NewGuardedController(inner)
	if err != nil {
		return nil, err
	}
	if limits.MaxPapers <= 0 {
		return nil, fmt.Errorf("maximum research papers must be greater than zero")
	}
	if limits.MaxSearchRounds <= 0 {
		return nil, fmt.Errorf("maximum research search rounds must be greater than zero")
	}
	controller.maxPapers = limits.MaxPapers
	controller.maxSearchRounds = limits.MaxSearchRounds
	return controller, nil
}

func (c *GuardedController) Decide(ctx context.Context, request orchestrator.DecisionRequest) (orchestrator.Decision, error) {
	decision, err := c.inner.Decide(ctx, request)
	if err != nil {
		return orchestrator.Decision{}, err
	}
	if decision.Type != orchestrator.DecisionGoalCompleted {
		return decision, nil
	}
	for _, observation := range request.Observations {
		if observation.Capability != "research.report" || !observation.Success || !observation.Metadata.OutputVerified {
			continue
		}
		unsupported, _ := observation.Output["unsupported_claims"].(float64)
		if unsupported != 0 {
			return orchestrator.Decision{Type: orchestrator.DecisionFailed, Reason: "the generated report contains unsupported claims"}, nil
		}
		if quality, ok := observation.Output["quality"].(map[string]any); ok {
			status, _ := quality["status"].(string)
			gaps := stringValues(quality["gaps"])
			if status == QualityInsufficient {
				return orchestrator.Decision{Type: orchestrator.DecisionReplan, Reason: "citation validation passed, but verified answer coverage is insufficient: " + strings.Join(gaps, "; ")}, nil
			}
			if status == QualityPartial {
				decision.Reason = "Execution completed with citation closure, but answer completeness is PARTIAL"
				if len(gaps) > 0 {
					decision.Reason += ": " + strings.Join(gaps, "; ")
				}
			}
		}
		return decision, nil
	}
	return orchestrator.Decision{
		Type:   orchestrator.DecisionReplan,
		Reason: "research completion requires a verified citation-validated research.report output",
	}, nil
}

func stringValues(value any) []string {
	if values, ok := value.([]string); ok {
		return nonEmptyStrings(values)
	}
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

func (c *GuardedController) Replan(ctx context.Context, request orchestrator.ReplanRequest) (planner.Plan, error) {
	request.Constraints = append(request.Constraints,
		"Every paper path must use direct dependencies literature.search -> paper.fetch -> paper.parse -> paper.analyze.",
		"research.synthesize must directly depend on at least two paper.analyze tasks; experiment.design must depend on that synthesis; research.report must depend on both.",
	)
	if c.maxPapers > 0 {
		completedFetches := countCapability(request.CompletedTask, "paper.fetch")
		completedAnalyses := countCapability(request.CompletedTask, "paper.analyze")
		request.Constraints = append(request.Constraints, fmt.Sprintf(
			"The complete revised plan may contain at most %d paper.fetch tasks and at most %d paper.analyze tasks total, including preserved tasks. It currently has %d completed fetch task(s) and %d completed analysis task(s), so introduce no more than %d new fetch task(s) and %d new analysis task(s). Remove failed paper.fetch, paper.parse, and paper.analyze branches instead of duplicating them.",
			c.maxPapers, c.maxPapers, completedFetches, completedAnalyses,
			remainingBudget(c.maxPapers, completedFetches), remainingBudget(c.maxPapers, completedAnalyses),
		))
	}
	if c.maxSearchRounds > 0 {
		request.Constraints = append(request.Constraints, fmt.Sprintf(
			"Across the complete revised plan preserve existing successful literature.search tasks, introduce at most one new literature.search task in this revision, and never exceed %d distinct search task(s) total.",
			c.maxSearchRounds,
		))
	}
	for _, observation := range request.Observations {
		if observation.Capability != "literature.search" || !observation.Success {
			continue
		}
		results, ok := observation.Output["total_results"].(float64)
		if !ok || results >= 2 {
			continue
		}
		query, _ := observation.Output["query"].(string)
		request.Constraints = append(request.Constraints, fmt.Sprintf(
			"Search task %s returned too few results for query %q. Preserve it unchanged and add exactly one new literature.search task with a new ID and a semantically broader, non-equivalent query; all replacement paper paths must depend on the new search.",
			observation.TaskID, strings.TrimSpace(query),
		))
	}
	unavailable := unavailablePaperSelections(request)
	if len(unavailable) > 0 {
		request.Constraints = append(request.Constraints,
			"A paper.fetch output with available=false and retryable=false is a terminal result for that paper selection, not a transient execution failure. Preserve its verified metadata output, but never attach paper.parse to it and never fetch the same rank or paper_id again. Select a different paper, or synthesize from at least two already successful paper.analyze outputs.",
			"research.synthesize must not depend on a failed, blocked, or unavailable paper branch. Replacement papers are alternatives: once enough valid paper.analyze outputs exist, remove unavailable branches from synthesis dependencies.",
			"Unavailable selections for this run: "+formatUnavailableSelections(unavailable),
		)
	}
	revised, err := c.inner.Replan(ctx, request)
	if err != nil {
		return planner.Plan{}, err
	}
	if err := validateUnavailableRecovery(revised, request.CompletedTask, unavailable); err == nil {
		return revised, nil
	} else {
		// One bounded correction call keeps an invalid model draft away from the
		// Scheduler while still letting the cognitive plane repair its own DAG.
		request.Constraints = append(request.Constraints,
			"The previous revised-plan draft was rejected by the deterministic recovery guard: "+err.Error()+" Return a corrected full plan.",
		)
	}
	corrected, err := c.inner.Replan(ctx, request)
	if err != nil {
		return planner.Plan{}, err
	}
	if err := validateUnavailableRecovery(corrected, request.CompletedTask, unavailable); err != nil {
		return planner.Plan{}, fmt.Errorf("validate research recovery plan: %w", err)
	}
	return corrected, nil
}

type unavailablePaperSelection struct {
	TaskID   string
	SearchID string
	PaperID  string
	Rank     int
	Code     string
}

func unavailablePaperSelections(request orchestrator.ReplanRequest) []unavailablePaperSelection {
	tasks := make(map[string]planner.Task, len(request.PreviousPlan.Tasks))
	for _, task := range request.PreviousPlan.Tasks {
		tasks[task.ID] = task
	}
	var selections []unavailablePaperSelection
	for _, observation := range request.Observations {
		if observation.Capability != "paper.fetch" || observation.Output == nil {
			continue
		}
		available, exists := observation.Output["available"].(bool)
		if !exists || available {
			continue
		}
		if retryable, ok := observation.Output["retryable"].(bool); ok && retryable {
			continue
		}
		task, exists := tasks[observation.TaskID]
		if !exists {
			continue
		}
		selection := unavailablePaperSelection{
			TaskID: observation.TaskID, Rank: intArgument(task.Arguments, "rank"),
			PaperID: stringArgument(task.Arguments, "paper_id"),
		}
		if len(task.DependsOn) > 0 {
			selection.SearchID = task.DependsOn[0]
		}
		if paper, ok := observation.Output["paper"].(map[string]any); ok {
			if id, _ := paper["id"].(string); strings.TrimSpace(id) != "" {
				selection.PaperID = strings.TrimSpace(id)
			}
		}
		selection.Code, _ = observation.Output["failure_code"].(string)
		selections = append(selections, selection)
	}
	return selections
}

func validateUnavailableRecovery(plan planner.Plan, completed []planner.Task, unavailable []unavailablePaperSelection) error {
	if len(unavailable) == 0 {
		return nil
	}
	completedIDs := make(map[string]struct{}, len(completed))
	for _, task := range completed {
		completedIDs[task.ID] = struct{}{}
	}
	blockedFetchIDs := make(map[string]struct{}, len(unavailable))
	for _, selection := range unavailable {
		blockedFetchIDs[selection.TaskID] = struct{}{}
	}
	for _, task := range plan.Tasks {
		if task.Capability == "paper.fetch" {
			if _, preserved := completedIDs[task.ID]; preserved {
				continue
			}
			for _, selection := range unavailable {
				if sameUnavailableSelection(task, selection) {
					return fmt.Errorf("task %s repeats non-retryable paper selection %s", task.ID, unavailableSelectionLabel(selection))
				}
			}
		}
		if task.Capability == "paper.parse" {
			for _, dependency := range task.DependsOn {
				if _, blocked := blockedFetchIDs[dependency]; blocked {
					return fmt.Errorf("task %s parses non-retryable fetch %s", task.ID, dependency)
				}
			}
		}
	}
	return nil
}

func sameUnavailableSelection(task planner.Task, selection unavailablePaperSelection) bool {
	searchID := ""
	if len(task.DependsOn) > 0 {
		searchID = task.DependsOn[0]
	}
	paperID := stringArgument(task.Arguments, "paper_id")
	if paperID != "" && selection.PaperID != "" && paperID == selection.PaperID {
		return true
	}
	rank := intArgument(task.Arguments, "rank")
	return rank > 0 && rank == selection.Rank && searchID == selection.SearchID
}

func formatUnavailableSelections(selections []unavailablePaperSelection) string {
	values := make([]string, 0, len(selections))
	for _, selection := range selections {
		values = append(values, unavailableSelectionLabel(selection))
	}
	return strings.Join(values, "; ")
}

func unavailableSelectionLabel(selection unavailablePaperSelection) string {
	parts := []string{"task=" + selection.TaskID}
	if selection.SearchID != "" {
		parts = append(parts, "search="+selection.SearchID)
	}
	if selection.Rank > 0 {
		parts = append(parts, fmt.Sprintf("rank=%d", selection.Rank))
	}
	if selection.PaperID != "" {
		parts = append(parts, "paper_id="+selection.PaperID)
	}
	if selection.Code != "" {
		parts = append(parts, "code="+selection.Code)
	}
	return strings.Join(parts, ",")
}

func stringArgument(arguments map[string]any, name string) string {
	value, _ := arguments[name].(string)
	return strings.TrimSpace(value)
}

func intArgument(arguments map[string]any, name string) int {
	switch value := arguments[name].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func countCapability(tasks []planner.Task, capability string) int {
	count := 0
	for _, task := range tasks {
		if task.Capability == capability {
			count++
		}
	}
	return count
}

func remainingBudget(maximum, used int) int {
	if used >= maximum {
		return 0
	}
	return maximum - used
}
