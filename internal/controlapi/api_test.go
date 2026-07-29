package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

type fakeSchedulerView struct {
	records []scheduler.Record
	status  scheduler.RuntimeStatus
}

func (f fakeSchedulerView) Snapshot() []scheduler.Record {
	return append([]scheduler.Record(nil), f.records...)
}

func (f fakeSchedulerView) Record(
	agentID string,
) (scheduler.Record, bool) {
	for _, record := range f.records {
		if record.ID == agentID {
			return record, true
		}
	}

	return scheduler.Record{}, false
}

func (f fakeSchedulerView) Status() scheduler.RuntimeStatus {
	return f.status
}

type fakeEventView struct {
	events []telemetry.Event
}

func (f fakeEventView) Snapshot() []telemetry.Event {
	return append([]telemetry.Event(nil), f.events...)
}

func (f fakeEventView) Since(
	sequence uint64,
) []telemetry.Event {
	result := make([]telemetry.Event, 0)

	for _, event := range f.events {
		if event.Sequence > sequence {
			result = append(result, event)
		}
	}

	return result
}

type fakeBusView struct {
	stats telemetry.BusStats
}

func (f fakeBusView) Stats() telemetry.BusStats {
	return f.stats
}

func newTestAPI(t *testing.T) *API {
	t.Helper()

	records := []scheduler.Record{
		{
			ID:             "agent-success",
			Role:           "producer",
			Phase:          scheduler.PhaseSucceeded,
			SubmittedAt:    time.Now().UTC(),
			OutputVerified: true,
		},
		{
			ID:          "agent-failed",
			Role:        "producer",
			Phase:       scheduler.PhaseFailed,
			SubmittedAt: time.Now().UTC(),
			Error:       "simulated failure",
		},
	}

	events := []telemetry.Event{
		{
			ID:        "evt-1",
			Sequence:  1,
			Timestamp: time.Now().UTC(),
			Kind:      telemetry.KindAgentSubmitted,
			AgentID:   "agent-success",
			Phase:     "QUEUED",
		},
		{
			ID:        "evt-2",
			Sequence:  2,
			Timestamp: time.Now().UTC(),
			Kind:      telemetry.KindAgentFinished,
			AgentID:   "agent-success",
			Phase:     "SUCCEEDED",
		},
	}

	api, err := New(
		fakeSchedulerView{
			records: records,
			status: scheduler.RuntimeStatus{
				Started:     true,
				Stopped:     false,
				WorkerCount: 2,
				TotalAgents: 2,
			},
		},
		fakeEventView{events: events},
		fakeBusView{
			stats: telemetry.BusStats{
				Published: 2,
				Delivered: 2,
			},
		},
	)
	if err != nil {
		t.Fatalf("create API: %v", err)
	}

	return api
}

func TestListAgentsByPhase(t *testing.T) {
	api := newTestAPI(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/agents?phase=SUCCEEDED",
		nil,
	)

	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"expected 200, got %d: %s",
			response.Code,
			response.Body.String(),
		)
	}

	var payload struct {
		Items []scheduler.Record `json:"items"`
	}

	if err := json.Unmarshal(
		response.Body.Bytes(),
		&payload,
	); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(payload.Items) != 1 ||
		payload.Items[0].ID != "agent-success" {
		t.Fatalf(
			"unexpected Agent response: %+v",
			payload.Items,
		)
	}
}

func TestGetAgent(t *testing.T) {
	api := newTestAPI(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/agents/agent-success",
		nil,
	)

	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"expected 200, got %d",
			response.Code,
		)
	}

	var record scheduler.Record

	if err := json.Unmarshal(
		response.Body.Bytes(),
		&record,
	); err != nil {
		t.Fatalf("decode Agent: %v", err)
	}

	if record.ID != "agent-success" {
		t.Fatalf(
			"unexpected Agent ID %q",
			record.ID,
		)
	}
}

func TestEventsSinceSequence(t *testing.T) {
	api := newTestAPI(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/events?since=1",
		nil,
	)

	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"expected 200, got %d",
			response.Code,
		)
	}

	var payload struct {
		Items []telemetry.Event `json:"items"`
	}

	if err := json.Unmarshal(
		response.Body.Bytes(),
		&payload,
	); err != nil {
		t.Fatalf("decode events: %v", err)
	}

	if len(payload.Items) != 1 ||
		payload.Items[0].Sequence != 2 {
		t.Fatalf(
			"unexpected events: %+v",
			payload.Items,
		)
	}
}

func TestUnknownAgentReturns404(t *testing.T) {
	api := newTestAPI(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/agents/missing-agent",
		nil,
	)

	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf(
			"expected 404, got %d",
			response.Code,
		)
	}
}

func TestRuntimeStatus(t *testing.T) {
	api := newTestAPI(t)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/runtime/status",
		nil,
	)

	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf(
			"expected 200, got %d",
			response.Code,
		)
	}
}
