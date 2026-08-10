package dashboard

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BuildRuntimeSnapshot projects Scheduler events into a compact live view. The
// pressure values are Linux PSI stall percentages, not CPU or RAM utilization.
func BuildRuntimeSnapshot(events []DashboardEvent) RuntimeSnapshot {
	phases := make(map[string]string)
	started := make(map[string]time.Time)
	running := make(map[string]struct{})
	intervals := make([]taskInterval, 0)
	var result RuntimeSnapshot
	for _, event := range events {
		switch event.Kind {
		case "runtime.agent.submitted", "runtime.agent.dispatched", "runtime.agent.finished", "runtime.agent.blocked":
			if event.TaskID != "" {
				phase := strings.ToUpper(event.Phase)
				if phase == "" {
					phase, _ = event.Data["phase"].(string)
					phase = strings.ToUpper(phase)
				}
				phases[event.TaskID] = phase
			}
			if spec, ok := event.Data["resource_spec"].(map[string]any); ok {
				if value := integerValue(spec["cpu_quota_percent"]); value > result.CPUQuotaPercent {
					result.CPUQuotaPercent = value
				}
				if value := int64(integerValue(spec["memory_max_bytes"])); value > result.MemoryMaxBytes {
					result.MemoryMaxBytes = value
				}
				if value := integerValue(spec["pids_max"]); value > result.PIDsMax {
					result.PIDsMax = value
				}
			}
			if path, _ := event.Data["cgroup_path"].(string); strings.TrimSpace(path) != "" {
				result.CgroupIsolated = true
			}
			key := runtimeTaskKey(event)
			switch event.Kind {
			case "runtime.agent.dispatched":
				if !event.Timestamp.IsZero() {
					started[key] = event.Timestamp
				}
				running[key] = struct{}{}
				if len(running) > result.PeakParallelAgents {
					result.PeakParallelAgents = len(running)
				}
			case "runtime.agent.finished", "runtime.agent.blocked":
				if began, exists := started[key]; exists && event.Timestamp.After(began) {
					intervals = append(intervals, taskInterval{start: began, end: event.Timestamp})
				}
				delete(started, key)
				delete(running, key)
			}
		case "runtime.pressure.sampled":
			snapshot, _ := event.Data["snapshot"].(map[string]any)
			if snapshot == nil {
				continue
			}
			if value, ok := nestedFloat(snapshot, "cpu", "some", "avg10"); ok {
				result.CPUPressureAvg10 = floatPointer(value)
				result.PeakCPUPressure = maximumFloatPointer(result.PeakCPUPressure, value)
				result.PressureAvailable = true
			}
			if value, ok := nestedFloat(snapshot, "memory", "some", "avg10"); ok {
				result.MemoryPressureAvg10 = floatPointer(value)
				result.PeakMemoryPressure = maximumFloatPointer(result.PeakMemoryPressure, value)
				result.PressureAvailable = true
			}
		}
	}
	for _, phase := range phases {
		switch phase {
		case "QUEUED":
			result.SchedulerQueue++
		case "RUNNING":
			result.RunningTasks++
		case "SUCCEEDED":
			result.SucceededTasks++
		case "FAILED", "BLOCKED":
			result.FailedTasks++
		}
	}
	result.ActiveAgents = result.SchedulerQueue + result.RunningTasks
	result.ScheduledTasks = len(phases)
	result.ExecutedTasks = len(intervals)
	result.ParallelWorkMS, result.ParallelWindowMS = intervalDurations(intervals)
	if result.ParallelWorkMS > result.ParallelWindowMS {
		result.ParallelSavedMS = result.ParallelWorkMS - result.ParallelWindowMS
	}
	if result.ParallelWindowMS > 0 {
		result.AverageParallelism = float64(result.ParallelWorkMS) / float64(result.ParallelWindowMS)
	}
	return result
}

type taskInterval struct {
	start time.Time
	end   time.Time
}

func runtimeTaskKey(event DashboardEvent) string {
	return strconv.Itoa(eventIteration(event)) + "\x00" + event.TaskID
}

func intervalDurations(intervals []taskInterval) (int64, int64) {
	if len(intervals) == 0 {
		return 0, 0
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start.Before(intervals[j].start) })
	var work time.Duration
	for _, interval := range intervals {
		work += interval.end.Sub(interval.start)
	}
	unionStart, unionEnd := intervals[0].start, intervals[0].end
	var activeWindow time.Duration
	for _, interval := range intervals[1:] {
		if !interval.start.After(unionEnd) {
			if interval.end.After(unionEnd) {
				unionEnd = interval.end
			}
			continue
		}
		activeWindow += unionEnd.Sub(unionStart)
		unionStart, unionEnd = interval.start, interval.end
	}
	activeWindow += unionEnd.Sub(unionStart)
	return work.Milliseconds(), activeWindow.Milliseconds()
}

func maximumFloatPointer(current *float64, candidate float64) *float64 {
	if current == nil || candidate > *current {
		return floatPointer(candidate)
	}
	return current
}

// BuildPlanSnapshot reconstructs sanitized plan versions from cognitive
// telemetry, then overlays the corresponding real Scheduler task states.
func BuildPlanSnapshot(events []DashboardEvent) PlanSnapshot {
	statuses := make(map[int]map[string]string)
	versions := make([]PlanVersionView, 0)
	for _, event := range events {
		iteration := eventIteration(event)
		if iteration > 0 && event.TaskID != "" {
			if statuses[iteration] == nil {
				statuses[iteration] = make(map[string]string)
			}
			switch event.Kind {
			case "runtime.agent.submitted":
				statuses[iteration][event.TaskID] = "PENDING"
			case "runtime.agent.dispatched":
				statuses[iteration][event.TaskID] = "RUNNING"
			case "runtime.agent.finished":
				if strings.EqualFold(event.Phase, "SUCCEEDED") {
					statuses[iteration][event.TaskID] = "COMPLETED"
				} else {
					statuses[iteration][event.TaskID] = "FAILED"
				}
			case "runtime.agent.blocked":
				statuses[iteration][event.TaskID] = "FAILED"
			case "cognitive.observation.created":
				if reused, _ := event.Data["reused"].(bool); reused {
					statuses[iteration][event.TaskID] = "REUSED"
				} else if success, _ := event.Data["success"].(bool); success {
					statuses[iteration][event.TaskID] = "COMPLETED"
				} else {
					statuses[iteration][event.TaskID] = "FAILED"
				}
			}
		}

		if event.Kind != "cognitive.plan.created" && event.Kind != "cognitive.plan.revised" {
			continue
		}
		version := integerValue(event.Data["version"])
		if version <= 0 {
			version = len(versions) + 1
		}
		view := PlanVersionView{Version: version}
		for _, raw := range anySlice(event.Data["plan_tasks"]) {
			taskMap, _ := raw.(map[string]any)
			id, _ := taskMap["id"].(string)
			if id == "" {
				continue
			}
			view.Tasks = append(view.Tasks, PlanTaskView{
				ID: id, Name: stringValue(taskMap["name"]), Capability: stringValue(taskMap["capability"]),
				DependsOn: stringSlice(taskMap["depends_on"]), Status: "PENDING", Change: "NEW",
			})
		}
		versions = append(versions, view)
	}

	for index := range versions {
		current := &versions[index]
		previous := make(map[string]PlanTaskView)
		if index > 0 {
			for _, task := range versions[index-1].Tasks {
				previous[task.ID] = task
			}
		}
		currentIDs := make(map[string]struct{}, len(current.Tasks))
		for taskIndex := range current.Tasks {
			task := &current.Tasks[taskIndex]
			currentIDs[task.ID] = struct{}{}
			if status := statuses[current.Version][task.ID]; status != "" {
				task.Status = status
			}
			if _, exists := previous[task.ID]; exists {
				task.Change = "RETAINED"
				if task.Status == "REUSED" {
					task.Change = "REUSED"
				}
			}
		}
		if index > 0 {
			for id := range previous {
				if _, exists := currentIDs[id]; !exists {
					current.Removed = append(current.Removed, id)
				}
			}
			sort.Strings(current.Removed)
		}
	}
	return PlanSnapshot{Versions: versions}
}

func BuildDecisionView(events []DashboardEvent) DecisionView {
	var result DecisionView
	for _, event := range events {
		if event.Kind != "cognitive.decision.made" {
			continue
		}
		result.Type = stringValue(event.Data["decision"])
		result.Reason = boundedText(stringValue(event.Data["reason"]), 1024)
		result.ObservationSummary = boundedText(stringValue(event.Data["observation_summary"]), 1024)
		result.Action = boundedText(stringValue(event.Data["action"]), 1024)
		result.Iteration = eventIteration(event)
		entry := DecisionEntryView{
			Type: result.Type, Reason: result.Reason, ObservationSummary: result.ObservationSummary,
			Action: result.Action, Iteration: result.Iteration,
		}
		result.History = append(result.History, entry)
		if strings.EqualFold(result.Type, "REPLAN") {
			result.FromPlan = result.Iteration
			result.ToPlan = result.Iteration + 1
			result.ReplanReason = result.Reason
			result.ReplanObservationSummary = result.ObservationSummary
			result.ReplanAction = result.Action
		}
	}
	return result
}

func eventIteration(event DashboardEvent) int {
	if value := integerValue(event.Data["iteration"]); value > 0 {
		return value
	}
	metadata, _ := event.Data["metadata"].(map[string]any)
	return integerValue(metadata["iteration"])
}

func integerValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		result, _ := strconv.Atoi(typed.String())
		return result
	case string:
		result, _ := strconv.Atoi(typed)
		return result
	default:
		return 0
	}
}

func nestedFloat(root map[string]any, keys ...string) (float64, bool) {
	var current any = root
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return 0, false
		}
		current, ok = object[key]
		if !ok {
			return 0, false
		}
	}
	value, ok := current.(float64)
	return value, ok
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func anySlice(value any) []any {
	result, _ := value.([]any)
	return result
}

func stringSlice(value any) []string {
	values := anySlice(value)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			result = append(result, item)
		}
	}
	return result
}

func floatPointer(value float64) *float64 { return &value }
