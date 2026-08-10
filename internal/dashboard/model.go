// Package dashboard exposes a small local competition control layer over the
// existing CAPSuleAgent CLI, telemetry, Scheduler, and verified run artifacts.
// It does not schedule or execute research tasks itself.
package dashboard

import (
	"context"
	"encoding/json"
	"time"

	"aegisrt/internal/experiment"
	"aegisrt/internal/research"
)

type RunStatus string

const (
	StatusIdle         RunStatus = "IDLE"
	StatusPlanning     RunStatus = "PLANNING"
	StatusRunning      RunStatus = "RUNNING"
	StatusObserving    RunStatus = "OBSERVING"
	StatusReplanning   RunStatus = "REPLANNING"
	StatusSynthesizing RunStatus = "SYNTHESIZING"
	StatusCompleted    RunStatus = "COMPLETED"
	StatusFailed       RunStatus = "FAILED"
	StatusCancelled    RunStatus = "CANCELLED"
)

func (s RunStatus) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

type CreateRunRequest struct {
	Goal                string `json:"goal"`
	Workload            string `json:"workload,omitempty"`
	Mode                string `json:"mode,omitempty"`
	Scenario            string `json:"scenario,omitempty"`
	ExperimentDirectory string `json:"experiment_directory,omitempty"`
	MaxPDFMB            int    `json:"max_pdf_mb,omitempty"`
}

type RunRecord struct {
	ID                  string                 `json:"id"`
	CognitiveRunID      string                 `json:"cognitive_run_id,omitempty"`
	Goal                string                 `json:"goal"`
	Workload            string                 `json:"workload"`
	Mode                string                 `json:"mode"`
	Scenario            string                 `json:"scenario,omitempty"`
	ExperimentDirectory string                 `json:"experiment_directory,omitempty"`
	MaxPDFMB            int                    `json:"max_pdf_mb"`
	Status              RunStatus              `json:"status"`
	CreatedAt           time.Time              `json:"created_at"`
	StartedAt           *time.Time             `json:"started_at,omitempty"`
	FinishedAt          *time.Time             `json:"finished_at,omitempty"`
	DurationMS          int64                  `json:"duration_ms"`
	Root                string                 `json:"-"`
	Error               string                 `json:"error,omitempty"`
	Summary             *research.RunSummary   `json:"summary,omitempty"`
	Experiment          *experiment.RunSummary `json:"experiment,omitempty"`
}

type RunView struct {
	RunRecord
	Runtime  RuntimeSnapshot  `json:"runtime"`
	Progress ArtifactProgress `json:"progress"`
	Decision DecisionView     `json:"decision"`
	Failures []FailureView    `json:"failures,omitempty"`
}

type Run struct {
	RunRecord
	Events *EventStore
	cancel context.CancelFunc
}

type PlanTaskView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Capability string   `json:"capability"`
	DependsOn  []string `json:"depends_on"`
	Status     string   `json:"status"`
	Change     string   `json:"change"`
}

type PlanVersionView struct {
	Version int            `json:"version"`
	Tasks   []PlanTaskView `json:"tasks"`
	Removed []string       `json:"removed,omitempty"`
}

type PlanSnapshot struct {
	Versions []PlanVersionView `json:"versions"`
}

type RuntimeSnapshot struct {
	ActiveAgents        int      `json:"active_agents"`
	RunningTasks        int      `json:"running_tasks"`
	SchedulerQueue      int      `json:"scheduler_queue"`
	SucceededTasks      int      `json:"succeeded_tasks"`
	FailedTasks         int      `json:"failed_tasks"`
	ScheduledTasks      int      `json:"scheduled_tasks"`
	ExecutedTasks       int      `json:"executed_tasks"`
	PeakParallelAgents  int      `json:"peak_parallel_agents"`
	ParallelWorkMS      int64    `json:"parallel_work_ms"`
	ParallelWindowMS    int64    `json:"parallel_window_ms"`
	ParallelSavedMS     int64    `json:"parallel_saved_ms"`
	AverageParallelism  float64  `json:"average_parallelism"`
	CPUPressureAvg10    *float64 `json:"cpu_pressure_avg10"`
	MemoryPressureAvg10 *float64 `json:"memory_pressure_avg10"`
	PeakCPUPressure     *float64 `json:"peak_cpu_pressure"`
	PeakMemoryPressure  *float64 `json:"peak_memory_pressure"`
	PressureAvailable   bool     `json:"pressure_available"`
	CPUQuotaPercent     int      `json:"cpu_quota_percent"`
	MemoryMaxBytes      int64    `json:"memory_max_bytes"`
	PIDsMax             int      `json:"pids_max"`
	CgroupIsolated      bool     `json:"cgroup_isolated"`
}

type DecisionView struct {
	Type                     string              `json:"type,omitempty"`
	Reason                   string              `json:"reason,omitempty"`
	ObservationSummary       string              `json:"observation_summary,omitempty"`
	Action                   string              `json:"action,omitempty"`
	Iteration                int                 `json:"iteration,omitempty"`
	FromPlan                 int                 `json:"from_plan,omitempty"`
	ToPlan                   int                 `json:"to_plan,omitempty"`
	ReplanReason             string              `json:"replan_reason,omitempty"`
	ReplanObservationSummary string              `json:"replan_observation_summary,omitempty"`
	ReplanAction             string              `json:"replan_action,omitempty"`
	History                  []DecisionEntryView `json:"history,omitempty"`
}

type DecisionEntryView struct {
	Type               string `json:"type"`
	Reason             string `json:"reason"`
	ObservationSummary string `json:"observation_summary,omitempty"`
	Action             string `json:"action,omitempty"`
	Iteration          int    `json:"iteration"`
}

type FailureView struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	TaskID     string `json:"task_id,omitempty"`
	Capability string `json:"capability,omitempty"`
	Recovered  bool   `json:"recovered"`
}

type ArtifactProgress struct {
	SearchQueries      int   `json:"search_queries"`
	RetrievedPapers    int   `json:"retrieved_papers"`
	DeduplicatedPapers int   `json:"deduplicated_papers"`
	PDFsAvailable      int   `json:"pdfs_available"`
	ParsedPapers       int   `json:"parsed_papers"`
	AnalyzedPapers     int   `json:"analyzed_papers"`
	Replans            int   `json:"replans"`
	LLMCalls           int   `json:"llm_calls"`
	InputTokens        *int  `json:"input_tokens"`
	OutputTokens       *int  `json:"output_tokens"`
	CandidateFindings  int   `json:"candidate_findings"`
	SourceVerified     int   `json:"source_verified"`
	SupportedFindings  int   `json:"supported_findings"`
	RejectedFindings   int   `json:"rejected_findings"`
	CitationClosure    *bool `json:"citation_closure"`
	DurationMS         int64 `json:"duration_ms"`
}

type PaperView struct {
	ID          string                      `json:"id"`
	Title       string                      `json:"title"`
	Authors     []string                    `json:"authors"`
	Year        int                         `json:"year"`
	Source      string                      `json:"source"`
	Reference   string                      `json:"reference,omitempty"`
	Abstract    string                      `json:"abstract,omitempty"`
	Status      string                      `json:"status"`
	Sections    []SectionView               `json:"sections,omitempty"`
	Diagnostics *research.ParserDiagnostics `json:"diagnostics,omitempty"`
}

type SectionView struct {
	ID         string `json:"id"`
	Heading    string `json:"heading"`
	PageStart  int    `json:"page_start"`
	PageEnd    int    `json:"page_end"`
	Characters int    `json:"characters"`
}

type EvidenceFindingView struct {
	Status     string `json:"status"`
	Claim      string `json:"claim"`
	ClaimType  string `json:"claim_type,omitempty"`
	PaperID    string `json:"paper_id"`
	PaperTitle string `json:"paper_title,omitempty"`
	Section    string `json:"section,omitempty"`
	SectionID  string `json:"section_id,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	Reason     string `json:"reason,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
	EvidenceID string `json:"evidence_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
}

type ResultFindingView struct {
	Kind        string   `json:"kind"`
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

type EvidenceSnapshot struct {
	CandidateCount      int                   `json:"candidate_count"`
	SourceVerifiedCount int                   `json:"source_verified_count"`
	SupportedCount      int                   `json:"supported_count"`
	RejectedCount       int                   `json:"rejected_count"`
	Findings            []EvidenceFindingView `json:"findings"`
	Facts               []ResultFindingView   `json:"facts"`
	Inferences          []ResultFindingView   `json:"inferences"`
	Proposals           []ResultFindingView   `json:"proposals"`
}

type ArtifactSnapshot struct {
	Progress ArtifactProgress         `json:"progress"`
	Papers   []PaperView              `json:"papers"`
	Evidence EvidenceSnapshot         `json:"evidence"`
	Queries  []string                 `json:"query_history,omitempty"`
	Quality  research.ResearchQuality `json:"quality"`
}

type DashboardEvent struct {
	DashboardRunID string         `json:"dashboard_run_id"`
	ID             string         `json:"id"`
	Sequence       uint64         `json:"sequence"`
	Timestamp      time.Time      `json:"timestamp"`
	Kind           string         `json:"kind"`
	Source         string         `json:"source"`
	TaskID         string         `json:"task_id,omitempty"`
	Phase          string         `json:"phase,omitempty"`
	Data           map[string]any `json:"data,omitempty"`
}

type SystemStatus struct {
	RuntimeOnline     bool         `json:"runtime_online"`
	LLMConfigured     bool         `json:"llm_configured"`
	ProviderAvailable bool         `json:"provider_available"`
	DefaultMode       string       `json:"default_mode"`
	PresetGoal        string       `json:"preset_goal"`
	Presets           []DemoPreset `json:"presets"`
	DefaultMaxPDFMB   int          `json:"default_max_pdf_mb"`
	MaximumMaxPDFMB   int          `json:"maximum_max_pdf_mb"`
}

type DemoPreset struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description"`
	Goal                string `json:"goal"`
	Workload            string `json:"workload,omitempty"`
	Mode                string `json:"mode"`
	Scenario            string `json:"scenario,omitempty"`
	ExperimentDirectory string `json:"experiment_directory,omitempty"`
}

type APIError struct {
	Error string `json:"error"`
}

func decodeMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}
