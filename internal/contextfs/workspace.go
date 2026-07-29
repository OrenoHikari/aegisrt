package contextfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const ficlone = 0x40049409

var safeAgentID = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
)

// AccessMode defines how an Agent may use a materialized context.
type AccessMode string

const (
	// AccessReadOnly creates an immutable Agent-local snapshot.
	AccessReadOnly AccessMode = "read-only"

	// AccessPrivate creates an Agent-local writable snapshot.
	AccessPrivate AccessMode = "private"
)

// MaterializeRequest describes one ContextFS object that should appear
// in an Agent workspace.
type MaterializeRequest struct {
	Name   string     `json:"name"`
	Digest string     `json:"digest"`
	Access AccessMode `json:"access"`
}

// MaterializedFile describes one file published into the workspace.
type MaterializedFile struct {
	Name       string     `json:"name"`
	Digest     string     `json:"digest"`
	SizeBytes  uint64     `json:"size_bytes"`
	Access     AccessMode `json:"access"`
	Path       string     `json:"path"`
	Method     string     `json:"method"`
	SourcePath string     `json:"source_path"`
}

// Workspace is one isolated Agent context directory.
type Workspace struct {
	AgentID     string             `json:"agent_id"`
	Root        string             `json:"root"`
	InputsDir   string             `json:"inputs_dir"`
	PrivateDir  string             `json:"private_dir"`
	Manifest    string             `json:"manifest"`
	Files       []MaterializedFile `json:"files"`
	CreatedAt   time.Time          `json:"created_at"`
	PublishedAt time.Time          `json:"published_at"`
}

// WorkspaceManager materializes ContextFS objects for Agents.
type WorkspaceManager struct {
	store *Store
	root  string

	mu sync.Mutex
}

// NewWorkspaceManager creates an Agent workspace manager.
func NewWorkspaceManager(
	store *Store,
	root string,
) (*WorkspaceManager, error) {
	if store == nil {
		return nil, fmt.Errorf("ContextFS store is required")
	}

	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("workspace root is required")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve workspace root: %w",
			err,
		)
	}

	if err := os.MkdirAll(absoluteRoot, 0o755); err != nil {
		return nil, fmt.Errorf(
			"create workspace root: %w",
			err,
		)
	}

	return &WorkspaceManager{
		store: store,
		root:  absoluteRoot,
	}, nil
}

// Root returns the absolute workspace root.
func (m *WorkspaceManager) Root() string {
	return m.root
}

// Prepare atomically creates one Agent workspace.
//
// The final workspace becomes visible only after every requested
// context has been materialized and the manifest has been persisted.
func (m *WorkspaceManager) Prepare(
	ctx context.Context,
	agentID string,
	requests []MaterializeRequest,
) (Workspace, error) {
	if err := validateAgentID(agentID); err != nil {
		return Workspace{}, err
	}

	normalized, err := normalizeRequests(requests)
	if err != nil {
		return Workspace{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	finalRoot := filepath.Join(m.root, agentID)

	if _, err := os.Stat(finalRoot); err == nil {
		return Workspace{}, fmt.Errorf(
			"workspace for Agent %q already exists",
			agentID,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Workspace{}, fmt.Errorf(
			"inspect Agent workspace: %w",
			err,
		)
	}

	stagingRoot, err := os.MkdirTemp(
		m.root,
		".prepare-"+agentID+"-",
	)
	if err != nil {
		return Workspace{}, fmt.Errorf(
			"create staging workspace: %w",
			err,
		)
	}

	defer os.RemoveAll(stagingRoot)

	inputsDir := filepath.Join(stagingRoot, "inputs")
	privateDir := filepath.Join(stagingRoot, "private")

	for _, directory := range []string{
		inputsDir,
		privateDir,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return Workspace{}, fmt.Errorf(
				"create workspace directory: %w",
				err,
			)
		}
	}

	workspace := Workspace{
		AgentID:    agentID,
		Root:       stagingRoot,
		InputsDir:  inputsDir,
		PrivateDir: privateDir,
		Manifest:   filepath.Join(stagingRoot, "manifest.json"),
		CreatedAt:  time.Now().UTC(),
		Files:      make([]MaterializedFile, 0, len(normalized)),
	}

	for _, request := range normalized {
		select {
		case <-ctx.Done():
			return Workspace{}, ctx.Err()
		default:
		}

		object, err := m.store.Resolve(request.Digest)
		if err != nil {
			return Workspace{}, fmt.Errorf(
				"resolve context %s: %w",
				request.Digest,
				err,
			)
		}

		baseDirectory := inputsDir
		mode := fs.FileMode(0o444)

		if request.Access == AccessPrivate {
			baseDirectory = privateDir
			mode = 0o644
		}

		targetPath := filepath.Join(
			baseDirectory,
			filepath.FromSlash(request.Name),
		)

		if err := os.MkdirAll(
			filepath.Dir(targetPath),
			0o755,
		); err != nil {
			return Workspace{}, fmt.Errorf(
				"create materialization directory: %w",
				err,
			)
		}

		method, err := cloneOrCopy(
			ctx,
			object.Path,
			targetPath,
			mode,
		)
		if err != nil {
			return Workspace{}, fmt.Errorf(
				"materialize context %q: %w",
				request.Name,
				err,
			)
		}

		workspace.Files = append(
			workspace.Files,
			MaterializedFile{
				Name:       request.Name,
				Digest:     object.Digest,
				SizeBytes:  object.SizeBytes,
				Access:     request.Access,
				Path:       targetPath,
				Method:     method,
				SourcePath: object.Path,
			},
		)
	}

	if err := persistWorkspaceManifest(workspace); err != nil {
		return Workspace{}, err
	}

	if err := syncDir(stagingRoot); err != nil {
		return Workspace{}, fmt.Errorf(
			"sync staging workspace: %w",
			err,
		)
	}

	if err := os.Rename(stagingRoot, finalRoot); err != nil {
		return Workspace{}, fmt.Errorf(
			"publish Agent workspace: %w",
			err,
		)
	}

	if err := syncDir(m.root); err != nil {
		return Workspace{}, fmt.Errorf(
			"sync workspace root: %w",
			err,
		)
	}

	workspace.PublishedAt = time.Now().UTC()
	workspace.Root = finalRoot
	workspace.InputsDir = filepath.Join(finalRoot, "inputs")
	workspace.PrivateDir = filepath.Join(finalRoot, "private")
	workspace.Manifest = filepath.Join(finalRoot, "manifest.json")

	for index := range workspace.Files {
		relativePath, err := filepath.Rel(
			stagingRoot,
			workspace.Files[index].Path,
		)
		if err != nil {
			return Workspace{}, fmt.Errorf(
				"resolve materialized path: %w",
				err,
			)
		}

		workspace.Files[index].Path = filepath.Join(
			finalRoot,
			relativePath,
		)
	}

	// Update the published manifest with final paths.
	if err := persistWorkspaceManifest(workspace); err != nil {
		_ = os.RemoveAll(finalRoot)

		return Workspace{}, fmt.Errorf(
			"publish final workspace manifest: %w",
			err,
		)
	}

	return workspace, nil
}

// OpenWorkspace reads one published workspace manifest.
func (m *WorkspaceManager) OpenWorkspace(
	agentID string,
) (Workspace, error) {
	if err := validateAgentID(agentID); err != nil {
		return Workspace{}, err
	}

	data, err := os.ReadFile(
		filepath.Join(m.root, agentID, "manifest.json"),
	)
	if err != nil {
		return Workspace{}, fmt.Errorf(
			"read workspace manifest: %w",
			err,
		)
	}

	var workspace Workspace

	if err := json.Unmarshal(data, &workspace); err != nil {
		return Workspace{}, fmt.Errorf(
			"decode workspace manifest: %w",
			err,
		)
	}

	if workspace.AgentID != agentID {
		return Workspace{}, fmt.Errorf(
			"workspace manifest Agent ID mismatch",
		)
	}

	return workspace, nil
}

// Cleanup removes one complete Agent workspace.
//
// ContextFS blobs are not affected.
func (m *WorkspaceManager) Cleanup(agentID string) error {
	if err := validateAgentID(agentID); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	path := filepath.Join(m.root, agentID)

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf(
			"remove Agent workspace: %w",
			err,
		)
	}

	return syncDir(m.root)
}

func normalizeRequests(
	requests []MaterializeRequest,
) ([]MaterializeRequest, error) {
	byPath := make(map[string]MaterializeRequest)

	for _, request := range requests {
		request.Name = filepath.ToSlash(
			strings.TrimSpace(request.Name),
		)
		request.Digest = strings.ToLower(
			strings.TrimSpace(request.Digest),
		)

		if err := validateRelativeName(request.Name); err != nil {
			return nil, err
		}

		if err := validateDigest(request.Digest); err != nil {
			return nil, fmt.Errorf(
				"context %q: %w",
				request.Name,
				err,
			)
		}

		switch request.Access {
		case "":
			request.Access = AccessReadOnly

		case AccessReadOnly, AccessPrivate:
		default:
			return nil, fmt.Errorf(
				"context %q has unsupported access mode %q",
				request.Name,
				request.Access,
			)
		}

		key := string(request.Access) + ":" + request.Name

		if existing, exists := byPath[key]; exists {
			if existing.Digest != request.Digest {
				return nil, fmt.Errorf(
					"context path %q maps to multiple digests",
					request.Name,
				)
			}

			continue
		}

		byPath[key] = request
	}

	keys := make([]string, 0, len(byPath))

	for key := range byPath {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	result := make([]MaterializeRequest, 0, len(keys))

	for _, key := range keys {
		result = append(result, byPath[key])
	}

	return result, nil
}

func validateAgentID(agentID string) error {
	if !safeAgentID.MatchString(agentID) {
		return fmt.Errorf(
			"invalid Agent ID %q",
			agentID,
		)
	}

	return nil
}

func validateRelativeName(name string) error {
	if name == "" {
		return fmt.Errorf("materialized context name is required")
	}

	if filepath.IsAbs(name) {
		return fmt.Errorf(
			"materialized context name must be relative",
		)
	}

	cleaned := filepath.ToSlash(filepath.Clean(name))

	if cleaned == "." ||
		cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf(
			"materialized context name escapes the workspace",
		)
	}

	return nil
}

func cloneOrCopy(
	ctx context.Context,
	sourcePath string,
	targetPath string,
	mode fs.FileMode,
) (string, error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open source object: %w", err)
	}
	defer source.Close()

	target, err := os.OpenFile(
		targetPath,
		os.O_CREATE|os.O_EXCL|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return "", fmt.Errorf("create target file: %w", err)
	}

	success := false

	defer func() {
		_ = target.Close()

		if !success {
			_ = os.Remove(targetPath)
		}
	}()

	method := "reflink"

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		target.Fd(),
		uintptr(ficlone),
		source.Fd(),
	)

	if errno != 0 {
		method = "copy"

		if err := target.Truncate(0); err != nil {
			return "", fmt.Errorf(
				"reset target after reflink failure: %w",
				err,
			)
		}

		if _, err := target.Seek(0, io.SeekStart); err != nil {
			return "", fmt.Errorf(
				"seek target: %w",
				err,
			)
		}

		if _, err := source.Seek(0, io.SeekStart); err != nil {
			return "", fmt.Errorf(
				"seek source: %w",
				err,
			)
		}

		if err := copyFileWithContext(
			ctx,
			target,
			source,
		); err != nil {
			return "", err
		}
	}

	if err := target.Chmod(mode); err != nil {
		return "", fmt.Errorf(
			"set materialized permissions: %w",
			err,
		)
	}

	if err := target.Sync(); err != nil {
		return "", fmt.Errorf(
			"sync materialized context: %w",
			err,
		)
	}

	if err := target.Close(); err != nil {
		return "", fmt.Errorf(
			"close materialized context: %w",
			err,
		)
	}

	success = true

	return method, nil
}

func copyFileWithContext(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
) error {
	buffer := make([]byte, 1024*1024)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		count, readErr := source.Read(buffer)

		if count > 0 {
			written, writeErr := destination.Write(
				buffer[:count],
			)

			if writeErr != nil {
				return writeErr
			}

			if written != count {
				return io.ErrShortWrite
			}
		}

		if errors.Is(readErr, io.EOF) {
			return nil
		}

		if readErr != nil {
			return readErr
		}
	}
}

func persistWorkspaceManifest(
	workspace Workspace,
) error {
	data, err := json.MarshalIndent(workspace, "", "  ")
	if err != nil {
		return fmt.Errorf(
			"encode workspace manifest: %w",
			err,
		)
	}

	if err := writeFileAtomic(
		workspace.Manifest,
		data,
		0o644,
	); err != nil {
		return fmt.Errorf(
			"persist workspace manifest: %w",
			err,
		)
	}

	return nil
}

// CleanupStaging removes abandoned temporary workspaces.
//
// This may be called during Runtime startup after an unclean shutdown.
func (m *WorkspaceManager) CleanupStaging() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.root)
	if err != nil {
		return fmt.Errorf(
			"read workspace root: %w",
			err,
		)
	}

	for _, entry := range entries {
		if !entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), ".prepare-") {
			continue
		}

		if err := os.RemoveAll(
			filepath.Join(m.root, entry.Name()),
		); err != nil {
			return fmt.Errorf(
				"remove abandoned workspace %s: %w",
				entry.Name(),
				err,
			)
		}
	}

	return syncDir(m.root)
}
