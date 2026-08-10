package research

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
)

// Evaluate derives metrics only from real loop records and structured
// observations. Token usage remains nil unless the provider returned usage.
func Evaluate(result orchestrator.LoopResult, runErr error, duration time.Duration) RunMetrics {
	metrics := RunMetrics{
		ReplanCount: result.Replans, TotalIterations: len(result.Iterations),
		Duration: duration.String(), DurationMillis: duration.Milliseconds(),
		RecoverySuccess: result.Replans > 0 && runErr == nil,
	}
	taskPhase := make(map[string]scheduler.Phase)
	retrieved := make(map[string]struct{})
	usable := make(map[string]struct{})
	evidence := make(map[string]struct{})
	queries := make(map[string]struct{})
	referenceTasks := make(map[string]struct{})
	analyzed := make(map[string]struct{})
	reportChecks, closedReports := 0, 0
	facts, factsWithEvidence := 0, 0
	inputTokens, outputTokens, usageRecords := 0, 0, 0
	for _, iteration := range result.Iterations {
		for _, record := range iteration.Execution.Records {
			taskPhase[record.ID] = record.Phase
		}
		for _, observation := range iteration.Observations {
			switch observation.Capability {
			case "literature.search":
				if query, ok := observation.Output["query"].(string); ok {
					queries[strings.ToLower(strings.TrimSpace(query))] = struct{}{}
				}
				if papers, ok := observation.Output["papers"].([]any); ok {
					for _, raw := range papers {
						if paper, ok := raw.(map[string]any); ok {
							if id, ok := paper["id"].(string); ok {
								retrieved[id] = struct{}{}
							}
						}
					}
				}
			case "paper.fetch":
				if available, _ := observation.Output["available"].(bool); available {
					if paper, ok := observation.Output["paper"].(map[string]any); ok {
						if id, ok := paper["id"].(string); ok {
							usable[id] = struct{}{}
						}
					}
				}
			case "paper.analyze", "research.synthesize":
				collectEvidenceIDs(observation.Output["evidence"], evidence)
				if observation.Capability == "paper.analyze" {
					if paper, ok := observation.Output["paper"].(map[string]any); ok {
						if id, ok := paper["id"].(string); ok {
							analyzed[id] = struct{}{}
						}
					}
					if candidates, ok := observation.Output["candidate_findings"].([]any); ok {
						metrics.CandidateFindings += len(candidates)
					}
					if findings, ok := observation.Output["findings"].([]any); ok {
						for _, raw := range findings {
							finding, _ := raw.(map[string]any)
							if evidenceID, _ := finding["evidence_id"].(string); evidenceID != "" {
								metrics.VerifiedFindings++
							}
							switch finding["status"] {
							case string(FindingSupported):
								metrics.SupportedFindings++
							case string(FindingUnsupported):
								metrics.UnsupportedFindings++
							}
						}
					}
					if usage, ok := observation.Output["usage"].(map[string]any); ok {
						inputTokens += jsonInt(usage["input_tokens"])
						outputTokens += jsonInt(usage["output_tokens"])
						usageRecords++
					}
				}
				if observation.Capability == "research.synthesize" {
					if rawFacts, ok := observation.Output["facts"].([]any); ok {
						facts += len(rawFacts)
						for _, raw := range rawFacts {
							if fact, ok := raw.(map[string]any); ok {
								if ids, ok := fact["evidence_ids"].([]any); ok && len(ids) > 0 {
									factsWithEvidence++
								}
							}
						}
					}
				}
			case "research.report":
				reportChecks++
				if observation.Success && observation.Metadata.OutputVerified {
					if closed, _ := observation.Output["citation_closed"].(bool); closed {
						closedReports++
					}
				}
				if count, ok := observation.Output["unsupported_claims"].(float64); ok {
					metrics.UnsupportedClaimCount += int(count)
				}
				metrics.HallucinatedReferenceCount += jsonInt(observation.Output["hallucinated_references"])
			}
			if observation.Capability == "research.synthesize" {
				if _, processed := referenceTasks[observation.TaskID]; processed {
					continue
				}
				referenceTasks[observation.TaskID] = struct{}{}
			}
			if rawReferences, ok := observation.Output["references"].([]any); ok {
				seenReferences := make(map[string]struct{})
				for _, raw := range rawReferences {
					if paper, ok := raw.(map[string]any); ok {
						if id, ok := paper["id"].(string); ok {
							if _, duplicate := seenReferences[id]; duplicate {
								metrics.DuplicatedReferences++
							}
							seenReferences[id] = struct{}{}
						}
					}
				}
			}
		}
	}
	metrics.TotalTasks = len(taskPhase)
	for _, phase := range taskPhase {
		if phase == scheduler.PhaseSucceeded {
			metrics.SuccessfulTasks++
		} else {
			metrics.FailedTasks++
		}
	}
	metrics.RetrievedPapers = len(retrieved)
	metrics.UsablePapers = len(usable)
	metrics.AnalyzedPapers = len(analyzed)
	metrics.EvidenceBackedClaims = len(evidence)
	metrics.SearchIterations = len(queries)
	metrics.TaskSuccessRate = ratio(metrics.SuccessfulTasks, metrics.TotalTasks)
	metrics.EvidenceVerificationRate = ratio(metrics.VerifiedFindings, metrics.CandidateFindings)
	metrics.UnsupportedClaimCount += metrics.UnsupportedFindings
	metrics.CitationClosureRate = ratio(closedReports, reportChecks)
	metrics.FactWithEvidenceRatio = ratio(factsWithEvidence, facts)
	metrics.UnsupportedFactCount = facts - factsWithEvidence
	if usageRecords > 0 {
		metrics.InputTokens = &inputTokens
		metrics.OutputTokens = &outputTokens
	}
	if errors.Is(runErr, orchestrator.ErrRepeatedPlan) || errors.Is(runErr, ErrRepeatedSearchQuery) {
		metrics.RepeatedPlanCount = 1
	}
	if errors.Is(runErr, planner.ErrInvalidJSON) || errors.Is(runErr, planner.ErrUnknownCapability) || errors.Is(runErr, ErrMaximumSearchRounds) {
		metrics.InvalidPlanCount = 1
	}
	if errors.Is(runErr, orchestrator.ErrMaxReplansExceeded) || errors.Is(runErr, orchestrator.ErrLoopNoProgress) || errors.Is(runErr, context.DeadlineExceeded) {
		metrics.MaxLoopAbortCount = 1
	}
	metrics.RecoveryRate = ratio(boolInt(metrics.RecoverySuccess), boolInt(result.Replans > 0))
	return metrics
}

func jsonInt(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case int:
		return number
	default:
		return 0
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ExportReport copies the verified report artifact to a stable CLI path using
// an atomic rename.
func ExportReport(result orchestrator.LoopResult, destination string) (string, error) {
	var source string
	for iterationIndex := len(result.Iterations) - 1; iterationIndex >= 0 && source == ""; iterationIndex-- {
		for _, record := range result.Iterations[iterationIndex].Execution.Records {
			if record.Role == "research.report" && record.Phase == scheduler.PhaseSucceeded && record.OutputVerified {
				source = filepath.Join(record.OutputCommitPath, "report.md")
				break
			}
		}
	}
	if source == "" {
		return "", fmt.Errorf("verified research report artifact was not produced")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	if len(data) > 4*1024*1024 {
		return "", fmt.Errorf("research report exceeds export limit")
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".report-*.md")
	if err != nil {
		return "", err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func collectEvidenceIDs(raw any, target map[string]struct{}) {
	items, ok := raw.([]any)
	if !ok {
		return
	}
	for _, item := range items {
		if evidence, ok := item.(map[string]any); ok {
			if id, ok := evidence["id"].(string); ok {
				target[id] = struct{}{}
			}
		}
	}
}
