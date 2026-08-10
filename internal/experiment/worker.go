package experiment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aegisrt/internal/agent"
	"aegisrt/internal/outputtxn"
)

const maximumDependencyBytes = 1024 * 1024

// RunWorker executes one fixed experiment action. It accepts no command or
// script from the cognitive plane.
func RunWorker(ctx context.Context, arguments []string) error {
	flags := flag.NewFlagSet("internal-experiment-worker", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	action := flags.String("action", "", "registered experiment action")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	staging, err := requiredPath("AEGIS_OUTPUT_STAGING")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	args, err := decodeArguments()
	if err != nil {
		return err
	}

	switch *action {
	case "manifest_inspect":
		return inspectManifest(staging)
	case "dataset_prepare":
		return prepareDataset(staging)
	case "experiment_run":
		dependencies, err := loadDependencyResults()
		if err != nil {
			return err
		}
		return runMethod(ctx, staging, args, dependencies)
	case "experiment_analyze":
		dependencies, err := loadDependencyResults()
		if err != nil {
			return err
		}
		return analyzeMethods(staging, dependencies)
	case "experiment_report":
		dependencies, err := loadDependencyResults()
		if err != nil {
			return err
		}
		return writeReport(staging, args, dependencies)
	default:
		return fmt.Errorf("unknown registered experiment action %q", *action)
	}
}

type manifestDocument struct {
	Version int              `json:"version"`
	Dataset string           `json:"dataset"`
	Methods []ManifestMethod `json:"methods"`
}

func inspectManifest(staging string) error {
	workspaceRoot, err := requiredPath("CAPSULE_EXPERIMENT_WORKSPACE")
	if err != nil {
		return err
	}
	directory, err := requiredPath("CAPSULE_EXPERIMENT_MANIFEST_DIR")
	if err != nil {
		return err
	}
	workspaceRoot, err = filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve experiment workspace: %w", err)
	}
	directory, err = resolveManifestDirectoryPath(workspaceRoot, directory)
	if err != nil {
		return err
	}
	directoryRelative, err := workspaceRelativePath(workspaceRoot, directory)
	if err != nil {
		return err
	}
	entries, err := inspectManifestEntries(directory)
	if err != nil {
		return err
	}

	manifestPath := filepath.Join(directory, ManifestFilename)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return fmt.Errorf("inspect experiment manifest %q: %w", ManifestFilename, err)
	}
	if manifestInfo.Mode()&os.ModeSymlink != 0 || !manifestInfo.Mode().IsRegular() {
		return fmt.Errorf("experiment manifest %q must be a regular non-symlink file", ManifestFilename)
	}
	if manifestInfo.Size() > MaximumManifestBytes {
		return fmt.Errorf("experiment manifest exceeds the %d-byte limit", MaximumManifestBytes)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read experiment manifest: %w", err)
	}
	if int64(len(manifestData)) > MaximumManifestBytes {
		return fmt.Errorf("experiment manifest exceeds the %d-byte limit", MaximumManifestBytes)
	}
	document, err := decodeManifest(manifestData)
	if err != nil {
		return err
	}
	datasetRequest, err := cleanManifestDatasetPath(document.Dataset)
	if err != nil {
		return err
	}
	datasetPath, err := resolveDatasetPath(workspaceRoot, filepath.Join(directory, datasetRequest))
	if err != nil {
		return fmt.Errorf("validate manifest dataset: %w", err)
	}
	if !pathWithinExperimentRoot(directory, datasetPath) {
		return fmt.Errorf("manifest dataset escapes the experiment directory")
	}
	datasetRelative, err := workspaceRelativePath(workspaceRoot, datasetPath)
	if err != nil {
		return err
	}
	manifestRelative, err := workspaceRelativePath(workspaceRoot, manifestPath)
	if err != nil {
		return err
	}

	methods := append([]ManifestMethod(nil), document.Methods...)
	sort.Slice(methods, func(i, j int) bool { return methodOrder(methods[i].Method) < methodOrder(methods[j].Method) })
	digest := sha256.Sum256(manifestData)
	result := ManifestResult{
		Directory: directoryRelative, ManifestFile: manifestRelative,
		ManifestSHA256: fmt.Sprintf("%x", digest[:]), DatasetPath: datasetRelative,
		Methods: methods, Entries: entries,
	}
	if err := result.Validate(); err != nil {
		return fmt.Errorf("validate experiment manifest: %w", err)
	}
	return writeResult(staging, result, fmt.Sprintf(
		"Manifest: %s\nSHA256: %s\nDataset: %s\nMethods: %s\n",
		result.ManifestFile, result.ManifestSHA256, result.DatasetPath, manifestMethodNames(result.Methods),
	))
}

func decodeManifest(data []byte) (manifestDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document manifestDocument
	if err := decoder.Decode(&document); err != nil {
		return manifestDocument{}, fmt.Errorf("decode experiment manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return manifestDocument{}, fmt.Errorf("decode experiment manifest: multiple JSON values")
		}
		return manifestDocument{}, fmt.Errorf("decode experiment manifest trailing content: %w", err)
	}
	if document.Version != 1 {
		return manifestDocument{}, fmt.Errorf("unsupported experiment manifest version %d", document.Version)
	}
	// A temporary normalized result reuses the public method allowlist checks.
	probe := ManifestResult{
		Directory: ".", ManifestFile: ManifestFilename,
		ManifestSHA256: strings.Repeat("0", sha256HexLength), DatasetPath: "dataset.csv",
		Methods: document.Methods,
	}
	if err := probe.Validate(); err != nil {
		return manifestDocument{}, fmt.Errorf("validate experiment manifest methods: %w", err)
	}
	return document, nil
}

func cleanManifestDatasetPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return "", fmt.Errorf("manifest dataset must be a relative CSV path")
	}
	cleaned := filepath.Clean(value)
	if cleaned != value || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("manifest dataset must be normalized without path traversal")
	}
	if !strings.EqualFold(filepath.Ext(cleaned), ".csv") {
		return "", fmt.Errorf("manifest dataset must be a CSV file")
	}
	return cleaned, nil
}

func inspectManifestEntries(directory string) ([]ManifestEntry, error) {
	handle, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("list experiment directory: %w", err)
	}
	defer handle.Close()
	items, err := handle.ReadDir(MaximumManifestEntries + 1)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("list experiment directory: %w", err)
	}
	if len(items) > MaximumManifestEntries {
		return nil, fmt.Errorf("experiment directory exceeds the %d-entry inspection limit", MaximumManifestEntries)
	}
	entries := make([]ManifestEntry, 0, len(items))
	for _, item := range items {
		if item.Type()&os.ModeSymlink != 0 {
			entries = append(entries, ManifestEntry{Name: item.Name(), Kind: "symlink"})
			continue
		}
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect experiment directory entry %q: %w", item.Name(), err)
		}
		kind := "other"
		switch {
		case item.IsDir():
			kind = "directory"
		case info.Mode().IsRegular():
			kind = "file"
		}
		entries = append(entries, ManifestEntry{Name: item.Name(), Kind: kind, SizeBytes: info.Size()})
	}
	return entries, nil
}

func manifestMethodNames(methods []ManifestMethod) string {
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.Method)
	}
	return strings.Join(names, ", ")
}

func prepareDataset(staging string) error {
	workspaceRoot, err := requiredPath("CAPSULE_EXPERIMENT_WORKSPACE")
	if err != nil {
		return err
	}
	path, err := requiredPath("CAPSULE_EXPERIMENT_DATASET")
	if err != nil {
		return err
	}
	path, err = resolveDatasetPath(workspaceRoot, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if int64(len(data)) > MaximumDatasetBytes {
		return fmt.Errorf("dataset exceeds the %d-byte limit", MaximumDatasetBytes)
	}
	reader := csv.NewReader(strings.NewReader(string(data)))
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("parse classification CSV: %w", err)
	}
	if len(records) < 2 || len(records[0]) < 2 {
		return fmt.Errorf("classification CSV requires a header and at least one row")
	}
	width := len(records[0])
	classes := make(map[string]struct{})
	for index, row := range records[1:] {
		if len(row) != width {
			return fmt.Errorf("classification CSV row %d has %d columns; expected %d", index+2, len(row), width)
		}
		classes[strings.TrimSpace(row[width-1])] = struct{}{}
	}
	classList := make([]string, 0, len(classes))
	for value := range classes {
		classList = append(classList, value)
	}
	sort.Strings(classList)
	digest := sha256.Sum256(data)
	result := DatasetResult{
		Path: filepath.Base(path), Rows: len(records) - 1,
		Features: append([]string(nil), records[0][:width-1]...), Classes: classList,
		SHA256: fmt.Sprintf("%x", digest[:]), PreparedAt: time.Now().UTC(),
	}
	return writeResult(staging, result, fmt.Sprintf(
		"Dataset: %s\nRows: %d\nFeatures: %d\nClasses: %s\nSHA256: %s\n",
		result.Path, result.Rows, len(result.Features), strings.Join(result.Classes, ", "), result.SHA256,
	))
}

func runMethod(ctx context.Context, staging string, arguments map[string]any, dependencies []json.RawMessage) error {
	var dataset DatasetResult
	if err := findDependency(dependencies, &dataset, func(raw map[string]any) bool {
		_, rows := raw["rows"]
		_, features := raw["features"]
		return rows && features
	}); err != nil {
		return fmt.Errorf("verified dataset dependency: %w", err)
	}
	if dataset.Rows <= 0 {
		return fmt.Errorf("verified dataset is empty")
	}
	method := stringArgument(arguments, "method")
	attempt := integerArgument(arguments, "attempt")
	limit := environmentInt64("CAPSULE_EXPERIMENT_MEMORY_LIMIT", defaultMemoryLimitBytes)
	workScale, err := environmentWorkScale()
	if err != nil {
		return err
	}
	parameters := map[string]any{"dataset_sha256": dataset.SHA256, "work_scale": workScale}
	accuracy, memoryPeak, units := 0.0, int64(0), 0
	displayName := ""
	switch method {
	case MethodLogisticRegression:
		displayName, accuracy, memoryPeak, units = "Logistic Regression", 0.86, 18*1024*1024, 350000
		parameters["regularization"] = 1.0
	case MethodRandomForest:
		estimators := integerArgument(arguments, "n_estimators")
		displayName = "Random Forest"
		parameters["n_estimators"] = estimators
		memoryPeak = int64(12+estimators*80/1000) * 1024 * 1024
		if memoryPeak > limit {
			failure := FailureObservation{
				TaskID: os.Getenv("CAPSULE_TASK_ID"), Method: method, Attempt: attempt, Status: "FAILED",
				FailureCode: FailureMemoryLimitExceeded, Reason: fmt.Sprintf("estimated working set %d bytes exceeds configured limit %d bytes", memoryPeak, limit),
				Retryable: true, MemoryPeakBytes: memoryPeak, MemoryLimitBytes: limit, Parameters: parameters,
			}
			if err := writeFailure(staging, failure); err != nil {
				return err
			}
			return fmt.Errorf("%s", FailureMemoryLimitExceeded)
		}
		accuracy, units = 0.91, 550000
	case MethodSVM:
		displayName, accuracy, memoryPeak, units = "SVM", 0.88, 24*1024*1024, 450000
		parameters["kernel"] = "rbf"
	default:
		return fmt.Errorf("unsupported method %q", method)
	}
	started := time.Now()
	checksum, err := deterministicCPUWork(ctx, units, workScale, uint64(dataset.Rows))
	if err != nil {
		return err
	}
	runtimeMS := time.Since(started).Milliseconds()
	if runtimeMS < 1 {
		runtimeMS = 1
	}
	result := MethodResult{
		Method: method, DisplayName: displayName, Attempt: attempt, Status: "SUCCEEDED",
		Accuracy: accuracy, RuntimeMS: runtimeMS, MemoryPeakBytes: memoryPeak, MemoryLimitBytes: limit,
		Parameters: parameters, WorkChecksum: checksum,
	}
	return writeResult(staging, result, fmt.Sprintf(
		"Method: %s\nStatus: %s\nAccuracy: %.2f\nRuntime: %d ms\nMemory: %d / %d bytes\nAttempt: %d\n",
		result.DisplayName, result.Status, result.Accuracy, result.RuntimeMS,
		result.MemoryPeakBytes, result.MemoryLimitBytes, result.Attempt,
	))
}

func deterministicCPUWork(ctx context.Context, units, workScale int, seed uint64) (uint64, error) {
	if units <= 0 {
		return 0, fmt.Errorf("CPU work units must be positive")
	}
	if err := validateWorkScale(workScale); err != nil {
		return 0, err
	}
	if units > int(^uint(0)>>1)/workScale {
		return 0, fmt.Errorf("scaled CPU work units overflow")
	}
	units *= workScale
	value := uint64(1469598103934665603) ^ seed
	for index := 0; index < units; index++ {
		if index%32768 == 0 {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			default:
			}
		}
		value ^= uint64(index) + 0x9e3779b97f4a7c15
		value *= 1099511628211
		value ^= value >> 29
	}
	return value, nil
}

func analyzeMethods(staging string, dependencies []json.RawMessage) error {
	results := make([]MethodResult, 0, 3)
	seen := make(map[string]struct{})
	for _, raw := range dependencies {
		var result MethodResult
		if json.Unmarshal(raw, &result) != nil || result.Status != "SUCCEEDED" || result.Method == "" {
			continue
		}
		if _, exists := seen[result.Method]; exists {
			return fmt.Errorf("duplicate result for method %s", result.Method)
		}
		seen[result.Method] = struct{}{}
		results = append(results, result)
	}
	if len(results) != 3 {
		return fmt.Errorf("analysis requires three successful method results; got %d", len(results))
	}
	sort.Slice(results, func(i, j int) bool { return methodOrder(results[i].Method) < methodOrder(results[j].Method) })
	best := results[0]
	for _, result := range results[1:] {
		if result.Accuracy > best.Accuracy {
			best = result
		}
	}
	analysis := AnalysisResult{
		Experiments: results, BestMethod: best.Method, BestName: best.DisplayName,
		Rationale: fmt.Sprintf("%s has the highest deterministic scenario accuracy (%.2f) while remaining inside the %d MiB memory budget; measured worker runtime and the working-set estimate are reported for trade-off review.", best.DisplayName, best.Accuracy, best.MemoryLimitBytes/1024/1024),
	}
	return writeResult(staging, analysis, renderAnalysis(analysis))
}

func writeReport(staging string, arguments map[string]any, dependencies []json.RawMessage) error {
	var analysis AnalysisResult
	if err := findDependency(dependencies, &analysis, func(raw map[string]any) bool {
		_, experiments := raw["experiments"]
		_, best := raw["best_method"]
		return experiments && best
	}); err != nil {
		return err
	}
	replans := integerArgument(arguments, "replans")
	retryCode := stringArgument(arguments, "retry_code")
	manifestFile := stringArgument(arguments, "manifest_file")
	manifestSHA256 := stringArgument(arguments, "manifest_sha256")
	if (manifestFile == "") != (manifestSHA256 == "") {
		return fmt.Errorf("manifest_file and manifest_sha256 must be provided together")
	}
	if manifestFile != "" {
		if err := validateRelativeManifestPath("manifest_file", manifestFile, false); err != nil {
			return err
		}
		if err := validateSHA256Hex(manifestSHA256); err != nil {
			return err
		}
		var manifest ManifestResult
		if err := findDependency(dependencies, &manifest, func(raw map[string]any) bool {
			_, file := raw["manifest_file"]
			_, digest := raw["manifest_sha256"]
			_, methods := raw["methods"]
			return file && digest && methods
		}); err != nil {
			return fmt.Errorf("verified manifest dependency: %w", err)
		}
		if err := manifest.Validate(); err != nil {
			return fmt.Errorf("verified manifest dependency: %w", err)
		}
		if manifest.ManifestFile != manifestFile || manifest.ManifestSHA256 != manifestSHA256 {
			return fmt.Errorf("report manifest provenance does not match the verified manifest dependency")
		}
	}
	var report strings.Builder
	fmt.Fprintf(&report, "# Autonomous Experiment Report\n\n")
	fmt.Fprintf(&report, "## Goal\n\n%s\n\n", stringArgument(arguments, "goal"))
	fmt.Fprintf(&report, "## Execution provenance\n\n")
	fmt.Fprintf(&report, "Registered CPU-only worker processes were freshly scheduled by CAPSuleRT; the planner did not execute tools directly. Worker CPU work/runtime, Scheduler lifecycle, failure/retry, and output transactions are real execution. Accuracy values are deterministic scenario fixtures and peak working-set values are estimates, not measurements from ML model training.\n\n")
	if manifestFile != "" {
		fmt.Fprintf(&report, "Experiment configuration: `%s` (`SHA-256 %s`).\n\n", manifestFile, manifestSHA256)
	}
	fmt.Fprintf(&report, "| Method | Accuracy | Worker runtime | Peak working-set estimate | Limit | Attempt |\n")
	fmt.Fprintf(&report, "|---|---:|---:|---:|---:|---:|\n")
	for _, result := range analysis.Experiments {
		fmt.Fprintf(&report, "| %s | %.2f | %d ms | %.1f MiB | %.1f MiB | %d |\n",
			result.DisplayName, result.Accuracy, result.RuntimeMS,
			float64(result.MemoryPeakBytes)/(1024*1024), float64(result.MemoryLimitBytes)/(1024*1024), result.Attempt)
	}
	fmt.Fprintf(&report, "\n## Recovery\n\n")
	if retryCode != "" {
		fmt.Fprintf(&report, "The initial Random Forest configuration was rejected with `%s`. Structured Observation drove a revised plan that reused verified dataset and method outputs, then retried Random Forest with a bounded configuration. Total replans: %d.\n\n", retryCode, replans)
	} else if replans > 0 {
		fmt.Fprintf(&report, "The plan was revised %d time(s) after inspecting and validating the local experiment configuration. No resource retry was required.\n\n", replans)
	} else {
		fmt.Fprintf(&report, "No re-plan or resource retry was required.\n\n")
	}
	fmt.Fprintf(&report, "## Selection\n\n**Best method: %s**\n\n%s\n", analysis.BestName, analysis.Rationale)
	file := "experiment_report.md"
	if err := os.WriteFile(filepath.Join(staging, file), []byte(report.String()), 0o600); err != nil {
		return err
	}
	result := ReportResult{
		ReportFile: file, BestMethod: analysis.BestMethod, BestName: analysis.BestName,
		Experiments: analysis.Experiments, Replans: replans, RetryCode: retryCode,
		ManifestFile: manifestFile, ManifestSHA256: manifestSHA256,
	}
	return writeResult(staging, result, report.String())
}

func renderAnalysis(analysis AnalysisResult) string {
	var text strings.Builder
	for _, result := range analysis.Experiments {
		fmt.Fprintf(&text, "%s: accuracy=%.2f runtime=%dms memory=%.1fMiB\n",
			result.DisplayName, result.Accuracy, result.RuntimeMS, float64(result.MemoryPeakBytes)/(1024*1024))
	}
	fmt.Fprintf(&text, "Best method: %s\n%s\n", analysis.BestName, analysis.Rationale)
	return text.String()
}

func methodOrder(method string) int {
	switch method {
	case MethodLogisticRegression:
		return 1
	case MethodRandomForest:
		return 2
	case MethodSVM:
		return 3
	default:
		return 99
	}
}

func writeFailure(staging string, failure FailureObservation) error {
	if err := failure.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(failure, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(staging, "failure.json"), encoded, 0o600)
}

func writeResult(staging string, value any, text string) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "result.json"), encoded, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(staging, "result.txt"), []byte(text), 0o600)
}

func decodeArguments() (map[string]any, error) {
	raw := strings.TrimSpace(os.Getenv("CAPSULE_TASK_ARGUMENTS_JSON"))
	if raw == "" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func requiredPath(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("required environment variable %s is missing", name)
	}
	return filepath.Abs(value)
}

func environmentInt64(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func environmentWorkScale() (int, error) {
	raw := strings.TrimSpace(os.Getenv("CAPSULE_EXPERIMENT_WORK_SCALE"))
	if raw == "" {
		return DefaultWorkScale, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("CAPSULE_EXPERIMENT_WORK_SCALE must be an integer: %w", err)
	}
	if err := validateWorkScale(value); err != nil {
		return 0, err
	}
	return value, nil
}

func loadDependencyResults() ([]json.RawMessage, error) {
	raw := strings.TrimSpace(os.Getenv("AEGIS_DEPENDENCY_OUTPUTS_JSON"))
	if raw == "" {
		return nil, fmt.Errorf("verified dependency outputs are required")
	}
	var outputs map[string]agent.DependencyOutput
	if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(outputs))
	for id := range outputs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	results := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		output := outputs[id]
		if !output.Verified {
			return nil, fmt.Errorf("dependency %s is not verified", id)
		}
		commit, err := filepath.Abs(output.CommitPath)
		if err != nil {
			return nil, err
		}
		manifestPath, err := filepath.Abs(output.ManifestPath)
		if err != nil || !insidePath(commit, manifestPath) {
			return nil, fmt.Errorf("dependency %s manifest escapes commit path", id)
		}
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		var manifest outputtxn.Manifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return nil, err
		}
		allowed := false
		for _, file := range manifest.Files {
			if filepath.ToSlash(file.Path) == "result.json" {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("dependency %s has no verified result.json", id)
		}
		resultPath := filepath.Join(commit, "result.json")
		if !insidePath(commit, resultPath) {
			return nil, fmt.Errorf("dependency %s result escapes commit path", id)
		}
		data, err := os.ReadFile(resultPath)
		if err != nil {
			return nil, err
		}
		if len(data) > maximumDependencyBytes {
			return nil, fmt.Errorf("dependency %s result is too large", id)
		}
		results = append(results, append(json.RawMessage(nil), data...))
	}
	return results, nil
}

func findDependency(results []json.RawMessage, target any, matches func(map[string]any) bool) error {
	for _, result := range results {
		var raw map[string]any
		if json.Unmarshal(result, &raw) != nil || !matches(raw) {
			continue
		}
		return json.Unmarshal(result, target)
	}
	return fmt.Errorf("required typed dependency result is missing")
}

func insidePath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
