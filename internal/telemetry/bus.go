package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var ErrBusClosed = errors.New("telemetry bus is closed")

// Publisher receives Runtime events.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// Sink persists or indexes events.
type Sink interface {
	WriteEvent(ctx context.Context, event Event) error
}

// NopPublisher discards events.
type NopPublisher struct{}

// Publish implements Publisher.
func (NopPublisher) Publish(
	_ context.Context,
	_ Event,
) error {
	return nil
}

// Bus asynchronously fans events out to one or more sinks.
type Bus struct {
	mu     sync.RWMutex
	queue  chan Event
	sinks  []Sink
	closed bool

	closeOnce sync.Once
	done      chan struct{}

	sequence   atomic.Uint64
	published  atomic.Uint64
	delivered  atomic.Uint64
	sinkErrors atomic.Uint64

	errorMu sync.Mutex
	errors  []error
}

// BusStats describes event-pipeline state.
type BusStats struct {
	Published     uint64 `json:"published"`
	Delivered     uint64 `json:"delivered"`
	SinkErrors    uint64 `json:"sink_errors"`
	LastSequence  uint64 `json:"last_sequence"`
	QueueDepth    int    `json:"queue_depth"`
	QueueCapacity int    `json:"queue_capacity"`
}

// NewBus starts one asynchronous event bus.
func NewBus(
	bufferSize int,
	sinks ...Sink,
) (*Bus, error) {
	if bufferSize <= 0 {
		bufferSize = 1024
	}

	for index, sink := range sinks {
		if sink == nil {
			return nil, fmt.Errorf(
				"event sink %d is nil",
				index,
			)
		}
	}

	bus := &Bus{
		queue: make(chan Event, bufferSize),
		sinks: append([]Sink(nil), sinks...),
		done:  make(chan struct{}),
	}

	go bus.run()

	return bus, nil
}

// Publish enqueues one immutable Runtime event.
func (b *Bus) Publish(
	ctx context.Context,
	event Event,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return ErrBusClosed
	}

	sequence := b.sequence.Add(1)

	event.Sequence = sequence
	event.ID = fmt.Sprintf(
		"evt-%020d",
		sequence,
	)

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	event = cloneEvent(event)

	select {
	case b.queue <- event:
		b.published.Add(1)
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns a point-in-time bus status.
func (b *Bus) Stats() BusStats {
	return BusStats{
		Published:     b.published.Load(),
		Delivered:     b.delivered.Load(),
		SinkErrors:    b.sinkErrors.Load(),
		LastSequence:  b.sequence.Load(),
		QueueDepth:    len(b.queue),
		QueueCapacity: cap(b.queue),
	}
}

// Error returns accumulated sink failures.
func (b *Bus) Error() error {
	b.errorMu.Lock()
	defer b.errorMu.Unlock()

	if len(b.errors) == 0 {
		return nil
	}

	copied := append([]error(nil), b.errors...)
	return errors.Join(copied...)
}

// Close drains queued events and closes closeable sinks.
func (b *Bus) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		close(b.queue)
		b.mu.Unlock()
	})

	select {
	case <-b.done:
		return b.Error()

	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Bus) run() {
	defer close(b.done)

	for event := range b.queue {
		for _, sink := range b.sinks {
			if err := sink.WriteEvent(
				context.Background(),
				event,
			); err != nil {
				b.recordError(err)
			}
		}

		b.delivered.Add(1)
	}

	for _, sink := range b.sinks {
		closer, ok := sink.(interface {
			Close() error
		})
		if !ok {
			continue
		}

		if err := closer.Close(); err != nil {
			b.recordError(err)
		}
	}
}

func (b *Bus) recordError(err error) {
	if err == nil {
		return
	}

	b.sinkErrors.Add(1)

	b.errorMu.Lock()
	b.errors = append(b.errors, err)
	b.errorMu.Unlock()
}
