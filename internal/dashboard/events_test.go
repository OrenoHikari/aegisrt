package dashboard

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aegisrt/internal/telemetry"
)

func TestEventStoreOrderingIsolationAndMalformedInput(t *testing.T) {
	first := NewEventStore("run-a", nil)
	second := NewEventStore("run-b", nil)
	line := telemetryLine(t, 1, telemetry.KindPlanCreated, "", "", map[string]any{"version": 1})
	event, err := first.AddRaw(line)
	if err != nil {
		t.Fatal(err)
	}
	if event.DashboardRunID != "run-a" || len(second.Snapshot(0)) != 0 {
		t.Fatalf("event leaked across run stores: %+v", event)
	}
	if _, err := first.AddRaw(line); !errors.Is(err, ErrEventOutOfOrder) {
		t.Fatalf("expected out-of-order rejection, got %v", err)
	}
	if _, err := first.AddRaw([]byte("not-json\n")); !errors.Is(err, ErrMalformedTelemetry) {
		t.Fatalf("expected malformed rejection, got %v", err)
	}
	if first.MalformedCount() != 1 {
		t.Fatalf("malformed count = %d", first.MalformedCount())
	}
}

func TestEventTailerWaitsForCompleteLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "events.jsonl")
	store := NewEventStore("run", nil)
	tailer := &EventTailer{Path: path, Store: store}
	line := telemetryLine(t, 1, telemetry.KindPlanCreated, "", "", map[string]any{"version": 1})
	if err := os.WriteFile(path, line[:len(line)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tailer.ReadAvailable(); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot(0)) != 0 {
		t.Fatal("tailer consumed a partial JSONL record")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{'\n'}); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := tailer.ReadAvailable(); err != nil {
		t.Fatal(err)
	}
	if len(store.Snapshot(0)) != 1 {
		t.Fatal("tailer did not consume completed record")
	}
}

func telemetryLine(t *testing.T, sequence uint64, kind telemetry.Kind, taskID, phase string, data any) []byte {
	t.Helper()
	event, err := telemetry.NewEvent(kind, "test", taskID, phase, data)
	if err != nil {
		t.Fatal(err)
	}
	event.ID = "event-test"
	event.Sequence = sequence
	event.Timestamp = time.Unix(int64(sequence), 0).UTC()
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return append(line, '\n')
}
