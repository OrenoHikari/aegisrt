package controlapi

import (
	"fmt"
	"net/http"
	"time"

	"aegisrt/internal/scheduler"
)

type readinessState struct {
	Ready     bool                    `json:"ready"`
	Timestamp time.Time               `json:"timestamp"`
	Reasons   []string                `json:"reasons,omitempty"`
	Scheduler scheduler.RuntimeStatus `json:"scheduler"`
}

func (a *API) handleReady(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	state := a.readiness()

	statusCode := http.StatusOK
	if !state.Ready {
		statusCode = http.StatusServiceUnavailable
	}

	writeJSON(writer, statusCode, state)
}

func (a *API) readiness() readinessState {
	status := a.scheduler.Status()
	reasons := make([]string, 0)

	if !status.Started {
		reasons = append(
			reasons,
			"scheduler_not_started",
		)
	}

	if status.Stopped {
		reasons = append(
			reasons,
			"scheduler_stopped",
		)
	}

	if status.QueueCapacity > 0 &&
		status.QueueDepth >= status.QueueCapacity {
		reasons = append(
			reasons,
			"scheduler_queue_full",
		)
	}

	if a.bus != nil {
		busStats := a.bus.Stats()

		if busStats.SinkErrors > 0 {
			reasons = append(
				reasons,
				"event_sink_errors",
			)
		}
	}

	return readinessState{
		Ready:     len(reasons) == 0,
		Timestamp: time.Now().UTC(),
		Reasons:   reasons,
		Scheduler: status,
	}
}

func (a *API) handleMetrics(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !requireGET(writer, request) {
		return
	}

	writer.Header().Set(
		"Content-Type",
		"text/plain; version=0.0.4; charset=utf-8",
	)

	status := a.scheduler.Status()
	ready := a.readiness().Ready

	fmt.Fprintln(
		writer,
		"# HELP capsulert_up Whether the CAPSuleRT HTTP process is alive.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_up gauge",
	)
	fmt.Fprintln(writer, "capsulert_up 1")

	fmt.Fprintln(
		writer,
		"# HELP capsulert_ready Whether the Runtime is ready to accept work.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_ready gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_ready %d\n",
		boolMetric(ready),
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_runtime_uptime_seconds Runtime API uptime.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_runtime_uptime_seconds gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_runtime_uptime_seconds %.6f\n",
		time.Since(a.startedAt).Seconds(),
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_scheduler_started Whether the Scheduler has started.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_scheduler_started gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_scheduler_started %d\n",
		boolMetric(status.Started),
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_scheduler_stopped Whether the Scheduler has stopped.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_scheduler_stopped gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_scheduler_stopped %d\n",
		boolMetric(status.Stopped),
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_scheduler_workers Configured Scheduler workers.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_scheduler_workers gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_scheduler_workers %d\n",
		status.WorkerCount,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_scheduler_queue_depth Current queued Agent count.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_scheduler_queue_depth gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_scheduler_queue_depth %d\n",
		status.QueueDepth,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_scheduler_queue_capacity Maximum waiting queue size.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_scheduler_queue_capacity gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_scheduler_queue_capacity %d\n",
		status.QueueCapacity,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_scheduler_agents Number of Agents by phase.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_scheduler_agents gauge",
	)

	phases := []scheduler.Phase{
		scheduler.PhaseQueued,
		scheduler.PhaseRunning,
		scheduler.PhaseSucceeded,
		scheduler.PhaseFailed,
		scheduler.PhaseBlocked,
	}

	for _, phase := range phases {
		fmt.Fprintf(
			writer,
			"capsulert_scheduler_agents{phase=%q} %d\n",
			string(phase),
			status.PhaseCounts[phase],
		)
	}

	if a.bus == nil {
		return
	}

	busStats := a.bus.Stats()

	fmt.Fprintln(
		writer,
		"# HELP capsulert_event_bus_published_total Events accepted by the bus.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_event_bus_published_total counter",
	)
	fmt.Fprintf(
		writer,
		"capsulert_event_bus_published_total %d\n",
		busStats.Published,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_event_bus_delivered_total Events delivered to sinks.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_event_bus_delivered_total counter",
	)
	fmt.Fprintf(
		writer,
		"capsulert_event_bus_delivered_total %d\n",
		busStats.Delivered,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_event_bus_sink_errors_total Event sink failures.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_event_bus_sink_errors_total counter",
	)
	fmt.Fprintf(
		writer,
		"capsulert_event_bus_sink_errors_total %d\n",
		busStats.SinkErrors,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_event_bus_queue_depth Current event queue depth.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_event_bus_queue_depth gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_event_bus_queue_depth %d\n",
		busStats.QueueDepth,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_event_bus_queue_capacity Event queue capacity.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_event_bus_queue_capacity gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_event_bus_queue_capacity %d\n",
		busStats.QueueCapacity,
	)

	fmt.Fprintln(
		writer,
		"# HELP capsulert_event_sequence Latest allocated event sequence.",
	)
	fmt.Fprintln(
		writer,
		"# TYPE capsulert_event_sequence gauge",
	)
	fmt.Fprintf(
		writer,
		"capsulert_event_sequence %d\n",
		busStats.LastSequence,
	)
}

func boolMetric(value bool) int {
	if value {
		return 1
	}

	return 0
}
