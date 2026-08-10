// Package experiment implements the bounded, CPU-only competition experiment
// demo. The cognitive plane selects registered actions; every action still
// crosses the normal CAPSuleRT Scheduler and trusted worker process boundary.
package experiment

import (
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"aegisrt/internal/orchestrator"
)

const (
	DefaultGoal                = "比较三种机器学习方法在给定数据集上的分类性能，分析准确率、运行时间和资源消耗。如果实验过程中出现资源不足或参数不适合导致的失败，请自主调整实验配置并重新执行，最终选择综合表现最佳的方法并生成实验报告。"
	DefaultExperimentDirectory = "examples/experiment"
	ManifestFilename           = "capsule-experiment.json"

	CapabilityManifestInspect = "experiment.manifest.inspect"
	CapabilityDatasetPrepare  = "experiment.dataset.prepare"
	CapabilityRun             = "experiment.run"
	CapabilityAnalyze         = "experiment.analyze"
	CapabilityReport          = "experiment.report"

	MethodLogisticRegression = "logistic_regression"
	MethodRandomForest       = "random_forest"
	MethodSVM                = "svm"

	FailureMemoryLimitExceeded = "MEMORY_LIMIT_EXCEEDED"
	structuredFailureMarker    = "CAPSULE_STRUCTURED_FAILURE:"

	defaultMemoryLimitBytes int64 = 64 * 1024 * 1024

	// DefaultWorkScale keeps tests and the basic CLI demo quick. Higher values
	// perform proportionally more deterministic CPU work in each model worker.
	DefaultWorkScale = 1
	MaximumWorkScale = 5000

	MaximumManifestBytes   int64 = 32 * 1024
	MaximumDatasetBytes    int64 = 8 * 1024 * 1024
	MaximumManifestEntries       = 128
)

// ManifestMethod is one allowlisted method requested by a local experiment
// manifest. NEstimators is meaningful only for Random Forest.
type ManifestMethod struct {
	Method      string `json:"method"`
	NEstimators int    `json:"n_estimators,omitempty"`
}

// ManifestEntry is bounded directory metadata returned by manifest discovery.
// It deliberately contains no file content or absolute path.
type ManifestEntry struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

// ManifestResult is the normalized, workspace-relative experiment
// configuration emitted by the trusted manifest worker.
type ManifestResult struct {
	Directory      string           `json:"directory"`
	ManifestFile   string           `json:"manifest_file"`
	ManifestSHA256 string           `json:"manifest_sha256"`
	DatasetPath    string           `json:"dataset_path"`
	Methods        []ManifestMethod `json:"methods"`
	Entries        []ManifestEntry  `json:"entries"`
}

// Validate enforces the normalized allowlist contract consumed by the
// cognitive plane. It intentionally rejects generalized commands or methods.
func (r ManifestResult) Validate() error {
	if err := validateRelativeManifestPath("directory", r.Directory, true); err != nil {
		return err
	}
	if err := validateRelativeManifestPath("manifest_file", r.ManifestFile, false); err != nil {
		return err
	}
	if err := validateRelativeManifestPath("dataset_path", r.DatasetPath, false); err != nil {
		return err
	}
	expectedManifest := path.Join(r.Directory, ManifestFilename)
	if r.ManifestFile != expectedManifest {
		return fmt.Errorf("manifest_file must be %q", expectedManifest)
	}
	if r.Directory != "." && !strings.HasPrefix(r.DatasetPath, r.Directory+"/") {
		return fmt.Errorf("dataset_path must remain inside the experiment directory")
	}
	if !strings.EqualFold(path.Ext(r.DatasetPath), ".csv") {
		return fmt.Errorf("dataset_path must identify a CSV file")
	}
	if err := validateSHA256Hex(r.ManifestSHA256); err != nil {
		return err
	}
	if len(r.Methods) != 3 {
		return fmt.Errorf("manifest must define exactly three methods")
	}
	seen := make(map[string]struct{}, len(r.Methods))
	for _, method := range r.Methods {
		if _, exists := seen[method.Method]; exists {
			return fmt.Errorf("manifest method %q is duplicated", method.Method)
		}
		seen[method.Method] = struct{}{}
		switch method.Method {
		case MethodLogisticRegression, MethodSVM:
			if method.NEstimators != 0 {
				return fmt.Errorf("n_estimators is only valid for random_forest")
			}
		case MethodRandomForest:
			if method.NEstimators < 1 || method.NEstimators > 1000 {
				return fmt.Errorf("random_forest n_estimators must be between 1 and 1000")
			}
		default:
			return fmt.Errorf("unsupported experiment method %q", method.Method)
		}
	}
	for _, required := range []string{MethodLogisticRegression, MethodRandomForest, MethodSVM} {
		if _, exists := seen[required]; !exists {
			return fmt.Errorf("manifest is missing required method %q", required)
		}
	}
	if len(r.Entries) > MaximumManifestEntries {
		return fmt.Errorf("manifest directory projection exceeds %d entries", MaximumManifestEntries)
	}
	for _, entry := range r.Entries {
		if strings.TrimSpace(entry.Name) == "" || entry.Name != path.Base(entry.Name) || strings.Contains(entry.Name, "\\") {
			return fmt.Errorf("manifest entry name %q is invalid", entry.Name)
		}
		if entry.SizeBytes < 0 {
			return fmt.Errorf("manifest entry %q has a negative size", entry.Name)
		}
		switch entry.Kind {
		case "file", "directory", "symlink", "other":
		default:
			return fmt.Errorf("manifest entry %q has invalid kind %q", entry.Name, entry.Kind)
		}
	}
	return nil
}

const sha256HexLength = 64

func validateSHA256Hex(value string) error {
	if len(value) != sha256HexLength {
		return fmt.Errorf("manifest_sha256 must contain a SHA-256 digest")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("manifest_sha256 must contain a SHA-256 digest")
	}
	return nil
}

func validateRelativeManifestPath(name, value string, allowDot bool) error {
	if strings.TrimSpace(value) == "" || strings.Contains(value, "\\") || path.IsAbs(value) {
		return fmt.Errorf("%s must be a normalized workspace-relative path", name)
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == ".." || strings.HasPrefix(cleaned, "../") || (!allowDot && cleaned == ".") {
		return fmt.Errorf("%s must be a normalized workspace-relative path", name)
	}
	return nil
}

type DatasetResult struct {
	Path       string    `json:"path"`
	Rows       int       `json:"rows"`
	Features   []string  `json:"features"`
	Classes    []string  `json:"classes"`
	SHA256     string    `json:"sha256"`
	PreparedAt time.Time `json:"prepared_at"`
}

type MethodResult struct {
	Method           string         `json:"method"`
	DisplayName      string         `json:"display_name"`
	Attempt          int            `json:"attempt"`
	Status           string         `json:"status"`
	Accuracy         float64        `json:"accuracy"`
	RuntimeMS        int64          `json:"runtime_ms"`
	MemoryPeakBytes  int64          `json:"memory_peak_bytes"`
	MemoryLimitBytes int64          `json:"memory_limit_bytes"`
	Parameters       map[string]any `json:"parameters"`
	WorkChecksum     uint64         `json:"work_checksum"`
}

type FailureObservation struct {
	TaskID           string         `json:"task_id"`
	Method           string         `json:"method"`
	Attempt          int            `json:"attempt"`
	Status           string         `json:"status"`
	FailureCode      string         `json:"failure_code"`
	Reason           string         `json:"reason"`
	Retryable        bool           `json:"retryable"`
	MemoryPeakBytes  int64          `json:"memory_peak_bytes"`
	MemoryLimitBytes int64          `json:"memory_limit_bytes"`
	Parameters       map[string]any `json:"parameters"`
}

func (f FailureObservation) Validate() error {
	if strings.TrimSpace(f.TaskID) == "" || strings.TrimSpace(f.Method) == "" {
		return fmt.Errorf("failure task_id and method are required")
	}
	if f.Status != "FAILED" || f.FailureCode != FailureMemoryLimitExceeded {
		return fmt.Errorf("unsupported experiment failure")
	}
	if f.MemoryLimitBytes <= 0 || f.MemoryPeakBytes <= f.MemoryLimitBytes {
		return fmt.Errorf("invalid memory failure bounds")
	}
	if strings.TrimSpace(f.Reason) == "" {
		return fmt.Errorf("failure reason is required")
	}
	return nil
}

type AnalysisResult struct {
	Experiments []MethodResult `json:"experiments"`
	BestMethod  string         `json:"best_method"`
	BestName    string         `json:"best_name"`
	Rationale   string         `json:"rationale"`
}

type ReportResult struct {
	ReportFile     string         `json:"report_file"`
	BestMethod     string         `json:"best_method"`
	BestName       string         `json:"best_name"`
	Experiments    []MethodResult `json:"experiments"`
	Replans        int            `json:"replans"`
	RetryCode      string         `json:"retry_code,omitempty"`
	ManifestFile   string         `json:"manifest_file,omitempty"`
	ManifestSHA256 string         `json:"manifest_sha256,omitempty"`
}

type RunSummary struct {
	RunID          string               `json:"run_id"`
	Goal           string               `json:"goal"`
	Status         string               `json:"status"`
	ExecutionMode  string               `json:"execution_mode"`
	PlannerMode    string               `json:"planner_mode"`
	Replans        int                  `json:"replans"`
	Experiments    []MethodResult       `json:"experiments,omitempty"`
	FailedAttempts []FailureObservation `json:"failed_attempts,omitempty"`
	BestMethod     string               `json:"best_method,omitempty"`
	BestName       string               `json:"best_name,omitempty"`
	ReportPath     string               `json:"report_path,omitempty"`
	TelemetryPath  string               `json:"telemetry_path"`
	DurationMS     int64                `json:"duration_ms"`
	Error          string               `json:"error,omitempty"`
}

type DemoResult struct {
	Loop    orchestrator.LoopResult `json:"loop"`
	Summary RunSummary              `json:"summary"`
}
