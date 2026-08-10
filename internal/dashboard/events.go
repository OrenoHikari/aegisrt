package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"aegisrt/internal/telemetry"
)

const maximumTelemetryLineBytes = 4 * 1024 * 1024

var (
	ErrMalformedTelemetry = errors.New("malformed dashboard telemetry event")
	ErrEventOutOfOrder    = errors.New("dashboard telemetry event is out of order")
)

type EventStore struct {
	mu           sync.RWMutex
	runID        string
	events       []DashboardEvent
	lastSequence uint64
	malformed    uint64
	subscribers  map[uint64]chan DashboardEvent
	nextSubID    uint64
	onEvent      func(DashboardEvent)
}

func NewEventStore(runID string, onEvent func(DashboardEvent)) *EventStore {
	return &EventStore{runID: runID, subscribers: make(map[uint64]chan DashboardEvent), onEvent: onEvent}
}

func (s *EventStore) AddRaw(line []byte) (DashboardEvent, error) {
	var event telemetry.Event
	if err := json.Unmarshal(line, &event); err != nil || event.Kind == "" || event.Sequence == 0 {
		s.mu.Lock()
		s.malformed++
		s.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("kind and sequence are required")
		}
		return DashboardEvent{}, fmt.Errorf("%w: %v", ErrMalformedTelemetry, err)
	}
	view := DashboardEvent{
		DashboardRunID: s.runID, ID: event.ID, Sequence: event.Sequence, Timestamp: event.Timestamp,
		Kind: string(event.Kind), Source: event.Source, TaskID: event.AgentID, Phase: event.Phase, Data: decodeMap(event.Data),
	}

	s.mu.Lock()
	if view.Sequence <= s.lastSequence {
		s.mu.Unlock()
		return DashboardEvent{}, fmt.Errorf("%w: got %d after %d", ErrEventOutOfOrder, view.Sequence, s.lastSequence)
	}
	s.lastSequence = view.Sequence
	s.events = append(s.events, view)
	subscribers := make([]chan DashboardEvent, 0, len(s.subscribers))
	for _, subscriber := range s.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	s.mu.Unlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- view:
		default:
		}
	}
	if s.onEvent != nil {
		s.onEvent(view)
	}
	return view, nil
}

func (s *EventStore) Snapshot(after uint64) []DashboardEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]DashboardEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result
}

func (s *EventStore) Subscribe() (<-chan DashboardEvent, func()) {
	s.mu.Lock()
	s.nextSubID++
	id := s.nextSubID
	channel := make(chan DashboardEvent, 512)
	s.subscribers[id] = channel
	s.mu.Unlock()
	return channel, func() {
		s.mu.Lock()
		if _, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
		}
		s.mu.Unlock()
	}
}

func (s *EventStore) MalformedCount() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.malformed
}

type EventTailer struct {
	Path     string
	Store    *EventStore
	Interval time.Duration
	mu       sync.Mutex
	offset   int64
}

func (t *EventTailer) Run(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_ = t.ReadAvailable()
		select {
		case <-ctx.Done():
			_ = t.ReadAvailable()
			return
		case <-ticker.C:
		}
	}
}

func (t *EventTailer) ReadAvailable() error {
	if t.Store == nil {
		return fmt.Errorf("event store is required")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	file, err := os.Open(t.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(t.offset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	var joined error
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > maximumTelemetryLineBytes {
			joined = errors.Join(joined, fmt.Errorf("%w: line exceeds %d bytes", ErrMalformedTelemetry, maximumTelemetryLineBytes))
			t.offset += int64(len(line))
		} else if len(line) > 0 && line[len(line)-1] == '\n' {
			t.offset += int64(len(line))
			if _, err := t.Store.AddRaw(line); err != nil {
				joined = errors.Join(joined, err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return errors.Join(joined, readErr)
		}
	}
	return joined
}
