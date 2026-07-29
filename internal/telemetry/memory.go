package telemetry

import (
	"context"
	"sync"
)

// MemorySink retains recent events for Runtime API queries.
type MemorySink struct {
	mu        sync.RWMutex
	maxEvents int
	events    []Event
}

// NewMemorySink creates a bounded in-memory event store.
//
// A maximum of zero means unlimited retention.
func NewMemorySink(maxEvents int) *MemorySink {
	return &MemorySink{
		maxEvents: maxEvents,
	}
}

// WriteEvent stores one immutable event.
func (s *MemorySink) WriteEvent(
	_ context.Context,
	event Event,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events = append(
		s.events,
		cloneEvent(event),
	)

	if s.maxEvents > 0 &&
		len(s.events) > s.maxEvents {
		removeCount :=
			len(s.events) - s.maxEvents

		copy(
			s.events,
			s.events[removeCount:],
		)

		s.events = s.events[:s.maxEvents]
	}

	return nil
}

// Snapshot returns all retained events.
func (s *MemorySink) Snapshot() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Event, len(s.events))

	for index, event := range s.events {
		result[index] = cloneEvent(event)
	}

	return result
}

// Since returns events whose sequence is greater than sequence.
func (s *MemorySink) Since(
	sequence uint64,
) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Event, 0)

	for _, event := range s.events {
		if event.Sequence <= sequence {
			continue
		}

		result = append(
			result,
			cloneEvent(event),
		)
	}

	return result
}

// Len returns the retained event count.
func (s *MemorySink) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.events)
}
