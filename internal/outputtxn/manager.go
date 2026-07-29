package outputtxn

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	"time"
)

const (
	manifestFileName = ".aegis-commit.json"
	manifestVersion  = 1
)

var safeAgentID = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`,
)

// Limits controls output validation.
type Limits struct {
	MaxFiles   int    `json:"max_files"`
	MaxBytes   uint64 `json:"max_bytes"`
	AllowEmpty bool   `json:"allow_empty"`
}

// DefaultLimits returns conservative output limits.
func DefaultLimits() Limits {
	return Limits{
		MaxFiles:   1024,
		MaxBytes:   1024 * 1024 * 1024,
		AllowEmpty: false,
	}
}

// Manager owns output staging and committed directories.
type Manager struct {
	root         string
	stagingDir   string
	committedDir string
	limits       Limits
}

// Transaction is one isolated Agent output transaction.
type Transaction struct {
	ID         string    `json:"id"`
	AgentID    string    `json:"agent_id"`
	StagingDir string    `json:"staging_dir"`
	FinalDir   string    `json:"final_dir"`
	CreatedAt  time.Time `json:"created_at"`
}

// FileRecord describes one validated committed output.
type FileRecord struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes uint64 `json:"size_bytes"`
	Mode      string `json:"mode"`
}

// Manifest is persisted inside every committed transaction.
type Manifest struct {
	Version       int          `json:"version"`
	TransactionID string       `json:"transaction_id"`
	AgentID       string       `json:"agent_id"`
	CreatedAt     time.Time    `json:"created_at"`
	CommittedAt   time.Time    `json:"committed_at"`
	FileCount     int          `json:"file_count"`
	TotalBytes    uint64       `json:"total_bytes"`
	Files         []FileRecord `json:"files"`
}

// CommitResult describes a successful atomic commit.
type CommitResult struct {
	TransactionID string `json:"transaction_id"`
	FinalDir      string `json:"final_dir"`
	ManifestPath  string `json:"manifest_path"`
	FileCount     int    `json:"file_count"`
	TotalBytes    uint64 `json:"total_bytes"`
}

// Open initializes a transactional output store.
func Open(root string, limits Limits) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("output transaction root is required")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf(
			"resolve output transaction root: %w",
			err,
		)
	}

	defaults := DefaultLimits()

	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaults.MaxFiles
	}

	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}

	manager := &Manager{
		root:         absoluteRoot,
		stagingDir:   filepath.Join(absoluteRoot, "staging"),
		committedDir: filepath.Join(absoluteRoot, "committed"),
		limits:       limits,
	}

	for _, directory := range []string{
		manager.stagingDir,
		manager.committedDir,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, fmt.Errorf(
				"create output directory %s: %w",
				directory,
				err,
			)
		}
	}

	return manager, nil
}

// Root returns the transaction store root.
func (m *Manager) Root() string {
	return m.root
}

// StagingRoot returns the private transaction staging root.
func (m *Manager) StagingRoot() string {
	return m.stagingDir
}

// CommittedRoot returns the immutable committed output root.
func (m *Manager) CommittedRoot() string {
	return m.committedDir
}

// Begin creates one private output staging directory.
func (m *Manager) Begin(agentID string) (Transaction, error) {
	if !safeAgentID.MatchString(agentID) {
		return Transaction{}, fmt.Errorf(
			"invalid Agent ID %q",
			agentID,
		)
	}

	for attempt := 0; attempt < 16; attempt++ {
		randomSuffix, err := randomHex(8)
		if err != nil {
			return Transaction{}, err
		}

		transactionID := fmt.Sprintf(
			"%s-%d-%s",
			agentID,
			time.Now().UTC().UnixNano(),
			randomSuffix,
		)

		stagingDir := filepath.Join(
			m.stagingDir,
			transactionID,
		)

		err = os.Mkdir(stagingDir, 0o700)
		if errors.Is(err, os.ErrExist) {
			continue
		}

		if err != nil {
			return Transaction{}, fmt.Errorf(
				"create output staging directory: %w",
				err,
			)
		}

		return Transaction{
			ID:         transactionID,
			AgentID:    agentID,
			StagingDir: stagingDir,
			FinalDir: filepath.Join(
				m.committedDir,
				agentID,
				transactionID,
			),
			CreatedAt: time.Now().UTC(),
		}, nil
	}

	return Transaction{}, fmt.Errorf(
		"could not allocate a unique output transaction",
	)
}

// Commit validates and atomically publishes one transaction.
func (m *Manager) Commit(
	ctx context.Context,
	transaction Transaction,
) (CommitResult, error) {
	if err := m.validateTransaction(transaction); err != nil {
		return CommitResult{}, err
	}

	files, totalBytes, err := m.scanArtifacts(
		ctx,
		transaction.StagingDir,
	)
	if err != nil {
		return CommitResult{}, err
	}

	committedAt := time.Now().UTC()

	manifest := Manifest{
		Version:       manifestVersion,
		TransactionID: transaction.ID,
		AgentID:       transaction.AgentID,
		CreatedAt:     transaction.CreatedAt,
		CommittedAt:   committedAt,
		FileCount:     len(files),
		TotalBytes:    totalBytes,
		Files:         files,
	}

	manifestData, err := json.MarshalIndent(
		manifest,
		"",
		"  ",
	)
	if err != nil {
		return CommitResult{}, fmt.Errorf(
			"encode output manifest: %w",
			err,
		)
	}

	stagingManifest := filepath.Join(
		transaction.StagingDir,
		manifestFileName,
	)

	if err := writeFileAtomic(
		stagingManifest,
		manifestData,
		0o444,
	); err != nil {
		return CommitResult{}, fmt.Errorf(
			"persist output manifest: %w",
			err,
		)
	}

	if err := syncDirectoryTree(
		transaction.StagingDir,
	); err != nil {
		return CommitResult{}, fmt.Errorf(
			"sync output transaction: %w",
			err,
		)
	}

	finalParent := filepath.Dir(transaction.FinalDir)

	if err := os.MkdirAll(finalParent, 0o755); err != nil {
		return CommitResult{}, fmt.Errorf(
			"create committed output directory: %w",
			err,
		)
	}

	if _, err := os.Stat(transaction.FinalDir); err == nil {
		return CommitResult{}, fmt.Errorf(
			"committed transaction already exists: %s",
			transaction.FinalDir,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CommitResult{}, fmt.Errorf(
			"inspect committed transaction: %w",
			err,
		)
	}

	// Staging and committed directories live under the same root,
	// so rename publishes the complete result atomically.
	if err := os.Rename(
		transaction.StagingDir,
		transaction.FinalDir,
	); err != nil {
		return CommitResult{}, fmt.Errorf(
			"atomically publish Agent output: %w",
			err,
		)
	}

	if err := syncDir(finalParent); err != nil {
		return CommitResult{}, fmt.Errorf(
			"sync committed output parent: %w",
			err,
		)
	}

	return CommitResult{
		TransactionID: transaction.ID,
		FinalDir:      transaction.FinalDir,
		ManifestPath: filepath.Join(
			transaction.FinalDir,
			manifestFileName,
		),
		FileCount:  len(files),
		TotalBytes: totalBytes,
	}, nil
}

// Abort discards an uncommitted transaction.
func (m *Manager) Abort(transaction Transaction) error {
	if err := m.validateTransaction(transaction); err != nil {
		return err
	}

	if err := os.RemoveAll(transaction.StagingDir); err != nil {
		return fmt.Errorf(
			"remove output staging directory: %w",
			err,
		)
	}

	return syncDir(m.stagingDir)
}

func (m *Manager) validateTransaction(
	transaction Transaction,
) error {
	if transaction.ID == "" ||
		transaction.AgentID == "" ||
		transaction.StagingDir == "" ||
		transaction.FinalDir == "" {
		return fmt.Errorf("incomplete output transaction")
	}

	absoluteStaging, err := filepath.Abs(
		transaction.StagingDir,
	)
	if err != nil {
		return fmt.Errorf(
			"resolve transaction staging path: %w",
			err,
		)
	}

	if filepath.Dir(absoluteStaging) != m.stagingDir {
		return fmt.Errorf(
			"transaction staging directory is outside the store",
		)
	}

	absoluteFinal, err := filepath.Abs(
		transaction.FinalDir,
	)
	if err != nil {
		return fmt.Errorf(
			"resolve committed transaction path: %w",
			err,
		)
	}

	expectedParent := filepath.Join(
		m.committedDir,
		transaction.AgentID,
	)

	if filepath.Dir(absoluteFinal) != expectedParent {
		return fmt.Errorf(
			"transaction commit directory is outside the Agent namespace",
		)
	}

	info, err := os.Stat(absoluteStaging)
	if err != nil {
		return fmt.Errorf(
			"inspect output staging directory: %w",
			err,
		)
	}

	if !info.IsDir() {
		return fmt.Errorf(
			"output staging path is not a directory",
		)
	}

	return nil
}

func (m *Manager) scanArtifacts(
	ctx context.Context,
	stagingDir string,
) ([]FileRecord, uint64, error) {
	files := make([]FileRecord, 0)
	var totalBytes uint64

	err := filepath.WalkDir(
		stagingDir,
		func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if path == stagingDir {
				return nil
			}

			relativePath, err := filepath.Rel(
				stagingDir,
				path,
			)
			if err != nil {
				return err
			}

			relativePath = filepath.ToSlash(relativePath)

			if relativePath == manifestFileName {
				return fmt.Errorf(
					"output path %q is reserved",
					relativePath,
				)
			}

			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf(
					"symbolic links are not allowed in Agent output: %s",
					relativePath,
				)
			}

			if entry.IsDir() {
				return nil
			}

			info, err := entry.Info()
			if err != nil {
				return err
			}

			if !info.Mode().IsRegular() {
				return fmt.Errorf(
					"non-regular output is not allowed: %s",
					relativePath,
				)
			}

			if len(files)+1 > m.limits.MaxFiles {
				return fmt.Errorf(
					"Agent output exceeds the file limit of %d",
					m.limits.MaxFiles,
				)
			}

			size := uint64(info.Size())

			if size > m.limits.MaxBytes-totalBytes {
				return fmt.Errorf(
					"Agent output exceeds the byte limit of %d",
					m.limits.MaxBytes,
				)
			}

			digest, err := hashFile(ctx, path, info.Size())
			if err != nil {
				return fmt.Errorf(
					"hash output %s: %w",
					relativePath,
					err,
				)
			}

			publishedMode := fs.FileMode(0o444)

			if info.Mode().Perm()&0o111 != 0 {
				publishedMode = 0o555
			}

			if err := os.Chmod(path, publishedMode); err != nil {
				return fmt.Errorf(
					"protect committed output %s: %w",
					relativePath,
					err,
				)
			}

			files = append(files, FileRecord{
				Path:      relativePath,
				SHA256:    digest,
				SizeBytes: size,
				Mode: fmt.Sprintf(
					"%04o",
					publishedMode.Perm(),
				),
			})

			totalBytes += size

			return nil
		},
	)
	if err != nil {
		return nil, 0, fmt.Errorf(
			"validate Agent output: %w",
			err,
		)
	}

	if len(files) == 0 && !m.limits.AllowEmpty {
		return nil, 0, fmt.Errorf(
			"Agent produced no output files",
		)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	return files, totalBytes, nil
}

func hashFile(
	ctx context.Context,
	path string,
	expectedSize int64,
) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	buffer := make([]byte, 1024*1024)
	var total int64

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		count, readErr := file.Read(buffer)

		if count > 0 {
			written, writeErr := hasher.Write(buffer[:count])
			if writeErr != nil {
				return "", writeErr
			}

			if written != count {
				return "", io.ErrShortWrite
			}

			total += int64(count)
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return "", readErr
		}
	}

	if total != expectedSize {
		return "", fmt.Errorf(
			"output changed while being validated",
		)
	}

	info, err := file.Stat()
	if err != nil {
		return "", err
	}

	if info.Size() != expectedSize {
		return "", fmt.Errorf(
			"output changed while being validated",
		)
	}

	if err := file.Sync(); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func randomHex(byteCount int) (string, error) {
	data := make([]byte, byteCount)

	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf(
			"generate transaction ID: %w",
			err,
		)
	}

	return hex.EncodeToString(data), nil
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

	temporary, err := os.CreateTemp(
		directory,
		".output-manifest-*",
	)
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

func syncDirectoryTree(root string) error {
	directories := make([]string, 0)

	err := filepath.WalkDir(
		root,
		func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}

			if entry.IsDir() {
				directories = append(directories, path)
			}

			return nil
		},
	)
	if err != nil {
		return err
	}

	sort.Slice(directories, func(i, j int) bool {
		return len(directories[i]) > len(directories[j])
	})

	for _, directory := range directories {
		if err := syncDir(directory); err != nil {
			return err
		}
	}

	return nil
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()

	return directory.Sync()
}
