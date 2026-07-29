package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// JSONLSink durably appends one JSON object per line.
type JSONLSink struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	encoder *json.Encoder
	closed  bool
}

// OpenJSONLSink opens an append-only event log.
func OpenJSONLSink(path string) (*JSONLSink, error) {
	if path == "" {
		return nil, fmt.Errorf(
			"JSONL event path is required",
		)
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve event log path: %w",
			err,
		)
	}

	if err := os.MkdirAll(
		filepath.Dir(absolutePath),
		0o755,
	); err != nil {
		return nil, fmt.Errorf(
			"create event log directory: %w",
			err,
		)
	}

	file, err := os.OpenFile(
		absolutePath,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"open JSONL event log: %w",
			err,
		)
	}

	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)

	return &JSONLSink{
		path:    absolutePath,
		file:    file,
		encoder: encoder,
	}, nil
}

// Path returns the absolute JSONL path.
func (s *JSONLSink) Path() string {
	return s.path
}

// WriteEvent appends and synchronizes one event.
func (s *JSONLSink) WriteEvent(
	ctx context.Context,
	event Event,
) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New(
			"JSONL event sink is closed",
		)
	}

	if err := s.encoder.Encode(event); err != nil {
		return fmt.Errorf(
			"append JSONL event: %w",
			err,
		)
	}

	if err := s.file.Sync(); err != nil {
		return fmt.Errorf(
			"sync JSONL event log: %w",
			err,
		)
	}

	return nil
}

// Close synchronizes and closes the event file.
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	syncErr := s.file.Sync()
	closeErr := s.file.Close()

	return errors.Join(syncErr, closeErr)
}
