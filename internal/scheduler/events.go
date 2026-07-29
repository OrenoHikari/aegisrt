package scheduler

import (
	"context"
	"time"

	"aegisrt/internal/telemetry"
)

// EventPublisher accepts unified Runtime events.
type EventPublisher interface {
	Publish(
		ctx context.Context,
		event telemetry.Event,
	) error
}

func defaultEventPublisher() EventPublisher {
	return telemetry.NopPublisher{}
}

func (s *Scheduler) emitSubmitted(
	record *Record,
) {
	s.emitRecord(
		telemetry.KindAgentSubmitted,
		record,
	)
}

func (s *Scheduler) emitDispatched(
	record *Record,
) {
	if record == nil {
		return
	}

	pressurePayload := map[string]any{
		"snapshot":       record.Pressure,
		"sample_error":   record.PressureError,
		"dispatch_score": record.DispatchScore,
		"agent_demand":   record.Demand,
	}

	s.emitPayload(
		telemetry.KindPressureSampled,
		record.ID,
		string(record.Phase),
		pressurePayload,
	)

	s.emitRecord(
		telemetry.KindAgentDispatched,
		record,
	)
}

func (s *Scheduler) emitBlocked(
	record *Record,
) {
	s.emitRecord(
		telemetry.KindAgentBlocked,
		record,
	)
}

func (s *Scheduler) emitFinished(
	record *Record,
) {
	if record == nil {
		return
	}

	if record.OutputCommitted {
		s.emitRecord(
			telemetry.KindOutputCommitted,
			record,
		)
	}

	if record.OutputVerified {
		s.emitRecord(
			telemetry.KindOutputVerified,
			record,
		)
	} else if record.OutputVerificationError != "" {
		s.emitRecord(
			telemetry.KindOutputVerificationFailed,
			record,
		)
	}

	s.emitRecord(
		telemetry.KindAgentFinished,
		record,
	)
}

func (s *Scheduler) emitRecord(
	kind telemetry.Kind,
	record *Record,
) {
	if record == nil {
		return
	}

	s.emitPayload(
		kind,
		record.ID,
		string(record.Phase),
		cloneRecord(record),
	)
}

func (s *Scheduler) emitPayload(
	kind telemetry.Kind,
	agentID string,
	phase string,
	payload any,
) {
	if s.events == nil {
		return
	}

	event, err := telemetry.NewEvent(
		kind,
		"scheduler",
		agentID,
		phase,
		payload,
	)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		2*time.Second,
	)
	defer cancel()

	_ = s.events.Publish(ctx, event)
}
