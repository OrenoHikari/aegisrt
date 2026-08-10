package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aegisrt/internal/planner"
	"aegisrt/internal/scheduler"
)

func TestSuccessObservationUsesVerifiedStructuredOutput(t *testing.T) {
	commit := t.TempDir()
	if err := os.WriteFile(filepath.Join(commit, "result.json"), []byte(`{"exists":false,"kind":"missing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commit, "result.txt"), []byte("path is missing"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Second)
	finish := time.Now()
	observation := observationFromRecord("run", 1, planner.Task{
		ID: "probe", Capability: "filesystem.stat",
	}, scheduler.Record{
		ID: "probe", Phase: scheduler.PhaseSucceeded, StartedAt: &start, FinishedAt: &finish,
		OutputVerified: true, OutputCommitted: true, OutputCommitPath: commit, OutputFileCount: 2,
	}, false)
	if !observation.Success || observation.Output["exists"] != false || observation.OutputSummary != "path is missing" {
		t.Fatalf("unexpected success observation: %+v", observation)
	}
	if observation.Metadata.Duration <= 0 || !observation.Metadata.OutputVerified {
		t.Fatalf("missing real execution metadata: %+v", observation.Metadata)
	}
}

func TestFailureObservationIncludesErrorAndExit(t *testing.T) {
	exit := 7
	observation := observationFromRecord("run", 2, planner.Task{
		ID: "bad", Capability: "data.inspect",
	}, scheduler.Record{
		ID: "bad", Phase: scheduler.PhaseFailed, Error: "unsupported format", ExitCode: &exit,
	}, false)
	if observation.Success || observation.Error != "unsupported format" || observation.ExitCode == nil || *observation.ExitCode != 7 {
		t.Fatalf("unexpected failure observation: %+v", observation)
	}
}

func TestLargePaperParseObservationUsesBoundedProjection(t *testing.T) {
	commit := t.TempDir()
	largeText := strings.Repeat("paper text ", 12*1024)
	result := map[string]any{
		"paper":  map[string]any{"id": "paper-1", "title": "Paper One"},
		"parser": "python-pypdf", "characters": len(largeText),
		"pages":       []any{map[string]any{"number": 1, "text": largeText, "start": 0, "end": len(largeText)}},
		"sections":    []any{map[string]any{"id": "section-1", "heading": "Method", "text": largeText, "page_start": 1, "page_end": 1}},
		"diagnostics": map[string]any{"selected": "python-pypdf", "page_count": 1, "detected_sections": 1},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= maximumObservationJSONBytes {
		t.Fatal("test fixture must exceed the cognitive observation limit")
	}
	if err := os.WriteFile(filepath.Join(commit, "result.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	observation := observationFromRecord("run", 1, planner.Task{ID: "parse", Capability: "paper.parse"}, scheduler.Record{
		ID: "parse", Phase: scheduler.PhaseSucceeded, OutputVerified: true, OutputCommitPath: commit,
	}, false)
	sections, _ := observation.Output["sections"].([]any)
	if observation.Output["parser"] != "python-pypdf" || len(sections) != 1 {
		t.Fatalf("parse diagnostics were lost: %+v", observation.Output)
	}
	projected, err := json.Marshal(observation.Output)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) > maximumObservationJSONBytes || strings.Contains(string(projected), largeText[:2048]) {
		t.Fatalf("paper text leaked into bounded observation (%d bytes)", len(projected))
	}
}
