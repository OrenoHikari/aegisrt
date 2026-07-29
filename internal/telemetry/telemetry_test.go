package telemetry

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBusPreservesOrderAndPersistsEvents(
	t *testing.T,
) {
	memory := NewMemorySink(100)

	jsonl, err := OpenJSONLSink(
		filepath.Join(
			t.TempDir(),
			"events.jsonl",
		),
	)
	if err != nil {
		t.Fatalf("open JSONL sink: %v", err)
	}

	bus, err := NewBus(
		16,
		memory,
		jsonl,
	)
	if err != nil {
		t.Fatalf("create event bus: %v", err)
	}

	for index := 0; index < 3; index++ {
		event, err := NewEvent(
			KindAgentSubmitted,
			"scheduler",
			"agent-test",
			"QUEUED",
			map[string]any{
				"index": index,
			},
		)
		if err != nil {
			t.Fatalf("create event: %v", err)
		}

		if err := bus.Publish(
			context.Background(),
			event,
		); err != nil {
			t.Fatalf("publish event: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	if err := bus.Close(ctx); err != nil {
		t.Fatalf("close event bus: %v", err)
	}

	events := memory.Snapshot()

	if len(events) != 3 {
		t.Fatalf(
			"expected three events, got %d",
			len(events),
		)
	}

	for index, event := range events {
		expectedSequence := uint64(index + 1)

		if event.Sequence != expectedSequence {
			t.Fatalf(
				"expected sequence %d, got %d",
				expectedSequence,
				event.Sequence,
			)
		}

		if event.ID == "" ||
			event.Timestamp.IsZero() {
			t.Fatal(
				"event identity or timestamp is missing",
			)
		}
	}

	file, err := os.Open(jsonl.Path())
	if err != nil {
		t.Fatalf("open persisted event log: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	persisted := 0

	for scanner.Scan() {
		var event Event

		if err := json.Unmarshal(
			scanner.Bytes(),
			&event,
		); err != nil {
			t.Fatalf(
				"decode persisted event: %v",
				err,
			)
		}

		persisted++
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scan event log: %v", err)
	}

	if persisted != 3 {
		t.Fatalf(
			"expected three persisted events, got %d",
			persisted,
		)
	}
}

func TestMemorySinkBoundsRetention(t *testing.T) {
	sink := NewMemorySink(2)

	for sequence := uint64(1); sequence <= 3; sequence++ {
		if err := sink.WriteEvent(
			context.Background(),
			Event{
				ID:       "event",
				Sequence: sequence,
			},
		); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}

	events := sink.Snapshot()

	if len(events) != 2 {
		t.Fatalf(
			"expected two retained events, got %d",
			len(events),
		)
	}

	if events[0].Sequence != 2 ||
		events[1].Sequence != 3 {
		t.Fatalf(
			"unexpected retained sequences: %d, %d",
			events[0].Sequence,
			events[1].Sequence,
		)
	}
}
