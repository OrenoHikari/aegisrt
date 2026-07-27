package contextstore

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Ref describes one context object required by an Agent.
type Ref struct {
	Key       string `json:"key"`
	SizeBytes uint64 `json:"size_bytes"`
}

// Entry describes one context object currently considered resident.
type Entry struct {
	Key        string    `json:"key"`
	SizeBytes  uint64    `json:"size_bytes"`
	Hits       uint64    `json:"hits"`
	AddedAt    time.Time `json:"added_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Snapshot is an immutable view of the context registry.
type Snapshot struct {
	Timestamp  time.Time        `json:"timestamp"`
	Entries    map[string]Entry `json:"entries"`
	TotalBytes uint64           `json:"total_bytes"`
}

// Registry tracks warm context objects.
//
// M3-C stores metadata only. ContextFS will later own the actual data.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// NewRegistry creates an empty context registry.
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]Entry),
	}
}

// NormalizeRefs validates, deduplicates, and sorts context references.
func NormalizeRefs(refs []Ref) ([]Ref, error) {
	sizes := make(map[string]uint64)

	for _, ref := range refs {
		key := strings.TrimSpace(ref.Key)

		if key == "" {
			return nil, fmt.Errorf("context key is required")
		}

		if ref.SizeBytes == 0 {
			return nil, fmt.Errorf(
				"context %q size must be greater than zero",
				key,
			)
		}

		if ref.SizeBytes > sizes[key] {
			sizes[key] = ref.SizeBytes
		}
	}

	keys := make([]string, 0, len(sizes))

	for key := range sizes {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	result := make([]Ref, 0, len(keys))

	for _, key := range keys {
		result = append(result, Ref{
			Key:       key,
			SizeBytes: sizes[key],
		})
	}

	return result, nil
}

// CloneRefs returns a copy of a reference slice.
func CloneRefs(refs []Ref) []Ref {
	if refs == nil {
		return nil
	}

	result := make([]Ref, len(refs))
	copy(result, refs)

	return result
}

// Add marks context objects as resident.
func (r *Registry) Add(refs []Ref) error {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ref := range normalized {
		entry, exists := r.entries[ref.Key]

		if !exists {
			entry = Entry{
				Key:     ref.Key,
				AddedAt: now,
			}
		}

		if ref.SizeBytes > entry.SizeBytes {
			entry.SizeBytes = ref.SizeBytes
		}

		entry.LastUsedAt = now
		r.entries[ref.Key] = entry
	}

	return nil
}

// Touch records reuse of already-resident context objects.
func (r *Registry) Touch(refs []Ref) error {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ref := range normalized {
		entry, exists := r.entries[ref.Key]
		if !exists {
			continue
		}

		entry.Hits++
		entry.LastUsedAt = now
		r.entries[ref.Key] = entry
	}

	return nil
}

// Snapshot returns a copy of the current registry state.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make(map[string]Entry, len(r.entries))
	var totalBytes uint64

	for key, entry := range r.entries {
		entries[key] = entry
		totalBytes += entry.SizeBytes
	}

	return Snapshot{
		Timestamp:  time.Now().UTC(),
		Entries:    entries,
		TotalBytes: totalBytes,
	}
}

// RequestedBytes returns the total unique context bytes requested.
func (s Snapshot) RequestedBytes(refs []Ref) uint64 {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return 0
	}

	var total uint64

	for _, ref := range normalized {
		total += ref.SizeBytes
	}

	return total
}

// ReusableBytes returns how many requested bytes are already resident.
func (s Snapshot) ReusableBytes(refs []Ref) uint64 {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return 0
	}

	var total uint64

	for _, ref := range normalized {
		entry, exists := s.Entries[ref.Key]
		if !exists {
			continue
		}

		reusable := ref.SizeBytes

		if entry.SizeBytes < reusable {
			reusable = entry.SizeBytes
		}

		total += reusable
	}

	return total
}

// Affinity returns the fraction of requested context already resident.
func (s Snapshot) Affinity(refs []Ref) float64 {
	requested := s.RequestedBytes(refs)
	if requested == 0 {
		return 0
	}

	reusable := s.ReusableBytes(refs)

	return float64(reusable) / float64(requested)
}
