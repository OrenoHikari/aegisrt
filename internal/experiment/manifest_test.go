package experiment

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aegisrt/internal/planner"
)

const validManifestFixture = `{
  "version": 1,
  "dataset": "classification.csv",
  "methods": [
    {"method": "svm"},
    {"method": "random_forest", "n_estimators": 1000},
    {"method": "logistic_regression"}
  ]
}`

func TestManifestCapabilityBuildsFixedWorkerAndReturnsNormalizedResult(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "configured")
	writeManifestFixture(t, directory, validManifestFixture)
	if err := os.WriteFile(filepath.Join(directory, "README.txt"), []byte("bounded metadata only"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := NewRegistry(RegistrationOptions{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatal(err)
	}
	job, err := registry.Build(context.Background(), planner.Task{
		ID: "inspect-manifest", Name: "inspect manifest", Description: "inspect the configured directory",
		Capability: CapabilityManifestInspect, Arguments: map[string]any{"path": "configured"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(job.Agent.Args, " "); got != "internal-experiment-worker --action manifest_inspect" {
		t.Fatalf("manifest worker arguments = %q", got)
	}
	if got := job.Agent.Environment["CAPSULE_EXPERIMENT_MANIFEST_DIR"]; got != directory {
		t.Fatalf("manifest directory environment = %q", got)
	}
	if got := job.Agent.Environment["CAPSULE_EXPERIMENT_WORKSPACE"]; got != workspace {
		t.Fatalf("manifest workspace environment = %q", got)
	}

	staging := t.TempDir()
	t.Setenv("CAPSULE_EXPERIMENT_WORKSPACE", workspace)
	t.Setenv("CAPSULE_EXPERIMENT_MANIFEST_DIR", directory)
	if err := inspectManifest(staging); err != nil {
		t.Fatal(err)
	}
	var result ManifestResult
	readJSONFile(t, filepath.Join(staging, "result.json"), &result)
	if err := result.Validate(); err != nil {
		t.Fatal(err)
	}
	if result.Directory != "configured" || result.ManifestFile != "configured/"+ManifestFilename || result.DatasetPath != "configured/classification.csv" {
		t.Fatalf("manifest paths = %+v", result)
	}
	digest := sha256.Sum256([]byte(validManifestFixture))
	if result.ManifestSHA256 != fmt.Sprintf("%x", digest[:]) {
		t.Fatalf("manifest digest = %q", result.ManifestSHA256)
	}
	if len(result.Methods) != 3 || result.Methods[0].Method != MethodLogisticRegression || result.Methods[1].Method != MethodRandomForest || result.Methods[2].Method != MethodSVM {
		t.Fatalf("normalized methods = %+v", result.Methods)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("directory entries = %+v", result.Entries)
	}
	encoded := fmt.Sprintf("%+v", result)
	if strings.Contains(encoded, workspace) {
		t.Fatalf("manifest result exposed absolute workspace path: %s", encoded)
	}
}

func TestManifestInspectionRejectsMalformedOrUnallowlistedConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name: "unknown field",
			manifest: `{"version":1,"dataset":"classification.csv","methods":[` +
				`{"method":"logistic_regression"},{"method":"random_forest","n_estimators":100},{"method":"svm"}],"command":"sh"}`,
			want: "unknown field",
		},
		{
			name: "unsupported method",
			manifest: `{"version":1,"dataset":"classification.csv","methods":[` +
				`{"method":"logistic_regression"},{"method":"random_forest","n_estimators":100},{"method":"neural_network"}]}`,
			want: "unsupported experiment method",
		},
		{
			name: "duplicate method",
			manifest: `{"version":1,"dataset":"classification.csv","methods":[` +
				`{"method":"logistic_regression"},{"method":"random_forest","n_estimators":100},{"method":"random_forest","n_estimators":90}]}`,
			want: "duplicated",
		},
		{
			name: "wrong parameter owner",
			manifest: `{"version":1,"dataset":"classification.csv","methods":[` +
				`{"method":"logistic_regression","n_estimators":3},{"method":"random_forest","n_estimators":100},{"method":"svm"}]}`,
			want: "only valid for random_forest",
		},
		{
			name: "non integer estimator",
			manifest: `{"version":1,"dataset":"classification.csv","methods":[` +
				`{"method":"logistic_regression"},{"method":"random_forest","n_estimators":1.5},{"method":"svm"}]}`,
			want: "cannot unmarshal number",
		},
		{
			name:     "trailing object",
			manifest: validManifestFixture + ` {"version":1}`,
			want:     "multiple JSON values",
		},
		{
			name:     "internal dataset traversal",
			manifest: strings.Replace(validManifestFixture, `"classification.csv"`, `"nested/../classification.csv"`, 1),
			want:     "normalized without path traversal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			directory := filepath.Join(workspace, "configured")
			writeManifestFixture(t, directory, test.manifest)
			staging := t.TempDir()
			t.Setenv("CAPSULE_EXPERIMENT_WORKSPACE", workspace)
			t.Setenv("CAPSULE_EXPERIMENT_MANIFEST_DIR", directory)
			err := inspectManifest(staging)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("inspect error = %v; want %q", err, test.want)
			}
			if _, statErr := os.Stat(filepath.Join(staging, "result.json")); !os.IsNotExist(statErr) {
				t.Fatalf("invalid manifest produced result.json: %v", statErr)
			}
		})
	}
}

func TestManifestInspectionRejectsTraversalSymlinksAndBounds(t *testing.T) {
	t.Run("missing manifest", func(t *testing.T) {
		workspace := t.TempDir()
		directory := filepath.Join(workspace, "configured")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		assertManifestInspectError(t, workspace, directory, ManifestFilename)
	})

	t.Run("directory traversal", func(t *testing.T) {
		workspace := t.TempDir()
		outside := t.TempDir()
		registry, err := NewRegistry(RegistrationOptions{WorkspaceRoot: workspace})
		if err != nil {
			t.Fatal(err)
		}
		_, err = registry.Build(context.Background(), manifestTaskForPath(filepath.Join("..", filepath.Base(outside))))
		if err == nil || !strings.Contains(err.Error(), "traversal") {
			t.Fatalf("directory traversal error = %v", err)
		}
	})

	t.Run("directory symlink", func(t *testing.T) {
		workspace := t.TempDir()
		target := filepath.Join(workspace, "target")
		writeManifestFixture(t, target, validManifestFixture)
		if err := os.Symlink(target, filepath.Join(workspace, "linked")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		registry, err := NewRegistry(RegistrationOptions{WorkspaceRoot: workspace})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Build(context.Background(), manifestTaskForPath("linked")); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("directory symlink error = %v", err)
		}
	})

	t.Run("dataset traversal", func(t *testing.T) {
		workspace := t.TempDir()
		directory := filepath.Join(workspace, "configured")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "outside.csv"), []byte("x,label\n1,A\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest := strings.Replace(validManifestFixture, `"classification.csv"`, `"../outside.csv"`, 1)
		if err := os.WriteFile(filepath.Join(directory, ManifestFilename), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		assertManifestInspectError(t, workspace, directory, "traversal")
	})

	t.Run("dataset symlink", func(t *testing.T) {
		workspace := t.TempDir()
		directory := filepath.Join(workspace, "configured")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside.csv")
		if err := os.WriteFile(outside, []byte("x,label\n1,A\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(directory, "classification.csv")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.WriteFile(filepath.Join(directory, ManifestFilename), []byte(validManifestFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		assertManifestInspectError(t, workspace, directory, "symbolic link")
	})

	t.Run("oversized manifest", func(t *testing.T) {
		workspace := t.TempDir()
		directory := filepath.Join(workspace, "configured")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, ManifestFilename), make([]byte, MaximumManifestBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		assertManifestInspectError(t, workspace, directory, "byte limit")
	})

	t.Run("oversized dataset", func(t *testing.T) {
		workspace := t.TempDir()
		directory := filepath.Join(workspace, "configured")
		writeManifestFixture(t, directory, validManifestFixture)
		if err := os.Truncate(filepath.Join(directory, "classification.csv"), MaximumDatasetBytes+1); err != nil {
			t.Fatal(err)
		}
		assertManifestInspectError(t, workspace, directory, "byte limit")
	})

	t.Run("bounded directory entries", func(t *testing.T) {
		workspace := t.TempDir()
		directory := filepath.Join(workspace, "configured")
		writeManifestFixture(t, directory, validManifestFixture)
		for index := 0; index < MaximumManifestEntries; index++ {
			name := filepath.Join(directory, fmt.Sprintf("entry-%03d.txt", index))
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		assertManifestInspectError(t, workspace, directory, "entry inspection limit")
	})
}

func manifestTaskForPath(path string) planner.Task {
	return planner.Task{
		ID: "manifest", Name: "manifest", Description: "inspect manifest",
		Capability: CapabilityManifestInspect, Arguments: map[string]any{"path": path},
	}
}

func writeManifestFixture(t *testing.T, directory, manifest string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "classification.csv"), []byte("x,label\n1,A\n2,B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ManifestFilename), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertManifestInspectError(t *testing.T, workspace, directory, contains string) {
	t.Helper()
	staging := t.TempDir()
	t.Setenv("CAPSULE_EXPERIMENT_WORKSPACE", workspace)
	t.Setenv("CAPSULE_EXPERIMENT_MANIFEST_DIR", directory)
	err := inspectManifest(staging)
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("manifest inspection error = %v; want %q", err, contains)
	}
}
