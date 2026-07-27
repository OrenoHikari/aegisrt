package contextstore

import (
	"fmt"
	"strings"

	"aegisrt/internal/contextfs"
)

// Resolver converts submitted context requirements into authoritative
// immutable object identities and sizes.
type Resolver interface {
	Resolve(refs []Ref) ([]Ref, error)
}

// PassthroughResolver preserves compatibility with callers that
// already provide context sizes and optional digests.
type PassthroughResolver struct{}

// Resolve validates directly supplied references.
func (PassthroughResolver) Resolve(
	refs []Ref,
) ([]Ref, error) {
	return NormalizeRefs(refs)
}

// ContextFSResolver resolves logical reference names through ContextFS.
type ContextFSResolver struct {
	store *contextfs.Store
}

// NewContextFSResolver creates a ContextFS-backed resolver.
func NewContextFSResolver(
	store *contextfs.Store,
) (*ContextFSResolver, error) {
	if store == nil {
		return nil, fmt.Errorf("ContextFS store is required")
	}

	return &ContextFSResolver{
		store: store,
	}, nil
}

// Resolve converts logical ContextFS names into SHA-256 identities.
//
// Callers may submit:
//
//	Ref{Key: "dataset://shared-corpus"}
//
// without manually supplying Digest or SizeBytes.
func (r *ContextFSResolver) Resolve(
	refs []Ref,
) ([]Ref, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	resolved := make([]Ref, 0, len(refs))

	for _, submitted := range refs {
		key := strings.TrimSpace(submitted.Key)
		digest := strings.ToLower(
			strings.TrimSpace(submitted.Digest),
		)

		if digest != "" {
			object, err := r.store.Resolve(digest)
			if err != nil {
				return nil, fmt.Errorf(
					"resolve ContextFS digest %s: %w",
					digest,
					err,
				)
			}

			if key == "" {
				key = "sha256:" + digest
			}

			resolved = append(resolved, Ref{
				Key:       key,
				Digest:    object.Digest,
				SizeBytes: object.SizeBytes,
			})

			continue
		}

		if key == "" {
			return nil, fmt.Errorf(
				"context reference name is required",
			)
		}

		reference, err := r.store.ResolveRef(key)
		if err != nil {
			return nil, fmt.Errorf(
				"resolve ContextFS reference %q: %w",
				key,
				err,
			)
		}

		resolved = append(resolved, Ref{
			Key:       key,
			Digest:    reference.Digest,
			SizeBytes: reference.SizeBytes,
		})
	}

	return NormalizeRefs(resolved)
}
