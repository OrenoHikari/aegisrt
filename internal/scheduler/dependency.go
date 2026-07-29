package scheduler

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"aegisrt/internal/agent"
)

func normalizeDependencies(
	agentID string,
	dependencies []string,
) ([]string, error) {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(dependencies))

	for _, dependencyID := range dependencies {
		dependencyID = strings.TrimSpace(dependencyID)

		if dependencyID == "" {
			return nil, fmt.Errorf(
				"dependency Agent ID is required",
			)
		}

		if dependencyID == agentID {
			return nil, fmt.Errorf(
				"Agent %q cannot depend on itself",
				agentID,
			)
		}

		if _, exists := seen[dependencyID]; exists {
			continue
		}

		seen[dependencyID] = struct{}{}
		result = append(result, dependencyID)
	}

	sort.Strings(result)

	return result, nil
}

// dependencyStatusLocked returns:
//
//   - ready: every dependency succeeded with verified output
//   - blockedReason: one dependency permanently failed
//   - outputs: verified upstream outputs
//
// s.mu must already be held.
func (s *Scheduler) dependencyStatusLocked(
	entry queuedJob,
) (
	ready bool,
	blockedReason string,
	outputs map[string]agent.DependencyOutput,
) {
	if len(entry.DependsOn) == 0 {
		return true, "", nil
	}

	outputs = make(
		map[string]agent.DependencyOutput,
		len(entry.DependsOn),
	)

	for _, dependencyID := range entry.DependsOn {
		record, exists := s.records[dependencyID]

		if !exists {
			return false,
				fmt.Sprintf(
					"dependency %s is unknown",
					dependencyID,
				),
				nil
		}

		switch record.Phase {
		case PhaseFailed:
			return false,
				fmt.Sprintf(
					"dependency %s failed: %s",
					dependencyID,
					record.Error,
				),
				nil

		case PhaseBlocked:
			return false,
				fmt.Sprintf(
					"dependency %s is blocked",
					dependencyID,
				),
				nil

		case PhaseSucceeded:
			if !record.OutputCommitted {
				return false,
					fmt.Sprintf(
						"dependency %s has no committed output",
						dependencyID,
					),
					nil
			}

			if !record.OutputVerified {
				reason :=
					record.OutputVerificationError

				if reason == "" {
					reason = "output was not verified"
				}

				return false,
					fmt.Sprintf(
						"dependency %s failed integrity verification: %s",
						dependencyID,
						reason,
					),
					nil
			}

			verifiedAt := time.Time{}

			if record.OutputVerifiedAt != nil {
				verifiedAt = *record.OutputVerifiedAt
			}

			outputs[dependencyID] =
				agent.DependencyOutput{
					AgentID:       dependencyID,
					TransactionID: record.OutputTransactionID,
					CommitPath:    record.OutputCommitPath,
					ManifestPath:  record.OutputManifestPath,
					FileCount:     record.OutputFileCount,
					TotalBytes:    record.OutputBytes,

					Verified: true,

					VerificationMethod: record.OutputVerificationMethod,

					ManifestSHA256: record.OutputManifestSHA256,

					VerifiedAt: verifiedAt,
				}

		default:
			// QUEUED or RUNNING: dependency may still succeed.
			return false, "", nil
		}
	}

	return true, "", outputs
}

// blockUnrunnableLocked removes one permanently blocked job.
//
// s.mu must already be held.
func (s *Scheduler) blockUnrunnableLocked() bool {
	for index, entry := range s.queue {
		_, reason, _ :=
			s.dependencyStatusLocked(entry)

		if reason == "" {
			continue
		}

		s.blockEntryLocked(index, reason)
		return true
	}

	return false
}

// blockEntryLocked marks one queued Agent as BLOCKED.
//
// s.mu must already be held.
func (s *Scheduler) blockEntryLocked(
	index int,
	reason string,
) {
	entry := s.queue[index]

	s.queue = append(
		s.queue[:index],
		s.queue[index+1:]...,
	)

	finishedAt := time.Now().UTC()
	record := s.records[entry.Agent.ID]

	record.Phase = PhaseBlocked
	record.FinishedAt = &finishedAt
	record.Error = reason
	record.DispatchReason =
		"dependency gate blocked execution: " + reason

	s.emitBlocked(record)

	s.pending.Done()
	s.cond.Broadcast()
}

func applyDependencyOutputs(
	acb *agent.ACB,
	outputs map[string]agent.DependencyOutput,
) error {
	if len(outputs) == 0 {
		return nil
	}

	acb.DependencyOutputs =
		cloneDependencyOutputs(outputs)

	if acb.Environment == nil {
		acb.Environment = make(map[string]string)
	}

	data, err := json.Marshal(outputs)
	if err != nil {
		return fmt.Errorf(
			"encode dependency outputs: %w",
			err,
		)
	}

	acb.Environment["AEGIS_DEPENDENCY_OUTPUTS_JSON"] =
		string(data)

	acb.Environment["AEGIS_DEPENDENCY_COUNT"] =
		fmt.Sprintf("%d", len(outputs))

	return nil
}

func dependencyOutputFromACB(
	acb *agent.ACB,
) agent.DependencyOutput {
	return agent.DependencyOutput{
		AgentID:       acb.ID,
		TransactionID: acb.OutputTransactionID,
		CommitPath:    acb.OutputCommitPath,
		ManifestPath:  acb.OutputManifestPath,
		FileCount:     acb.OutputFileCount,
		TotalBytes:    acb.OutputBytes,
	}
}

func cloneDependencyOutputs(
	source map[string]agent.DependencyOutput,
) map[string]agent.DependencyOutput {
	if source == nil {
		return nil
	}

	result := make(
		map[string]agent.DependencyOutput,
		len(source),
	)

	for key, value := range source {
		result[key] = value
	}

	return result
}

func cloneStrings(source []string) []string {
	if source == nil {
		return nil
	}

	result := make([]string, len(source))
	copy(result, source)

	return result
}
