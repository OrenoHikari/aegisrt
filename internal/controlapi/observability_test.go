package controlapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

func TestReadyEndpoint(t *testing.T) {
	api, err := New(
		fakeSchedulerView{
			status: scheduler.RuntimeStatus{
				Started:       true,
				Stopped:       false,
				WorkerCount:   2,
				QueueCapacity: 16,
				QueueDepth:    1,
			},
		},
		fakeEventView{},
		fakeBusView{
			stats: telemetry.BusStats{},
		},
	)
	if err != nil {
		t.Fatalf("create API: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/readyz",
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
}

func TestReadyEndpointRejectsStoppedScheduler(
	t *testing.T,
) {
	api, err := New(
		fakeSchedulerView{
			status: scheduler.RuntimeStatus{
				Started:       true,
				Stopped:       true,
				QueueCapacity: 16,
			},
		},
		fakeEventView{},
		fakeBusView{},
	)
	if err != nil {
		t.Fatalf("create API: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/readyz",
		nil,
	)

	response := httptest.NewRecorder()

	api.Handler().ServeHTTP(response, request)

	if response.Code !=
		http.StatusServiceUnavailable {
		t.Fatalf(
			"expected 503, got %d",
			response.Code,
		)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	api, err := New(
		fakeSchedulerView{
			status: scheduler.RuntimeStatus{
				Started:       true,
				WorkerCount:   2,
				QueueCapacity: 16,
				PhaseCounts: map[scheduler.Phase]int{
					scheduler.PhaseSucceeded: 3,
					scheduler.PhaseFailed:    1,
				},
			},
		},
		fakeEventView{},
		fakeBusView{
			stats: telemetry.BusStats{
				Published:    10,
				Delivered:    10,
				LastSequence: 10,
			},
		},
	)
	if err != nil {
		t.Fatalf("create API: %v", err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/metrics",
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

	body := response.Body.String()

	required := []string{
		"capsulert_up 1",
		"capsulert_ready 1",
		`capsulert_scheduler_agents{phase="SUCCEEDED"} 3`,
		`capsulert_scheduler_agents{phase="FAILED"} 1`,
		"capsulert_event_bus_published_total 10",
		"capsulert_event_sequence 10",
	}

	for _, value := range required {
		if !strings.Contains(body, value) {
			t.Fatalf(
				"metric %q is missing:\n%s",
				value,
				body,
			)
		}
	}
}
