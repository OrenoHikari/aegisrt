package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"aegisrt/internal/contextfs"
	"aegisrt/internal/planner"
)

func TestEnvironmentCapabilitiesExistingMissingAndListing(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sales.json"), []byte(`[{"sales":12}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := newEnvironmentTestRegistry(t, root)

	existing := runEnvironmentTask(t, registry, planner.Task{
		ID: "stat-existing", Name: "stat", Description: "stat", Capability: "filesystem.stat",
		Arguments: map[string]any{"path": "sales.json"},
	})
	if existing["exists"] != true || existing["kind"] != "file" {
		t.Fatalf("unexpected existing stat: %+v", existing)
	}
	missing := runEnvironmentTask(t, registry, planner.Task{
		ID: "stat-missing", Name: "stat", Description: "stat", Capability: "filesystem.stat",
		Arguments: map[string]any{"path": "missing.csv"},
	})
	if missing["exists"] != false || missing["kind"] != "missing" {
		t.Fatalf("unexpected missing stat: %+v", missing)
	}
	listing := runEnvironmentTask(t, registry, planner.Task{
		ID: "list", Name: "list", Description: "list", Capability: "filesystem.list",
		Arguments: map[string]any{"path": "."},
	})
	entries, ok := listing["entries"].([]any)
	if !ok || len(entries) != 1 || entries[0].(map[string]any)["name"] != "sales.json" {
		t.Fatalf("unexpected directory listing: %+v", listing)
	}
}

func TestEnvironmentCapabilityRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	registry := newEnvironmentTestRegistry(t, root)
	for _, path := range []string{"../outside", filepath.Join(root, "..", "outside")} {
		_, err := registry.Build(context.Background(), planner.Task{
			ID: "escape", Name: "escape", Description: "escape", Capability: "filesystem.stat",
			Arguments: map[string]any{"path": path},
		})
		if err == nil || !strings.Contains(err.Error(), "escapes configured root") {
			t.Fatalf("expected traversal rejection for %q, got %v", path, err)
		}
	}
}

func TestEnvironmentCapabilityRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	registry := newEnvironmentTestRegistry(t, root)
	_, err := registry.Build(context.Background(), planner.Task{
		ID: "escape", Name: "escape", Description: "escape", Capability: "filesystem.stat",
		Arguments: map[string]any{"path": "outside-link/secret.txt"},
	})
	if err == nil || !strings.Contains(err.Error(), "escapes configured root") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func newEnvironmentTestRegistry(t *testing.T, root string) *Registry {
	t.Helper()
	store, err := contextfs.Open(filepath.Join(t.TempDir(), "contextfs"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewBuiltinRegistry(BuiltinOptions{
		WorkerPath:   filepath.Join("..", "..", "worker", "python", "cognitive_agent.py"),
		ContextStore: store,
		InputRoot:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func runEnvironmentTask(t *testing.T, registry *Registry, task planner.Task) map[string]any {
	t.Helper()
	job, err := registry.Build(context.Background(), task)
	if err != nil {
		t.Fatalf("build %s: %v", task.ID, err)
	}
	staging := t.TempDir()
	command := exec.Command(job.Agent.Command, job.Agent.Args...)
	command.Env = os.Environ()
	for name, value := range job.Agent.Environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Env = append(command.Env, "AEGIS_OUTPUT_STAGING="+staging)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("execute %s: %v\n%s", task.ID, err, output)
	}
	data, err := os.ReadFile(filepath.Join(staging, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
