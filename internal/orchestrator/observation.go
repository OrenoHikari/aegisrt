package orchestrator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

const structuredFailureMarker = "CAPSULE_STRUCTURED_FAILURE:"

const (
	maximumObservationJSONBytes       = 64 * 1024
	maximumObservationSourceJSONBytes = 2 * 1024 * 1024
	maximumObservationSummary         = 4 * 1024
	maximumDependencySummary          = 1024
)

// Observation is the bounded cognitive-plane view of one real Scheduler
// record and its verified output transaction.
type Observation struct {
	RunID                     string            `json:"run_id"`
	Iteration                 int               `json:"iteration"`
	TaskID                    string            `json:"task_id"`
	Capability                string            `json:"capability"`
	State                     scheduler.Phase   `json:"state"`
	Success                   bool              `json:"success"`
	Reused                    bool              `json:"reused"`
	Output                    map[string]any    `json:"output,omitempty"`
	OutputSummary             string            `json:"output_summary,omitempty"`
	Error                     string            `json:"error,omitempty"`
	ExitCode                  *int              `json:"exit_code,omitempty"`
	DependencyTaskIDs         []string          `json:"dependency_task_ids,omitempty"`
	PreviousDependencyOutputs map[string]string `json:"previous_dependency_outputs,omitempty"`
	Metadata                  ObservationMeta   `json:"metadata"`
}

// ObservationMeta selects useful existing Runtime metadata without exposing
// raw logs or workspace contents to the LLM.
type ObservationMeta struct {
	WorkerID       int           `json:"worker_id,omitempty"`
	Duration       time.Duration `json:"duration,omitempty"`
	OutputVerified bool          `json:"output_verified"`
	OutputFiles    int           `json:"output_files,omitempty"`
	OutputBytes    uint64        `json:"output_bytes,omitempty"`
}

// Observe derives structured environment feedback from the current execution.
func (o *Orchestrator) Observe(runID string, iteration int, result Result) []Observation {
	tasks := make(map[string]planner.Task, len(result.Plan.Tasks))
	for _, task := range result.Plan.Tasks {
		tasks[task.ID] = task
	}
	reused := stringSet(result.ReusedTaskIDs)
	observations := make([]Observation, 0, len(result.Records))
	for _, record := range result.Records {
		task, exists := tasks[record.ID]
		if !exists {
			continue
		}
		observation := observationFromRecord(runID, iteration, task, record, reused[record.ID])
		observations = append(observations, observation)
		payload := map[string]any{
			"run_id": runID, "iteration": iteration, "capability": task.Capability,
			"success": observation.Success, "reused": observation.Reused,
			"duration": observation.Metadata.Duration.String(), "error": observation.Error,
		}
		if task.Capability == "paper.fetch" {
			for _, key := range []string{"available", "reason", "failure_code", "retryable", "required_bytes", "limit_bytes"} {
				if value, exists := observation.Output[key]; exists {
					payload[key] = value
				}
			}
			if paper, ok := observation.Output["paper"].(map[string]any); ok {
				payload["paper_id"] = paper["id"]
				payload["paper_title"] = paper["title"]
			}
		}
		if task.Capability == "experiment.run" {
			for _, key := range []string{
				"method", "attempt", "status", "failure_code", "reason", "retryable",
				"memory_peak_bytes", "memory_limit_bytes", "accuracy", "runtime_ms",
			} {
				if value, exists := observation.Output[key]; exists {
					payload[key] = value
				}
			}
		}
		if task.Capability == "experiment.manifest.inspect" {
			for _, key := range []string{
				"directory", "manifest_file", "manifest_sha256", "dataset_path",
			} {
				if value, exists := observation.Output[key]; exists {
					payload[key] = value
				}
			}
			payload["method_count"] = jsonArrayLength(observation.Output["methods"])
			payload["entry_count"] = jsonArrayLength(observation.Output["entries"])
		}
		o.publish(
			telemetry.KindObservationCreated,
			record.ID,
			string(record.Phase),
			payload,
		)
		o.publishResearchObservation(observation)
	}
	return observations
}

func (o *Orchestrator) publishResearchObservation(observation Observation) {
	payload := map[string]any{
		"run_id": observation.RunID, "iteration": observation.Iteration,
		"capability": observation.Capability, "duration": observation.Metadata.Duration.String(),
	}
	switch observation.Capability {
	case "paper.parse":
		payload["parser"] = observation.Output["parser"]
		payload["pages"] = jsonArrayLength(observation.Output["pages"])
		payload["sections"] = jsonArrayLength(observation.Output["sections"])
		if diagnostics, ok := observation.Output["diagnostics"].(map[string]any); ok {
			payload["diagnostics"] = diagnostics
		}
		payload["error"] = observation.Error
		o.publish(telemetry.KindPaperParsed, observation.TaskID, string(observation.State), payload)
	case "paper.analyze":
		candidates := jsonArrayLength(observation.Output["candidate_findings"])
		findings, _ := observation.Output["findings"].([]any)
		supported, sourceVerified, rejected := 0, 0, 0
		for _, raw := range findings {
			finding, _ := raw.(map[string]any)
			switch finding["status"] {
			case "SUPPORTED":
				supported++
				sourceVerified++
			case "VERIFIED_SOURCE":
				sourceVerified++
			case "UNSUPPORTED":
				rejected++
			}
		}
		payload["candidates"] = candidates
		payload["supported"] = supported
		payload["source_verified"] = sourceVerified
		payload["rejected"] = rejected
		payload["error"] = observation.Error
		if candidates > 0 {
			o.publish(telemetry.KindCandidateFinding, observation.TaskID, string(observation.State), payload)
		}
		if sourceVerified > 0 {
			o.publish(telemetry.KindEvidenceVerified, observation.TaskID, string(observation.State), payload)
		}
		if rejected > 0 {
			o.publish(telemetry.KindEvidenceRejected, observation.TaskID, string(observation.State), payload)
			o.publish(telemetry.KindClaimUnsupported, observation.TaskID, string(observation.State), payload)
		}
		if supported > 0 {
			o.publish(telemetry.KindClaimSupported, observation.TaskID, string(observation.State), payload)
		}
		o.publish(telemetry.KindPaperAnalysisDone, observation.TaskID, string(observation.State), payload)
	case "research.report":
		payload["error"] = observation.Error
		if quality, ok := observation.Output["quality"].(map[string]any); ok {
			payload["quality_status"] = quality["status"]
			payload["quality_score"] = quality["score"]
			payload["quality_gaps"] = quality["gaps"]
		}
		if observation.Success && observation.Metadata.OutputVerified {
			o.publish(telemetry.KindReportValidated, observation.TaskID, string(observation.State), payload)
		} else {
			o.publish(telemetry.KindReportValidationFail, observation.TaskID, string(observation.State), payload)
		}
	}
}

func jsonArrayLength(value any) int {
	items, _ := value.([]any)
	return len(items)
}

func observationFromRecord(runID string, iteration int, task planner.Task, record scheduler.Record, reused bool) Observation {
	observation := Observation{
		RunID:                     runID,
		Iteration:                 iteration,
		TaskID:                    record.ID,
		Capability:                task.Capability,
		State:                     record.Phase,
		Success:                   record.Phase == scheduler.PhaseSucceeded,
		Reused:                    reused,
		Error:                     record.Error,
		ExitCode:                  cloneExitCode(record.ExitCode),
		DependencyTaskIDs:         append([]string(nil), record.DependsOn...),
		PreviousDependencyOutputs: dependencySummaries(record.DependencyOutputs),
		Metadata: ObservationMeta{
			WorkerID:       record.WorkerID,
			OutputVerified: record.OutputVerified,
			OutputFiles:    record.OutputFileCount,
			OutputBytes:    record.OutputBytes,
		},
	}
	if record.StartedAt != nil && record.FinishedAt != nil {
		observation.Metadata.Duration = record.FinishedAt.Sub(*record.StartedAt)
	}
	if record.OutputVerified && record.OutputCommitPath != "" {
		jsonPath := filepath.Join(record.OutputCommitPath, "result.json")
		if data, err := readBoundedFile(jsonPath, maximumObservationSourceJSONBytes); err == nil {
			var output map[string]any
			if json.Unmarshal(data, &output) == nil {
				observation.Output = projectObservationOutput(task.Capability, output)
			}
		}
		textPath := filepath.Join(record.OutputCommitPath, "result.txt")
		if data, err := readBoundedFile(textPath, maximumObservationSummary); err == nil {
			observation.OutputSummary = strings.TrimSpace(string(data))
		} else if !errors.Is(err, os.ErrNotExist) {
			observation.OutputSummary = "verified result omitted: " + err.Error()
		}
	}
	if observation.OutputSummary == "" && observation.Error != "" {
		observation.OutputSummary = observation.Error
	}
	if task.Capability == "experiment.run" && !observation.Success {
		if failure := structuredFailureFromError(observation.Error); failure != nil {
			observation.Output = failure
			if reason, _ := failure["reason"].(string); reason != "" {
				observation.OutputSummary = reason
			}
		}
	}
	return observation
}

func structuredFailureFromError(message string) map[string]any {
	index := strings.Index(message, structuredFailureMarker)
	if index < 0 {
		return nil
	}
	raw := strings.TrimSpace(message[index+len(structuredFailureMarker):])
	if len(raw) == 0 || len(raw) > maximumObservationJSONBytes {
		return nil
	}
	var source map[string]any
	if json.Unmarshal([]byte(raw), &source) != nil {
		return nil
	}
	result := copyObservationFields(source,
		"task_id", "method", "attempt", "status", "failure_code", "reason", "retryable",
		"memory_peak_bytes", "memory_limit_bytes", "parameters")
	if result["status"] != "FAILED" || result["failure_code"] == "" {
		return nil
	}
	return result
}

func projectObservationOutput(capability string, output map[string]any) map[string]any {
	switch capability {
	case "literature.search":
		return projectSearchObservation(output)
	case "paper.parse":
		return projectPaperParseObservation(output)
	}
	encoded, err := json.Marshal(output)
	if err == nil && len(encoded) <= maximumObservationJSONBytes {
		return output
	}
	return projectGenericObservation(output)
}

func projectSearchObservation(output map[string]any) map[string]any {
	result := copyObservationFields(output,
		"query", "from_year", "to_year", "total_results", "provider", "cached", "completed_at")
	for _, raw := range outputArray(output["papers"]) {
		paper, _ := raw.(map[string]any)
		result["papers"] = appendOutputItem(result["papers"], copyObservationFields(paper,
			"id", "title", "authors", "year", "venue", "doi", "arxiv_id", "url", "pdf_url",
			"provider", "metadata_sources", "full_text_available"))
	}
	return result
}

func projectPaperParseObservation(output map[string]any) map[string]any {
	result := copyObservationFields(output,
		"paper", "query", "abstract", "parser", "fallback", "characters", "truncated", "diagnostics")
	delete(result, "abstract")
	pages := outputArray(output["pages"])
	result["page_count"] = len(pages)
	for _, raw := range pages {
		page, _ := raw.(map[string]any)
		item := copyObservationFields(page, "number", "start", "end")
		item["characters"] = len(stringValue(page["text"]))
		result["pages"] = appendOutputItem(result["pages"], item)
	}
	sections := outputArray(output["sections"])
	result["section_count"] = len(sections)
	for _, raw := range sections {
		section, _ := raw.(map[string]any)
		item := copyObservationFields(section,
			"id", "heading", "normalized_heading", "page_start", "page_end", "start", "end", "truncated")
		item["characters"] = len(stringValue(section["text"]))
		result["sections"] = appendOutputItem(result["sections"], item)
	}
	result["reference_count"] = len(outputArray(output["references"]))
	return result
}

func projectGenericObservation(output map[string]any) map[string]any {
	result := map[string]any{"observation_truncated": true}
	for key, value := range output {
		switch typed := value.(type) {
		case nil, bool, float64:
			result[key] = typed
		case string:
			if len(typed) <= maximumDependencySummary {
				result[key] = typed
			}
		case []any:
			result[key+"_count"] = len(typed)
		case map[string]any:
			encoded, err := json.Marshal(typed)
			if err == nil && len(encoded) <= maximumDependencySummary {
				result[key] = typed
			}
		}
	}
	return result
}

func copyObservationFields(source map[string]any, keys ...string) map[string]any {
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, exists := source[key]; exists {
			result[key] = value
		}
	}
	return result
}

func appendOutputItem(current any, item map[string]any) []any {
	items, _ := current.([]any)
	return append(items, item)
}

func outputArray(value any) []any {
	items, _ := value.([]any)
	return items
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func dependencySummaries(outputs map[string]agent.DependencyOutput) map[string]string {
	if len(outputs) == 0 {
		return nil
	}
	result := make(map[string]string, len(outputs))
	for id, output := range outputs {
		if !output.Verified || output.CommitPath == "" {
			result[id] = "dependency output was not verified"
			continue
		}
		data, err := readBoundedFile(filepath.Join(output.CommitPath, "result.txt"), maximumDependencySummary)
		if err != nil {
			result[id] = "verified dependency output available"
			continue
		}
		result[id] = strings.TrimSpace(string(data))
	}
	return result
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func cloneExitCode(source *int) *int {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
