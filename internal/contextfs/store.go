package contextfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	algorithm       = "sha256"
	digestHexLength = sha256.Size * 2
)

// Store is a persistent content-addressed context store.
type Store struct {
	root        string
	blobsDir    string
	metadataDir string
	refsDir     string
	tmpDir      string

	mu sync.RWMutex
}

// Object describes one immutable context object.
type Object struct {
	Algorithm    string    `json:"algorithm"`
	Digest       string    `json:"digest"`
	SizeBytes    uint64    `json:"size_bytes"`
	Path         string    `json:"path"`
	CreatedAt    time.Time `json:"created_at"`
	Deduplicated bool      `json:"deduplicated"`
}

// Reference maps one logical name to one immutable object.
type Reference struct {
	Name      string    `json:"name"`
	Algorithm string    `json:"algorithm"`
	Digest    string    `json:"digest"`
	SizeBytes uint64    `json:"size_bytes"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Stats summarizes the current store.
type Stats struct {
	Objects    uint64 `json:"objects"`
	Bytes      uint64 `json:"bytes"`
	References uint64 `json:"references"`
}

// GCReport describes one garbage-collection pass.
type GCReport struct {
	RemovedObjects  uint64 `json:"removed_objects"`
	RemovedBytes    uint64 `json:"removed_bytes"`
	RetainedObjects uint64 `json:"retained_objects"`
}

type metadata struct {
	Algorithm string    `json:"algorithm"`
	Digest    string    `json:"digest"`
	SizeBytes uint64    `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// Open initializes a ContextFS store under root.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("ContextFS root is required")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve ContextFS root: %w", err)
	}

	store := &Store{
		root:        absoluteRoot,
		blobsDir:    filepath.Join(absoluteRoot, "blobs", algorithm),
		metadataDir: filepath.Join(absoluteRoot, "metadata", algorithm),
		refsDir:     filepath.Join(absoluteRoot, "refs"),
		tmpDir:      filepath.Join(absoluteRoot, "tmp"),
	}

	for _, path := range []string{
		store.blobsDir,
		store.metadataDir,
		store.refsDir,
		store.tmpDir,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, fmt.Errorf(
				"create ContextFS directory %s: %w",
				path,
				err,
			)
		}
	}

	return store, nil
}

// Root returns the absolute store root.
func (s *Store) Root() string {
	return s.root
}

// PutBytes stores an in-memory context object.
func (s *Store) PutBytes(
	ctx context.Context,
	data []byte,
) (Object, error) {
	return s.Put(ctx, bytes.NewReader(data))
}

// PutFile stores the contents of one file.
func (s *Store) PutFile(
	ctx context.Context,
	path string,
) (Object, error) {
	file, err := os.Open(path)
	if err != nil {
		return Object{}, fmt.Errorf("open context file: %w", err)
	}
	defer file.Close()

	return s.Put(ctx, file)
}

// Put streams an immutable object into the content-addressed store.
func (s *Store) Put(
	ctx context.Context,
	source io.Reader,
) (Object, error) {
	if source == nil {
		return Object{}, fmt.Errorf("context source is required")
	}

	temporary, err := os.CreateTemp(s.tmpDir, "put-*")
	if err != nil {
		return Object{}, fmt.Errorf(
			"create temporary context object: %w",
			err,
		)
	}

	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	hasher := sha256.New()

	written, copyErr := copyWithContext(
		ctx,
		io.MultiWriter(temporary, hasher),
		source,
	)
	if copyErr != nil {
		_ = temporary.Close()

		return Object{}, fmt.Errorf(
			"write temporary context object: %w",
			copyErr,
		)
	}

	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Object{}, fmt.Errorf("sync temporary object: %w", err)
	}

	// Context objects are immutable after publication.
	if err := temporary.Chmod(0o444); err != nil {
		_ = temporary.Close()
		return Object{}, fmt.Errorf("protect temporary object: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return Object{}, fmt.Errorf("close temporary object: %w", err)
	}

	digest := hex.EncodeToString(hasher.Sum(nil))
	blobPath := s.blobPath(digest)
	metadataPath := s.metadataPath(digest)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(blobPath), 0o755); err != nil {
		return Object{}, fmt.Errorf("create blob shard: %w", err)
	}

	if err := os.MkdirAll(
		filepath.Dir(metadataPath),
		0o755,
	); err != nil {
		return Object{}, fmt.Errorf("create metadata shard: %w", err)
	}

	deduplicated := false

	// A hard link atomically publishes the object without overwriting
	// an object already stored under the same digest.
	if err := os.Link(temporaryPath, blobPath); err != nil {
		if !errors.Is(err, fs.ErrExist) &&
			!errors.Is(err, syscall.EEXIST) {
			return Object{}, fmt.Errorf(
				"publish context object: %w",
				err,
			)
		}

		deduplicated = true
	} else if err := syncDir(filepath.Dir(blobPath)); err != nil {
		return Object{}, fmt.Errorf("sync blob shard: %w", err)
	}

	info, err := os.Stat(blobPath)
	if err != nil {
		return Object{}, fmt.Errorf(
			"stat published context object: %w",
			err,
		)
	}

	if uint64(info.Size()) != uint64(written) {
		return Object{}, fmt.Errorf(
			"stored object size mismatch: expected %d, got %d",
			written,
			info.Size(),
		)
	}

	meta, err := s.ensureMetadataLocked(
		digest,
		uint64(info.Size()),
	)
	if err != nil {
		return Object{}, err
	}

	return Object{
		Algorithm:    algorithm,
		Digest:       digest,
		SizeBytes:    meta.SizeBytes,
		Path:         blobPath,
		CreatedAt:    meta.CreatedAt,
		Deduplicated: deduplicated,
	}, nil
}

// Resolve looks up one object by SHA-256 digest.
func (s *Store) Resolve(digest string) (Object, error) {
	if err := validateDigest(digest); err != nil {
		return Object{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.resolveLocked(digest)
}

// OpenObject opens an immutable object for reading.
func (s *Store) OpenObject(
	digest string,
) (*os.File, Object, error) {
	object, err := s.Resolve(digest)
	if err != nil {
		return nil, Object{}, err
	}

	file, err := os.Open(object.Path)
	if err != nil {
		return nil, Object{}, fmt.Errorf(
			"open context object: %w",
			err,
		)
	}

	return file, object, nil
}

// Bind atomically maps one logical name to an object.
func (s *Store) Bind(
	name string,
	digest string,
) (Reference, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		return Reference{}, fmt.Errorf("reference name is required")
	}

	if err := validateDigest(digest); err != nil {
		return Reference{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	object, err := s.resolveLocked(digest)
	if err != nil {
		return Reference{}, fmt.Errorf(
			"resolve referenced object: %w",
			err,
		)
	}

	reference := Reference{
		Name:      name,
		Algorithm: algorithm,
		Digest:    digest,
		SizeBytes: object.SizeBytes,
		UpdatedAt: time.Now().UTC(),
	}

	data, err := json.MarshalIndent(reference, "", "  ")
	if err != nil {
		return Reference{}, fmt.Errorf("encode reference: %w", err)
	}

	if err := writeFileAtomic(
		s.refPath(name),
		data,
		0o644,
	); err != nil {
		return Reference{}, fmt.Errorf(
			"persist reference: %w",
			err,
		)
	}

	return reference, nil
}

// ResolveRef resolves one logical reference name.
func (s *Store) ResolveRef(name string) (Reference, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		return Reference{}, fmt.Errorf("reference name is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	reference, err := readReference(s.refPath(name))
	if err != nil {
		return Reference{}, err
	}

	if reference.Name != name {
		return Reference{}, fmt.Errorf(
			"reference hash collision for %q",
			name,
		)
	}

	if _, err := os.Stat(
		s.blobPath(reference.Digest),
	); err != nil {
		return Reference{}, fmt.Errorf(
			"referenced object is unavailable: %w",
			err,
		)
	}

	return reference, nil
}

// Release removes one logical reference.
// Missing references are not treated as errors.
func (s *Store) Release(name string) (bool, error) {
	name = strings.TrimSpace(name)

	if name == "" {
		return false, fmt.Errorf("reference name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.refPath(name)

	reference, err := readReference(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, err
	}

	if reference.Name != name {
		return false, fmt.Errorf(
			"reference hash collision for %q",
			name,
		)
	}

	if err := os.Remove(path); err != nil {
		return false, fmt.Errorf("remove reference: %w", err)
	}

	if err := syncDir(s.refsDir); err != nil {
		return false, fmt.Errorf(
			"sync reference directory: %w",
			err,
		)
	}

	return true, nil
}

// ListReferences returns references sorted by logical name.
func (s *Store) ListReferences() ([]Reference, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.listReferencesLocked()
}

// RefCount returns the number of references to one digest.
func (s *Store) RefCount(digest string) (uint64, error) {
	if err := validateDigest(digest); err != nil {
		return 0, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	references, err := s.listReferencesLocked()
	if err != nil {
		return 0, err
	}

	var count uint64

	for _, reference := range references {
		if reference.Digest == digest {
			count++
		}
	}

	return count, nil
}

// Stats reports unique objects, physical bytes, and references.
func (s *Store) Stats() (Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result Stats

	err := filepath.WalkDir(
		s.blobsDir,
		func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}

			if entry.IsDir() {
				return nil
			}

			if validateDigest(entry.Name()) != nil {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}

			result.Objects++
			result.Bytes += uint64(info.Size())

			return nil
		},
	)
	if err != nil {
		return Stats{}, fmt.Errorf(
			"scan ContextFS objects: %w",
			err,
		)
	}

	references, err := s.listReferencesLocked()
	if err != nil {
		return Stats{}, err
	}

	result.References = uint64(len(references))

	return result, nil
}

// GC removes objects that have no logical references.
func (s *Store) GC() (GCReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	references, err := s.listReferencesLocked()
	if err != nil {
		return GCReport{}, err
	}

	live := make(map[string]struct{}, len(references))

	for _, reference := range references {
		live[reference.Digest] = struct{}{}
	}

	var report GCReport

	err = filepath.WalkDir(
		s.blobsDir,
		func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}

			if entry.IsDir() {
				return nil
			}

			digest := entry.Name()

			if validateDigest(digest) != nil {
				return nil
			}

			if _, exists := live[digest]; exists {
				report.RetainedObjects++
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}

			if err := os.Remove(path); err != nil {
				return fmt.Errorf(
					"remove unreferenced object %s: %w",
					digest,
					err,
				)
			}

			metadataPath := s.metadataPath(digest)

			if err := os.Remove(metadataPath); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf(
					"remove metadata %s: %w",
					digest,
					err,
				)
			}

			report.RemovedObjects++
			report.RemovedBytes += uint64(info.Size())

			return nil
		},
	)
	if err != nil {
		return GCReport{}, fmt.Errorf(
			"garbage collect ContextFS: %w",
			err,
		)
	}

	return report, nil
}

func (s *Store) resolveLocked(
	digest string,
) (Object, error) {
	blobPath := s.blobPath(digest)

	info, err := os.Stat(blobPath)
	if err != nil {
		return Object{}, fmt.Errorf(
			"stat context object %s: %w",
			digest,
			err,
		)
	}

	meta, err := s.ensureMetadataLocked(
		digest,
		uint64(info.Size()),
	)
	if err != nil {
		return Object{}, err
	}

	return Object{
		Algorithm: algorithm,
		Digest:    digest,
		SizeBytes: meta.SizeBytes,
		Path:      blobPath,
		CreatedAt: meta.CreatedAt,
	}, nil
}

func (s *Store) ensureMetadataLocked(
	digest string,
	size uint64,
) (metadata, error) {
	path := s.metadataPath(digest)

	data, err := os.ReadFile(path)
	if err == nil {
		var existing metadata

		if json.Unmarshal(data, &existing) == nil &&
			existing.Algorithm == algorithm &&
			existing.Digest == digest &&
			existing.SizeBytes == size &&
			!existing.CreatedAt.IsZero() {
			return existing, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return metadata{}, fmt.Errorf(
			"read object metadata: %w",
			err,
		)
	}

	createdAt := time.Now().UTC()

	if info, statErr := os.Stat(
		s.blobPath(digest),
	); statErr == nil {
		createdAt = info.ModTime().UTC()
	}

	meta := metadata{
		Algorithm: algorithm,
		Digest:    digest,
		SizeBytes: size,
		CreatedAt: createdAt,
	}

	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return metadata{}, fmt.Errorf(
			"encode object metadata: %w",
			err,
		)
	}

	if err := writeFileAtomic(
		path,
		encoded,
		0o644,
	); err != nil {
		return metadata{}, fmt.Errorf(
			"persist object metadata: %w",
			err,
		)
	}

	return meta, nil
}

func (s *Store) listReferencesLocked() ([]Reference, error) {
	entries, err := os.ReadDir(s.refsDir)
	if err != nil {
		return nil, fmt.Errorf("read references: %w", err)
	}

	references := make([]Reference, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() ||
			filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		reference, err := readReference(
			filepath.Join(s.refsDir, entry.Name()),
		)
		if err != nil {
			return nil, err
		}

		references = append(references, reference)
	}

	sort.Slice(references, func(i, j int) bool {
		return references[i].Name < references[j].Name
	})

	return references, nil
}

func readReference(path string) (Reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Reference{}, fmt.Errorf(
			"read reference: %w",
			err,
		)
	}

	var reference Reference

	if err := json.Unmarshal(data, &reference); err != nil {
		return Reference{}, fmt.Errorf(
			"decode reference %s: %w",
			path,
			err,
		)
	}

	if reference.Name == "" {
		return Reference{}, fmt.Errorf(
			"reference %s has an empty name",
			path,
		)
	}

	if reference.Algorithm != algorithm {
		return Reference{}, fmt.Errorf(
			"reference %s uses unsupported algorithm %q",
			path,
			reference.Algorithm,
		)
	}

	if err := validateDigest(reference.Digest); err != nil {
		return Reference{}, fmt.Errorf(
			"reference %s: %w",
			path,
			err,
		)
	}

	return reference, nil
}

func (s *Store) blobPath(digest string) string {
	return filepath.Join(
		s.blobsDir,
		digest[:2],
		digest,
	)
}

func (s *Store) metadataPath(digest string) string {
	return filepath.Join(
		s.metadataDir,
		digest[:2],
		digest+".json",
	)
}

func (s *Store) refPath(name string) string {
	sum := sha256.Sum256([]byte(name))

	return filepath.Join(
		s.refsDir,
		hex.EncodeToString(sum[:])+".json",
	)
}

func validateDigest(digest string) error {
	if len(digest) != digestHexLength {
		return fmt.Errorf("invalid SHA-256 digest length")
	}

	decoded, err := hex.DecodeString(digest)

	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid SHA-256 digest")
	}

	return nil
}

func copyWithContext(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
) (int64, error) {
	buffer := make([]byte, 1024*1024)
	var written int64

	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}

		count, readErr := source.Read(buffer)

		if count > 0 {
			writeCount, writeErr := destination.Write(
				buffer[:count],
			)

			written += int64(writeCount)

			if writeErr != nil {
				return written, writeErr
			}

			if writeCount != count {
				return written, io.ErrShortWrite
			}
		}

		if errors.Is(readErr, io.EOF) {
			return written, nil
		}

		if readErr != nil {
			return written, readErr
		}
	}
}

func writeFileAtomic(
	path string,
	data []byte,
	mode fs.FileMode,
) error {
	directory := filepath.Dir(path)

	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, ".atomic-*")
	if err != nil {
		return err
	}

	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}

	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}

	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}

	if err := temporary.Close(); err != nil {
		return err
	}

	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}

	return syncDir(directory)
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()

	return directory.Sync()
}
