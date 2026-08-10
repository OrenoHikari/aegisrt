package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aegisrt/internal/llm"
	"aegisrt/internal/orchestrator"
	"aegisrt/internal/planner"
)

// OfflineDemoModel is a deterministic cognitive fixture used only to make the
// competition scenario reproducible without an API. Planner and Decision
// responses still pass through the production strict LLM parsers and Registry
// validation; execution is never mocked.
type OfflineDemoModel struct {
	Goal                string
	DatasetPath         string
	ExperimentDirectory string
}

func (m *OfflineDemoModel) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	if len(request.Messages) == 0 {
		return llm.Response{}, fmt.Errorf("offline experiment model request has no messages")
	}
	content := request.Messages[len(request.Messages)-1].Content
	switch {
	case strings.HasPrefix(content, "CAPSULERT_DECISION_REQUEST\n"):
		return m.decision(strings.TrimPrefix(content, "CAPSULERT_DECISION_REQUEST\n"))
	case strings.HasPrefix(content, "CAPSULERT_REPLAN_REQUEST\n"):
		return m.replan(strings.TrimPrefix(content, "CAPSULERT_REPLAN_REQUEST\n"))
	default:
		if strings.TrimSpace(m.ExperimentDirectory) != "" {
			return encodePlan(discoveryPlan(m.Goal, m.ExperimentDirectory))
		}
		return encodePlan(initialPlan(m.Goal, m.DatasetPath))
	}
}

func (m *OfflineDemoModel) decision(payload string) (llm.Response, error) {
	var request orchestrator.DecisionRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return llm.Response{}, err
	}
	var unrecoverable *orchestrator.Observation
	for _, observation := range request.Observations {
		if observation.Success {
			continue
		}
		code, _ := observation.Output["failure_code"].(string)
		method, _ := observation.Output["method"].(string)
		retryable, _ := observation.Output["retryable"].(bool)
		if observation.Capability == CapabilityRun && method == MethodRandomForest && code == FailureMemoryLimitExceeded && retryable {
			estimators := "the configured value"
			if parameters, ok := observation.Output["parameters"].(map[string]any); ok {
				if value, exists := parameters["n_estimators"]; exists {
					estimators = fmt.Sprint(value)
				}
			}
			return encodeJSON(orchestrator.Decision{
				Type:   orchestrator.DecisionReplan,
				Reason: fmt.Sprintf("Random Forest n_estimators=%s exceeded the 64 MiB memory budget; preserve verified work and retry with n_estimators=100.", estimators),
			})
		}
		if unrecoverable == nil {
			copy := observation
			unrecoverable = &copy
		}
	}
	if unrecoverable != nil {
		return encodeJSON(orchestrator.Decision{
			Type:   orchestrator.DecisionFailed,
			Reason: fmt.Sprintf("task %s failed without a registered safe recovery", unrecoverable.TaskID),
		})
	}
	for _, observation := range request.Observations {
		if observation.Capability != CapabilityReport || !observation.Success || !observation.Metadata.OutputVerified {
			continue
		}
		best, _ := observation.Output["best_name"].(string)
		experiments, _ := observation.Output["experiments"].([]any)
		return encodeJSON(orchestrator.Decision{
			Type:        orchestrator.DecisionGoalCompleted,
			Reason:      "All three worker artifacts and the final report are verified.",
			FinalAnswer: fmt.Sprintf("All experiment artifacts were produced and verified by CAPSuleRT workers. The deterministic scenario selected %s from %d successful methods; see experiment_report.md for the fixture/measurement boundary plus runtime, failure, re-plan, and retry evidence.", best, len(experiments)),
		})
	}
	for _, observation := range request.Observations {
		if observation.Capability != CapabilityManifestInspect || !observation.Success || !observation.Metadata.OutputVerified {
			continue
		}
		manifest, err := manifestFromObservation(observation)
		if err != nil {
			return encodeJSON(orchestrator.Decision{
				Type: orchestrator.DecisionFailed, Reason: "the experiment manifest observation was not valid: " + err.Error(),
			})
		}
		return encodeJSON(orchestrator.Decision{
			Type: orchestrator.DecisionReplan,
			Reason: fmt.Sprintf(
				"Validated %s in %s; revise the DAG from the observed dataset and %d allowlisted method configurations.",
				manifest.ManifestFile, manifest.Directory, len(manifest.Methods),
			),
		})
	}
	return encodeJSON(orchestrator.Decision{
		Type:   orchestrator.DecisionFailed,
		Reason: "the verified final experiment report is missing",
	})
}

func (m *OfflineDemoModel) replan(payload string) (llm.Response, error) {
	var request orchestrator.ReplanRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return llm.Response{}, err
	}
	completed := append([]planner.Task(nil), request.CompletedTask...)
	byMethod := make(map[string]string)
	datasetID := ""
	for _, task := range completed {
		switch task.Capability {
		case CapabilityDatasetPrepare:
			datasetID = task.ID
		case CapabilityRun:
			byMethod[stringArgument(task.Arguments, "method")] = task.ID
		}
	}
	if datasetID == "" {
		manifest, manifestTask, err := manifestForReplan(request)
		if err != nil {
			return llm.Response{}, err
		}
		return encodePlan(experimentPlanFromManifest(request.PreviousPlan.Goal, completed, manifestTask.ID, manifest, request.Iteration))
	}
	if datasetID == "" || byMethod[MethodLogisticRegression] == "" || byMethod[MethodSVM] == "" {
		return llm.Response{}, fmt.Errorf("replan cannot recover without verified dataset, Logistic Regression, and SVM outputs")
	}
	retry := task(
		"random-forest-retry", "Retry Random Forest", "Retry Random Forest inside the observed memory budget.",
		CapabilityRun, map[string]any{"method": MethodRandomForest, "attempt": float64(2), "n_estimators": float64(100)}, datasetID,
	)
	analysis := task(
		"analyze-v2", "Analyze recovered experiments", "Compare the three verified method results after recovery.",
		CapabilityAnalyze, map[string]any{}, byMethod[MethodLogisticRegression], retry.ID, byMethod[MethodSVM],
	)
	report := task(
		"report-v2", "Generate experiment report", "Generate the verified final experiment report including recovery evidence.",
		CapabilityReport, recoveryReportArguments(request, float64(request.Iteration)), reportDependencies(completed, analysis.ID)...,
	)
	return encodePlan(planner.Plan{Goal: request.PreviousPlan.Goal, Tasks: append(completed, retry, analysis, report)})
}

func discoveryPlan(goal, directory string) planner.Plan {
	inspect := task(
		"manifest-inspect", "Inspect experiment workspace", "List the root-scoped directory and validate its declarative experiment manifest.",
		CapabilityManifestInspect, map[string]any{"path": directory},
	)
	return planner.Plan{Goal: goal, Tasks: []planner.Task{inspect}}
}

func experimentPlanFromManifest(goal string, completed []planner.Task, manifestTaskID string, manifest ManifestResult, iteration int) planner.Plan {
	dataset := task(
		"dataset-prepare", "Prepare discovered dataset", "Prepare the dataset selected by the verified experiment manifest.",
		CapabilityDatasetPrepare, map[string]any{"path": manifest.DatasetPath}, manifestTaskID,
	)
	methods := make(map[string]planner.Task, len(manifest.Methods))
	for _, configured := range manifest.Methods {
		arguments := map[string]any{"method": configured.Method, "attempt": float64(1)}
		id, name := configured.Method, configured.Method
		switch configured.Method {
		case MethodLogisticRegression:
			id, name = "logistic-regression", "Run Logistic Regression"
		case MethodRandomForest:
			id, name = "random-forest-configured", "Run configured Random Forest"
			arguments["n_estimators"] = float64(configured.NEstimators)
		case MethodSVM:
			id, name = "svm", "Run SVM"
		}
		methods[configured.Method] = task(
			id, name, "Execute the allowlisted method configuration read from the verified manifest.",
			CapabilityRun, arguments, dataset.ID,
		)
	}
	analysis := task(
		"analyze-configured", "Analyze configured experiments", "Compare the three verified worker results.",
		CapabilityAnalyze, map[string]any{},
		methods[MethodLogisticRegression].ID, methods[MethodRandomForest].ID, methods[MethodSVM].ID,
	)
	reportArguments := map[string]any{
		"goal": goal, "replans": float64(iteration),
		"manifest_file": manifest.ManifestFile, "manifest_sha256": manifest.ManifestSHA256,
	}
	report := task(
		"report-configured", "Generate experiment report", "Generate a report with manifest provenance and verified worker evidence.",
		CapabilityReport, reportArguments, analysis.ID, manifestTaskID,
	)
	tasks := append([]planner.Task(nil), completed...)
	tasks = append(tasks, dataset,
		methods[MethodLogisticRegression], methods[MethodRandomForest], methods[MethodSVM], analysis, report)
	return planner.Plan{Goal: goal, Tasks: tasks}
}

func manifestForReplan(request orchestrator.ReplanRequest) (ManifestResult, planner.Task, error) {
	var manifestTask planner.Task
	for _, task := range request.CompletedTask {
		if task.Capability == CapabilityManifestInspect {
			manifestTask = task
			break
		}
	}
	if manifestTask.ID == "" {
		return ManifestResult{}, planner.Task{}, fmt.Errorf("replan cannot continue without a verified manifest inspection")
	}
	for _, observation := range request.Observations {
		if observation.TaskID != manifestTask.ID || !observation.Success || !observation.Metadata.OutputVerified {
			continue
		}
		manifest, err := manifestFromObservation(observation)
		return manifest, manifestTask, err
	}
	return ManifestResult{}, planner.Task{}, fmt.Errorf("verified manifest observation is missing")
}

func manifestFromObservation(observation orchestrator.Observation) (ManifestResult, error) {
	encoded, err := json.Marshal(observation.Output)
	if err != nil {
		return ManifestResult{}, fmt.Errorf("encode manifest observation: %w", err)
	}
	var result ManifestResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return ManifestResult{}, fmt.Errorf("decode manifest observation: %w", err)
	}
	if err := result.Validate(); err != nil {
		return ManifestResult{}, err
	}
	return result, nil
}

func recoveryReportArguments(request orchestrator.ReplanRequest, replans float64) map[string]any {
	arguments := map[string]any{
		"goal": request.UserGoal, "replans": replans, "retry_code": FailureMemoryLimitExceeded,
	}
	for _, observation := range request.Observations {
		if observation.Capability != CapabilityManifestInspect || !observation.Success || !observation.Metadata.OutputVerified {
			continue
		}
		if manifest, err := manifestFromObservation(observation); err == nil {
			arguments["manifest_file"] = manifest.ManifestFile
			arguments["manifest_sha256"] = manifest.ManifestSHA256
		}
	}
	return arguments
}

func reportDependencies(completed []planner.Task, analysisID string) []string {
	dependencies := []string{analysisID}
	for _, completedTask := range completed {
		if completedTask.Capability == CapabilityManifestInspect {
			return append(dependencies, completedTask.ID)
		}
	}
	return dependencies
}

func initialPlan(goal, datasetPath string) planner.Plan {
	dataset := task(
		"dataset-prepare", "Prepare classification dataset", "Inspect and prepare the root-scoped local classification dataset.",
		CapabilityDatasetPrepare, map[string]any{"path": datasetPath},
	)
	logistic := task(
		"logistic-regression", "Run Logistic Regression", "Execute the Logistic Regression CPU simulation.",
		CapabilityRun, map[string]any{"method": MethodLogisticRegression, "attempt": float64(1)}, dataset.ID,
	)
	randomForest := task(
		"random-forest-large", "Run Random Forest", "Execute the initially requested large Random Forest configuration.",
		CapabilityRun, map[string]any{"method": MethodRandomForest, "attempt": float64(1), "n_estimators": float64(1000)}, dataset.ID,
	)
	svm := task(
		"svm", "Run SVM", "Execute the SVM CPU simulation.",
		CapabilityRun, map[string]any{"method": MethodSVM, "attempt": float64(1)}, dataset.ID,
	)
	analysis := task(
		"analyze-v1", "Analyze experiment results", "Compare verified accuracy, runtime, and memory results.",
		CapabilityAnalyze, map[string]any{}, logistic.ID, randomForest.ID, svm.ID,
	)
	report := task(
		"report-v1", "Generate experiment report", "Generate the verified final experiment report.",
		CapabilityReport, map[string]any{"goal": goal, "replans": float64(0)}, analysis.ID,
	)
	return planner.Plan{Goal: goal, Tasks: []planner.Task{dataset, logistic, randomForest, svm, analysis, report}}
}

func task(id, name, description, capability string, arguments map[string]any, dependencies ...string) planner.Task {
	return planner.Task{
		ID: id, Name: name, Description: description, Capability: capability,
		Arguments: arguments, DependsOn: append([]string(nil), dependencies...),
	}
}

func encodePlan(plan planner.Plan) (llm.Response, error) { return encodeJSON(plan) }

func encodeJSON(value any) (llm.Response, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Content: string(encoded)}, nil
}

// PlanPolicy prevents the fixture (or a future real LLM option) from widening
// this demo into arbitrary or unexpectedly expensive computation.
type PlanPolicy struct{}

func (PlanPolicy) ValidatePlan(plan planner.Plan) error {
	if len(plan.Tasks) > 7 {
		return fmt.Errorf("experiment plan is limited to seven tasks per iteration")
	}
	methodCount := make(map[string]int)
	capabilityCount := make(map[string]int)
	for _, task := range plan.Tasks {
		switch task.Capability {
		case CapabilityManifestInspect, CapabilityDatasetPrepare, CapabilityAnalyze, CapabilityReport:
			capabilityCount[task.Capability]++
			if capabilityCount[task.Capability] > 1 {
				return fmt.Errorf("experiment plan duplicates capability %s", task.Capability)
			}
		case CapabilityRun:
			method := stringArgument(task.Arguments, "method")
			methodCount[method]++
			if methodCount[method] > 1 {
				return fmt.Errorf("experiment plan duplicates method %s", method)
			}
		default:
			return fmt.Errorf("capability %s is outside the experiment demo policy", task.Capability)
		}
	}
	return nil
}
