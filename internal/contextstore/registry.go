package contextstore

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Ref describes one context object required by an Agent.
//
// Key is a logical ContextFS reference name.
// Digest is the immutable content identity after resolution.
// SizeBytes is authoritative after ContextFS resolution.
type Ref struct {
	Key       string `json:"key,omitempty"`
	Digest    string `json:"digest,omitempty"`
	SizeBytes uint64 `json:"size_bytes"`
}

// Identity returns the canonical identity used for affinity matching.
//
// Two different logical names with the same SHA-256 digest represent
// the same immutable context object.
func (r Ref) Identity() string {
	digest := strings.ToLower(strings.TrimSpace(r.Digest))

	if digest != "" {
		return "sha256:" + digest
	}

	return "key:" + strings.TrimSpace(r.Key)
}

// Entry describes one context object currently considered warm.
type Entry struct {
	Identity   string    `json:"identity"`
	Key        string    `json:"key,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	SizeBytes  uint64    `json:"size_bytes"`
	Hits       uint64    `json:"hits"`
	AddedAt    time.Time `json:"added_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Snapshot is an immutable view of the warm-context registry.
type Snapshot struct {
	Timestamp  time.Time        `json:"timestamp"`
	Entries    map[string]Entry `json:"entries"`
	TotalBytes uint64           `json:"total_bytes"`
}

// Catalog defines the warm-context operations required by Scheduler.
type Catalog interface {
	Add(refs []Ref) error
	Touch(refs []Ref) error
	Snapshot() Snapshot
}

// Registry tracks warm context identities.
//
// ContextFS owns the persistent object data. Registry tracks which
// resolved immutable objects are currently warm for scheduling.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

// NewRegistry creates an empty warm-context registry.
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]Entry),
	}
}

// NormalizeRefs validates, deduplicates, and sorts resolved references.
func NormalizeRefs(refs []Ref) ([]Ref, error) {
	byIdentity := make(map[string]Ref)

	for _, ref := range refs {
		ref.Key = strings.TrimSpace(ref.Key)
		ref.Digest = strings.ToLower(
			strings.TrimSpace(ref.Digest),
		)

		if ref.Key == "" && ref.Digest == "" {
			return nil, fmt.Errorf(
				"context key or digest is required",
			)
		}

		if ref.Digest != "" {
			if err := validateDigest(ref.Digest); err != nil {
				return nil, err
			}
		}

		if ref.SizeBytes == 0 {
			return nil, fmt.Errorf(
				"context %q size must be greater than zero",
				ref.Key,
			)
		}

		identity := ref.Identity()
		existing, exists := byIdentity[identity]

		if !exists {
			byIdentity[identity] = ref
			continue
		}

		// Preserve the authoritative maximum size and a stable
		// logical name when aliases share one digest.
		if ref.SizeBytes > existing.SizeBytes {
			existing.SizeBytes = ref.SizeBytes
		}

		if existing.Key == "" ||
			(ref.Key != "" && ref.Key < existing.Key) {
			existing.Key = ref.Key
		}

		byIdentity[identity] = existing
	}

	identities := make([]string, 0, len(byIdentity))

	for identity := range byIdentity {
		identities = append(identities, identity)
	}

	sort.Strings(identities)

	result := make([]Ref, 0, len(identities))

	for _, identity := range identities {
		result = append(result, byIdentity[identity])
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

// Add marks resolved context objects as warm.
func (r *Registry) Add(refs []Ref) error {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ref := range normalized {
		identity := ref.Identity()
		entry, exists := r.entries[identity]

		if !exists {
			entry = Entry{
				Identity: identity,
				Key:      ref.Key,
				Digest:   ref.Digest,
				AddedAt:  now,
			}
		}

		if ref.SizeBytes > entry.SizeBytes {
			entry.SizeBytes = ref.SizeBytes
		}

		if entry.Key == "" {
			entry.Key = ref.Key
		}

		if entry.Digest == "" {
			entry.Digest = ref.Digest
		}

		entry.LastUsedAt = now
		r.entries[identity] = entry
	}

	return nil
}

// Touch records reuse of already-warm context objects.
func (r *Registry) Touch(refs []Ref) error {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ref := range normalized {
		identity := ref.Identity()
		entry, exists := r.entries[identity]

		if !exists {
			continue
		}

		entry.Hits++
		entry.LastUsedAt = now
		r.entries[identity] = entry
	}

	return nil
}

// Snapshot returns a copy of the warm registry.
func (r *Registry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := make(map[string]Entry, len(r.entries))
	var totalBytes uint64

	for identity, entry := range r.entries {
		entries[identity] = entry
		totalBytes += entry.SizeBytes
	}

	return Snapshot{
		Timestamp:  time.Now().UTC(),
		Entries:    entries,
		TotalBytes: totalBytes,
	}
}

// RequestedBytes returns total unique requested context bytes.
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

// ReusableBytes returns requested bytes already warm.
func (s Snapshot) ReusableBytes(refs []Ref) uint64 {
	normalized, err := NormalizeRefs(refs)
	if err != nil {
		return 0
	}

	var total uint64

	for _, ref := range normalized {
		entry, exists := s.Entries[ref.Identity()]
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

// Affinity returns the fraction of requested context already warm.
func (s Snapshot) Affinity(refs []Ref) float64 {
	requested := s.RequestedBytes(refs)
	if requested == 0 {
		return 0
	}

	reusable := s.ReusableBytes(refs)

	return float64(reusable) / float64(requested)
}

func validateDigest(digest string) error {
	if len(digest) != 64 {
		return fmt.Errorf(
			"invalid SHA-256 digest length",
		)
	}

	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("invalid SHA-256 digest")
	}

	return nil
}
