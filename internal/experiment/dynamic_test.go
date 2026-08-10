package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegisrt/internal/orchestrator"
)

func TestManifestDrivenDemoDiscoversReplansRecoversAndReuses(t *testing.T) {
	root := t.TempDir()
	result, err := RunDemo(context.Background(), "读取目录设置并比较三个方法", DemoOptions{
		Root: root, WorkspaceRoot: repositoryRoot(t), ExperimentDirectory: DefaultExperimentDirectory,
		Workers: 3, MaxReplans: 3, LoopTimeout: 30 * time.Second, WorkScale: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.Status != "COMPLETED" || result.Summary.PlannerMode != "OFFLINE_MANIFEST_DRIVEN_LLM_FIXTURE" {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if result.Loop.Replans != 2 || len(result.Loop.Iterations) != 3 {
		t.Fatalf("replans=%d iterations=%d", result.Loop.Replans, len(result.Loop.Iterations))
	}
	first := result.Loop.Iterations[0]
	if len(first.Plan.Tasks) != 1 || first.Plan.Tasks[0].Capability != CapabilityManifestInspect {
		t.Fatalf("initial discovery plan = %+v", first.Plan)
	}
	var discovered ManifestResult
	for _, observation := range first.Observations {
		if observation.Capability != CapabilityManifestInspect || !observation.Success || !observation.Metadata.OutputVerified {
			continue
		}
		encoded, _ := json.Marshal(observation.Output)
		if err := json.Unmarshal(encoded, &discovered); err != nil {
			t.Fatal(err)
		}
	}
	if err := discovered.Validate(); err != nil {
		t.Fatalf("manifest observation = %+v: %v", discovered, err)
	}
	if discovered.DatasetPath != "examples/experiment/classification.csv" {
		t.Fatalf("dataset path = %q", discovered.DatasetPath)
	}
	assertReusedExperimentTasks(t, result.Loop.Iterations[1].Execution.ReusedTaskIDs, "manifest-inspect")
	assertReusedExperimentTasks(t, result.Loop.Iterations[2].Execution.ReusedTaskIDs,
		"manifest-inspect", "dataset-prepare", "logistic-regression", "svm")
	if len(result.Summary.FailedAttempts) != 1 || result.Summary.FailedAttempts[0].FailureCode != FailureMemoryLimitExceeded {
		t.Fatalf("failed attempts = %+v", result.Summary.FailedAttempts)
	}
	if result.Summary.BestMethod != MethodRandomForest || len(result.Summary.Experiments) != 3 {
		t.Fatalf("experiment summary = %+v", result.Summary)
	}
	report, err := os.ReadFile(filepath.Join(root, "experiment_report.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{ManifestFilename, discovered.ManifestSHA256, FailureMemoryLimitExceeded, "deterministic scenario fixtures", "not measurements from ML model training"} {
		if !strings.Contains(string(report), expected) {
			t.Fatalf("report does not contain %q:\n%s", expected, report)
		}
	}
	reportUsesManifest := false
	for _, iteration := range result.Loop.Iterations[1:] {
		for _, planned := range iteration.Plan.Tasks {
			if planned.Capability == CapabilityReport && containsString(planned.DependsOn, "manifest-inspect") {
				reportUsesManifest = true
			}
		}
	}
	if !reportUsesManifest {
		t.Fatal("report did not depend directly on the verified manifest output")
	}
}

func TestManifestDrivenDemoUsesCurrentConfigurationWithoutResourceRetry(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "requested")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	dataset, err := os.ReadFile(filepath.Join(repositoryRoot(t), "examples", "experiment", "classification.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "classification.csv"), dataset, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"dataset":"classification.csv","methods":[{"method":"logistic_regression"},{"method":"random_forest","n_estimators":100},{"method":"svm"}]}`
	if err := os.WriteFile(filepath.Join(directory, ManifestFilename), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	result, err := RunDemo(context.Background(), "按目录中的设置运行", DemoOptions{
		Root: root, WorkspaceRoot: workspace, ExperimentDirectory: "requested",
		Workers: 3, MaxReplans: 3, LoopTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Loop.Replans != 1 || len(result.Loop.Iterations) != 2 || len(result.Summary.FailedAttempts) != 0 {
		t.Fatalf("result replans=%d iterations=%d failures=%+v", result.Loop.Replans, len(result.Loop.Iterations), result.Summary.FailedAttempts)
	}
	for _, method := range result.Summary.Experiments {
		if method.Method == MethodRandomForest {
			if method.Attempt != 1 || method.Parameters["n_estimators"] != float64(100) {
				t.Fatalf("Random Forest result = %+v", method)
			}
		}
	}
	report, err := os.ReadFile(filepath.Join(root, "experiment_report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(report), FailureMemoryLimitExceeded) || !strings.Contains(string(report), "No resource retry") {
		t.Fatalf("unexpected recovery report:\n%s", report)
	}
}

func TestManifestDrivenDemoRejectsInvalidConfigurationBeforeExperiments(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "unsafe")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":1,"dataset":"../../outside.csv","methods":[],"command":"sh"}`
	if err := os.WriteFile(filepath.Join(directory, ManifestFilename), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RunDemo(context.Background(), "运行目录实验", DemoOptions{
		Root: t.TempDir(), WorkspaceRoot: workspace, ExperimentDirectory: "unsafe",
		Workers: 2, MaxReplans: 3, LoopTimeout: 30 * time.Second,
	})
	if err == nil || result.Summary.Status != "FAILED" {
		t.Fatalf("result=%+v err=%v", result.Summary, err)
	}
	for _, iteration := range result.Loop.Iterations {
		for _, planned := range iteration.Plan.Tasks {
			if planned.Capability == CapabilityRun {
				t.Fatalf("invalid manifest planned an experiment worker: %+v", planned)
			}
		}
	}
}

func TestManifestDrivenDemoHonorsReplanLimit(t *testing.T) {
	result, err := RunDemo(context.Background(), "读取配置并恢复资源失败", DemoOptions{
		Root: t.TempDir(), WorkspaceRoot: repositoryRoot(t), ExperimentDirectory: DefaultExperimentDirectory,
		Workers: 3, MaxReplans: 1, LoopTimeout: 30 * time.Second,
	})
	if !errors.Is(err, orchestrator.ErrMaxReplansExceeded) {
		t.Fatalf("error = %v", err)
	}
	if result.Loop.Replans != 1 || len(result.Loop.Iterations) != 2 || result.Summary.Status != "FAILED" {
		t.Fatalf("replans=%d iterations=%d summary=%+v", result.Loop.Replans, len(result.Loop.Iterations), result.Summary)
	}
}

func TestManifestDrivenDemoRereadsChangedConfiguration(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "mutable")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	dataset, err := os.ReadFile(filepath.Join(repositoryRoot(t), "examples", "experiment", "classification.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "classification.csv"), dataset, 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest := func(estimators int) {
		t.Helper()
		manifest := `{"version":1,"dataset":"classification.csv","methods":[{"method":"logistic_regression"},{"method":"random_forest","n_estimators":` + fmt.Sprint(estimators) + `},{"method":"svm"}]}`
		if err := os.WriteFile(filepath.Join(directory, ManifestFilename), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func() DemoResult {
		t.Helper()
		result, err := RunDemo(context.Background(), "使用当前目录设置", DemoOptions{
			Root: t.TempDir(), WorkspaceRoot: workspace, ExperimentDirectory: "mutable",
			Workers: 3, MaxReplans: 3, LoopTimeout: 30 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	writeManifest(900)
	first := run()
	writeManifest(100)
	second := run()
	firstManifest, err := manifestFromObservation(first.Loop.Iterations[0].Observations[0])
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := manifestFromObservation(second.Loop.Iterations[0].Observations[0])
	if err != nil {
		t.Fatal(err)
	}
	if first.Loop.RunID == second.Loop.RunID || firstManifest.ManifestSHA256 == secondManifest.ManifestSHA256 {
		t.Fatalf("configuration was not freshly observed: first=%s/%s second=%s/%s",
			first.Loop.RunID, firstManifest.ManifestSHA256, second.Loop.RunID, secondManifest.ManifestSHA256)
	}
	if len(first.Summary.FailedAttempts) != 1 || len(second.Summary.FailedAttempts) != 0 || first.Loop.Replans != 2 || second.Loop.Replans != 1 {
		t.Fatalf("first failures/replans=%d/%d second=%d/%d",
			len(first.Summary.FailedAttempts), first.Loop.Replans, len(second.Summary.FailedAttempts), second.Loop.Replans)
	}
	if !strings.Contains(first.Loop.Iterations[1].Decision.Reason, "n_estimators=900") {
		t.Fatalf("resource decision did not use observed configuration: %s", first.Loop.Iterations[1].Decision.Reason)
	}
}

func TestReportRejectsUnverifiedManifestProvenance(t *testing.T) {
	manifest := ManifestResult{
		Directory: "configured", ManifestFile: "configured/" + ManifestFilename,
		ManifestSHA256: strings.Repeat("0", 64), DatasetPath: "configured/classification.csv",
		Methods: []ManifestMethod{
			{Method: MethodLogisticRegression},
			{Method: MethodRandomForest, NEstimators: 100},
			{Method: MethodSVM},
		},
	}
	analysis := AnalysisResult{
		BestMethod: MethodRandomForest, BestName: "Random Forest",
		Experiments: []MethodResult{
			{Method: MethodLogisticRegression, Status: "SUCCEEDED"},
			{Method: MethodRandomForest, Status: "SUCCEEDED"},
			{Method: MethodSVM, Status: "SUCCEEDED"},
		},
	}
	manifestJSON, _ := json.Marshal(manifest)
	analysisJSON, _ := json.Marshal(analysis)
	err := writeReport(t.TempDir(), map[string]any{
		"goal": "compare", "replans": float64(1),
		"manifest_file": manifest.ManifestFile, "manifest_sha256": strings.Repeat("1", 64),
	}, []json.RawMessage{manifestJSON, analysisJSON})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("forged manifest provenance error = %v", err)
	}
}

func assertReusedExperimentTasks(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	set := make(map[string]bool, len(actual))
	for _, id := range actual {
		set[id] = true
	}
	for _, id := range expected {
		if !set[id] {
			t.Fatalf("task %s was not reused; reused=%v", id, actual)
		}
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
