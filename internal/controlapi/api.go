package controlapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aegisrt/internal/scheduler"
	"aegisrt/internal/telemetry"
)

const (
	defaultLimit = 200
	maximumLimit = 1000
)

// SchedulerView exposes immutable Runtime scheduling state.
type SchedulerView interface {
	Snapshot() []scheduler.Record
	Record(agentID string) (scheduler.Record, bool)
	Status() scheduler.RuntimeStatus
}

// EventView exposes retained Runtime events.
type EventView interface {
	Snapshot() []telemetry.Event
	Since(sequence uint64) []telemetry.Event
}

// BusView exposes event-pipeline status.
type BusView interface {
	Stats() telemetry.BusStats
}

// API implements the AegisRT HTTP query plane.
type API struct {
	scheduler SchedulerView
	events    EventView
	bus       BusView
	startedAt time.Time
	handler   http.Handler
}

// New creates a Runtime query API.
func New(
	schedulerView SchedulerView,
	eventView EventView,
	busView BusView,
) (*API, error) {
	if schedulerView == nil {
		return nil, fmt.Errorf(
			"Scheduler view is required",
		)
	}

	if eventView == nil {
		return nil, fmt.Errorf(
			"event view is required",
		)
	}

	api := &API{
		scheduler: schedulerView,
		events:    eventView,
		bus:       busView,
		startedAt: time.Now().UTC(),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", api.handleHealth)
	mux.HandleFunc(
		"/v1/runtime/status",
		api.handleRuntimeStatus,
	)
	mux.HandleFunc("/v1/agents", api.handleAgents)
	mux.HandleFunc("/v1/agents/", api.handleAgent)
	mux.HandleFunc("/v1/events", api.handleEvents)

	api.handler = requestMiddleware(mux)

	return api, nil
}

// Handler returns the HTTP handler.
func (a *API) Handler() http.Handler {
	return a.handler
}

func (a *API) handleHealth(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		map[string]any{
			"status":    "ok",
			"timestamp": time.Now().UTC(),
		},
	)
}

func (a *API) handleRuntimeStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	response := map[string]any{
		"timestamp":      time.Now().UTC(),
		"started_at":     a.startedAt,
		"uptime_seconds": time.Since(a.startedAt).Seconds(),
		"scheduler":      a.scheduler.Status(),
	}

	if a.bus != nil {
		response["event_bus"] = a.bus.Stats()
	}

	writeJSON(
		writer,
		http.StatusOK,
		response,
	)
}

func (a *API) handleAgents(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	limit, err := parseLimit(
		request.URL.Query().Get("limit"),
	)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
			err.Error(),
		)
		return
	}

	phaseFilter := strings.ToUpper(
		strings.TrimSpace(
			request.URL.Query().Get("phase"),
		),
	)

	if phaseFilter != "" &&
		!validPhase(scheduler.Phase(phaseFilter)) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_phase",
			fmt.Sprintf(
				"unsupported Agent phase %q",
				phaseFilter,
			),
		)
		return
	}

	records := a.scheduler.Snapshot()
	filtered := make(
		[]scheduler.Record,
		0,
		len(records),
	)

	for _, record := range records {
		if phaseFilter != "" &&
			string(record.Phase) != phaseFilter {
			continue
		}

		filtered = append(filtered, record)
	}

	total := len(filtered)

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	writeJSON(
		writer,
		http.StatusOK,
		map[string]any{
			"items": filtered,
			"count": len(filtered),
			"total": total,
		},
	)
}

func (a *API) handleAgent(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	encodedID := strings.TrimPrefix(
		request.URL.Path,
		"/v1/agents/",
	)

	if encodedID == "" ||
		strings.Contains(encodedID, "/") {
		writeError(
			writer,
			http.StatusNotFound,
			"agent_not_found",
			"Agent was not found",
		)
		return
	}

	agentID, err := url.PathUnescape(encodedID)
	if err != nil || strings.TrimSpace(agentID) == "" {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_agent_id",
			"Agent ID is invalid",
		)
		return
	}

	record, exists := a.scheduler.Record(agentID)
	if !exists {
		writeError(
			writer,
			http.StatusNotFound,
			"agent_not_found",
			fmt.Sprintf(
				"Agent %q was not found",
				agentID,
			),
		)
		return
	}

	writeJSON(
		writer,
		http.StatusOK,
		record,
	)
}

func (a *API) handleEvents(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	query := request.URL.Query()

	since, err := parseUintQuery(
		query.Get("since"),
		"since",
	)
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_since",
			err.Error(),
		)
		return
	}

	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_limit",
			err.Error(),
		)
		return
	}

	kindFilter := strings.TrimSpace(
		query.Get("kind"),
	)

	agentFilter := strings.TrimSpace(
		query.Get("agent_id"),
	)

	phaseFilter := strings.ToUpper(
		strings.TrimSpace(query.Get("phase")),
	)

	var events []telemetry.Event

	if since > 0 {
		events = a.events.Since(since)
	} else {
		events = a.events.Snapshot()
	}

	filtered := make(
		[]telemetry.Event,
		0,
		len(events),
	)

	for _, event := range events {
		if kindFilter != "" &&
			string(event.Kind) != kindFilter {
			continue
		}

		if agentFilter != "" &&
			event.AgentID != agentFilter {
			continue
		}

		if phaseFilter != "" &&
			event.Phase != phaseFilter {
			continue
		}

		filtered = append(filtered, event)
	}

	total := len(filtered)

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	var nextSequence uint64

	if len(filtered) > 0 {
		nextSequence =
			filtered[len(filtered)-1].Sequence
	}

	writeJSON(
		writer,
		http.StatusOK,
		map[string]any{
			"items":         filtered,
			"count":         len(filtered),
			"total":         total,
			"since":         since,
			"next_sequence": nextSequence,
		},
	)
}

func parseLimit(value string) (int, error) {
	value = strings.TrimSpace(value)

	if value == "" {
		return defaultLimit, nil
	}

	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf(
			"limit must be an integer",
		)
	}

	if limit <= 0 || limit > maximumLimit {
		return 0, fmt.Errorf(
			"limit must be between 1 and %d",
			maximumLimit,
		)
	}

	return limit, nil
}

func parseUintQuery(
	value string,
	name string,
) (uint64, error) {
	value = strings.TrimSpace(value)

	if value == "" {
		return 0, nil
	}

	parsed, err := strconv.ParseUint(
		value,
		10,
		64,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"%s must be an unsigned integer",
			name,
		)
	}

	return parsed, nil
}

func validPhase(phase scheduler.Phase) bool {
	switch phase {
	case scheduler.PhaseQueued,
		scheduler.PhaseRunning,
		scheduler.PhaseSucceeded,
		scheduler.PhaseFailed,
		scheduler.PhaseBlocked:
		return true

	default:
		return false
	}
}

func requireGET(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.Method == http.MethodGet {
		return true
	}

	writer.Header().Set("Allow", http.MethodGet)

	writeError(
		writer,
		http.StatusMethodNotAllowed,
		"method_not_allowed",
		"only GET is supported",
	)

	return false
}

func requestMiddleware(
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set(
			"Content-Type",
			"application/json; charset=utf-8",
		)

		writer.Header().Set(
			"Cache-Control",
			"no-store",
		)

		writer.Header().Set(
			"X-Content-Type-Options",
			"nosniff",
		)

		next.ServeHTTP(writer, request)
	})
}

func writeError(
	writer http.ResponseWriter,
	status int,
	code string,
	message string,
) {
	writeJSON(
		writer,
		status,
		map[string]any{
			"error": map[string]string{
				"code":    code,
				"message": message,
			},
		},
	)
}

func writeJSON(
	writer http.ResponseWriter,
	status int,
	payload any,
) {
	writer.WriteHeader(status)

	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)

	_ = encoder.Encode(payload)
}
