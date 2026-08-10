package telemetry

import (
	"encoding/json"
	"fmt"
	"time"
)

// Kind identifies one Runtime event type.
type Kind string

const (
	KindPlanCreated           Kind = "cognitive.plan.created"
	KindObservationCreated    Kind = "cognitive.observation.created"
	KindDecisionMade          Kind = "cognitive.decision.made"
	KindReplanRequested       Kind = "cognitive.replan.requested"
	KindPlanRevised           Kind = "cognitive.plan.revised"
	KindGoalCompleted         Kind = "cognitive.goal.completed"
	KindAgentLoopAborted      Kind = "cognitive.loop.aborted"
	KindPaperParsed           Kind = "research.paper.parsed"
	KindPaperAnalysisStarted  Kind = "research.paper.analysis.started"
	KindPaperAnalysisDone     Kind = "research.paper.analysis.completed"
	KindCandidateFinding      Kind = "research.candidate_finding.created"
	KindEvidenceVerified      Kind = "research.evidence.verified"
	KindEvidenceRejected      Kind = "research.evidence.rejected"
	KindClaimSupported        Kind = "research.claim.supported"
	KindClaimUnsupported      Kind = "research.claim.unsupported"
	KindReportValidationStart Kind = "research.report.validation.started"
	KindReportValidationFail  Kind = "research.report.validation.failed"
	KindReportValidated       Kind = "research.report.validated"
	KindEvalCompleted         Kind = "research.eval.completed"
	KindOrchestrationStarted  Kind = "orchestrator.execution.started"
	KindOrchestrationFinished Kind = "orchestrator.execution.finished"

	KindAgentSubmitted  Kind = "runtime.agent.submitted"
	KindPressureSampled Kind = "runtime.pressure.sampled"
	KindAgentDispatched Kind = "runtime.agent.dispatched"
	KindAgentBlocked    Kind = "runtime.agent.blocked"

	KindOutputCommitted          Kind = "runtime.output.committed"
	KindOutputVerified           Kind = "runtime.output.verified"
	KindOutputVerificationFailed Kind = "runtime.output.verification_failed"

	KindAgentFinished Kind = "runtime.agent.finished"
)

// Event is the common observable record emitted by CAPSuleRT.
type Event struct {
	ID        string    `json:"id"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`

	Kind   Kind   `json:"kind"`
	Source string `json:"source"`

	AgentID string `json:"agent_id,omitempty"`
	Phase   string `json:"phase,omitempty"`

	Data json.RawMessage `json:"data,omitempty"`
}

// NewEvent creates an event whose payload is immutable JSON.
func NewEvent(
	kind Kind,
	source string,
	agentID string,
	phase string,
	payload any,
) (Event, error) {
	if kind == "" {
		return Event{}, fmt.Errorf("event kind is required")
	}

	if source == "" {
		source = "capsulert"
	}

	var data json.RawMessage

	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return Event{}, fmt.Errorf(
				"encode event payload: %w",
				err,
			)
		}

		data = append(json.RawMessage(nil), encoded...)
	}

	return Event{
		Kind:    kind,
		Source:  source,
		AgentID: agentID,
		Phase:   phase,
		Data:    data,
	}, nil
}

func cloneEvent(source Event) Event {
	result := source

	if source.Data != nil {
		result.Data = append(
			json.RawMessage(nil),
			source.Data...,
		)
	}

	return result
}
