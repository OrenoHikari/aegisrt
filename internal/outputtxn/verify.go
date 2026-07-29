package outputtxn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aegisrt/internal/agent"
)

const maximumManifestBytes = 16 * 1024 * 1024

// Verify validates one committed Agent output against its persisted
// manifest and recomputes every artifact SHA-256 digest.
func (m *Manager) Verify(
	ctx context.Context,
	output agent.DependencyOutput,
) (agent.OutputVerification, error) {
	if !safeAgentID.MatchString(output.AgentID) {
		return agent.OutputVerification{}, fmt.Errorf(
			"invalid committed-output Agent ID %q",
			output.AgentID,
		)
	}

	if strings.TrimSpace(output.TransactionID) == "" {
		return agent.OutputVerification{}, fmt.Errorf(
			"output transaction ID is required",
		)
	}

	expectedFinal := filepath.Join(
		m.committedDir,
		output.AgentID,
		output.TransactionID,
	)

	commitPath, err := filepath.Abs(output.CommitPath)
	if err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"resolve committed output path: %w",
			err,
		)
	}

	if filepath.Clean(commitPath) !=
		filepath.Clean(expectedFinal) {
		return agent.OutputVerification{}, fmt.Errorf(
			"committed output path is outside its Agent namespace",
		)
	}

	expectedManifest := filepath.Join(
		expectedFinal,
		manifestFileName,
	)

	manifestPath, err := filepath.Abs(output.ManifestPath)
	if err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"resolve output manifest path: %w",
			err,
		)
	}

	if filepath.Clean(manifestPath) !=
		filepath.Clean(expectedManifest) {
		return agent.OutputVerification{}, fmt.Errorf(
			"output manifest path is invalid",
		)
	}

	if err := ensureNoSymlinkPath(
		m.committedDir,
		expectedFinal,
	); err != nil {
		return agent.OutputVerification{}, err
	}

	finalInfo, err := os.Lstat(expectedFinal)
	if err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"inspect committed output directory: %w",
			err,
		)
	}

	if finalInfo.Mode()&os.ModeSymlink != 0 ||
		!finalInfo.IsDir() {
		return agent.OutputVerification{}, fmt.Errorf(
			"committed output path is not a regular directory",
		)
	}

	manifestInfo, err := os.Lstat(expectedManifest)
	if err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"inspect output manifest: %w",
			err,
		)
	}

	if manifestInfo.Mode()&os.ModeSymlink != 0 ||
		!manifestInfo.Mode().IsRegular() {
		return agent.OutputVerification{}, fmt.Errorf(
			"output manifest is not a regular file",
		)
	}

	if manifestInfo.Size() > maximumManifestBytes {
		return agent.OutputVerification{}, fmt.Errorf(
			"output manifest exceeds %d bytes",
			maximumManifestBytes,
		)
	}

	manifestData, err := os.ReadFile(expectedManifest)
	if err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"read output manifest: %w",
			err,
		)
	}

	manifestDigest := sha256.Sum256(manifestData)
	manifestSHA256 := hex.EncodeToString(
		manifestDigest[:],
	)

	var manifest Manifest

	if err := json.Unmarshal(
		manifestData,
		&manifest,
	); err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"decode output manifest: %w",
			err,
		)
	}

	if manifest.Version != manifestVersion {
		return agent.OutputVerification{}, fmt.Errorf(
			"unsupported output manifest version %d",
			manifest.Version,
		)
	}

	if manifest.AgentID != output.AgentID {
		return agent.OutputVerification{}, fmt.Errorf(
			"output manifest Agent ID mismatch",
		)
	}

	if manifest.TransactionID != output.TransactionID {
		return agent.OutputVerification{}, fmt.Errorf(
			"output manifest transaction ID mismatch",
		)
	}

	if manifest.CreatedAt.IsZero() ||
		manifest.CommittedAt.IsZero() {
		return agent.OutputVerification{}, fmt.Errorf(
			"output manifest has invalid timestamps",
		)
	}

	if manifest.FileCount != len(manifest.Files) {
		return agent.OutputVerification{}, fmt.Errorf(
			"manifest file count mismatch: declared %d, listed %d",
			manifest.FileCount,
			len(manifest.Files),
		)
	}

	if output.FileCount != manifest.FileCount {
		return agent.OutputVerification{}, fmt.Errorf(
			"recorded file count mismatch: expected %d, got %d",
			output.FileCount,
			manifest.FileCount,
		)
	}

	if output.TotalBytes != manifest.TotalBytes {
		return agent.OutputVerification{}, fmt.Errorf(
			"recorded output byte count mismatch: expected %d, got %d",
			output.TotalBytes,
			manifest.TotalBytes,
		)
	}

	listedFiles := make(
		map[string]FileRecord,
		len(manifest.Files),
	)

	var verifiedBytes uint64

	for _, record := range manifest.Files {
		select {
		case <-ctx.Done():
			return agent.OutputVerification{}, ctx.Err()
		default:
		}

		relativePath, err :=
			validateArtifactPath(record.Path)
		if err != nil {
			return agent.OutputVerification{}, err
		}

		if relativePath == manifestFileName {
			return agent.OutputVerification{}, fmt.Errorf(
				"manifest cannot list itself as an artifact",
			)
		}

		if _, exists := listedFiles[relativePath]; exists {
			return agent.OutputVerification{}, fmt.Errorf(
				"duplicate manifest artifact %q",
				relativePath,
			)
		}

		if err := validateSHA256(record.SHA256); err != nil {
			return agent.OutputVerification{}, fmt.Errorf(
				"artifact %q: %w",
				relativePath,
				err,
			)
		}

		targetPath := filepath.Join(
			expectedFinal,
			filepath.FromSlash(relativePath),
		)

		targetInfo, err := os.Lstat(targetPath)
		if err != nil {
			return agent.OutputVerification{}, fmt.Errorf(
				"inspect committed artifact %q: %w",
				relativePath,
				err,
			)
		}

		if targetInfo.Mode()&os.ModeSymlink != 0 {
			return agent.OutputVerification{}, fmt.Errorf(
				"committed artifact %q is a symbolic link",
				relativePath,
			)
		}

		if !targetInfo.Mode().IsRegular() {
			return agent.OutputVerification{}, fmt.Errorf(
				"committed artifact %q is not a regular file",
				relativePath,
			)
		}

		if uint64(targetInfo.Size()) != record.SizeBytes {
			return agent.OutputVerification{}, fmt.Errorf(
				"artifact %q size mismatch: expected %d, got %d",
				relativePath,
				record.SizeBytes,
				targetInfo.Size(),
			)
		}

		recordedMode, err := strconv.ParseUint(
			record.Mode,
			8,
			32,
		)
		if err != nil {
			return agent.OutputVerification{}, fmt.Errorf(
				"artifact %q has invalid mode %q",
				relativePath,
				record.Mode,
			)
		}

		actualMode := targetInfo.Mode().Perm()

		if actualMode != fs.FileMode(recordedMode) {
			return agent.OutputVerification{}, fmt.Errorf(
				"artifact %q mode mismatch: expected %04o, got %04o",
				relativePath,
				recordedMode,
				actualMode,
			)
		}

		if actualMode&0o222 != 0 {
			return agent.OutputVerification{}, fmt.Errorf(
				"committed artifact %q remains writable",
				relativePath,
			)
		}

		actualDigest, err := hashFile(
			ctx,
			targetPath,
			targetInfo.Size(),
		)
		if err != nil {
			return agent.OutputVerification{}, fmt.Errorf(
				"hash committed artifact %q: %w",
				relativePath,
				err,
			)
		}

		if actualDigest != record.SHA256 {
			return agent.OutputVerification{}, fmt.Errorf(
				"artifact %q SHA-256 mismatch",
				relativePath,
			)
		}

		listedFiles[relativePath] = record
		verifiedBytes += record.SizeBytes
	}

	if verifiedBytes != manifest.TotalBytes {
		return agent.OutputVerification{}, fmt.Errorf(
			"manifest total byte count mismatch: declared %d, verified %d",
			manifest.TotalBytes,
			verifiedBytes,
		)
	}

	actualFiles := 0

	err = filepath.WalkDir(
		expectedFinal,
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

			if path == expectedFinal {
				return nil
			}

			relativePath, err := filepath.Rel(
				expectedFinal,
				path,
			)
			if err != nil {
				return err
			}

			relativePath = filepath.ToSlash(
				relativePath,
			)

			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf(
					"symbolic link found in committed output: %s",
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
					"non-regular committed output found: %s",
					relativePath,
				)
			}

			if relativePath == manifestFileName {
				return nil
			}

			if _, exists := listedFiles[relativePath]; !exists {
				return fmt.Errorf(
					"unmanifested committed artifact found: %s",
					relativePath,
				)
			}

			actualFiles++
			return nil
		},
	)
	if err != nil {
		return agent.OutputVerification{}, fmt.Errorf(
			"scan committed output: %w",
			err,
		)
	}

	if actualFiles != manifest.FileCount {
		return agent.OutputVerification{}, fmt.Errorf(
			"committed artifact count mismatch: expected %d, got %d",
			manifest.FileCount,
			actualFiles,
		)
	}

	return agent.OutputVerification{
		Method:         "sha256-manifest",
		ManifestSHA256: manifestSHA256,
		VerifiedAt:     time.Now().UTC(),
		FileCount:      manifest.FileCount,
		TotalBytes:     manifest.TotalBytes,
	}, nil
}

func validateArtifactPath(
	path string,
) (string, error) {
	path = filepath.ToSlash(
		strings.TrimSpace(path),
	)

	if path == "" {
		return "", fmt.Errorf(
			"manifest artifact path is required",
		)
	}

	if filepath.IsAbs(filepath.FromSlash(path)) {
		return "", fmt.Errorf(
			"manifest artifact path %q is absolute",
			path,
		)
	}

	cleaned := filepath.ToSlash(
		filepath.Clean(filepath.FromSlash(path)),
	)

	if cleaned == "." ||
		cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf(
			"manifest artifact path %q escapes the transaction",
			path,
		)
	}

	return cleaned, nil
}

func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("invalid SHA-256 digest length")
	}

	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid SHA-256 digest")
	}

	return nil
}

func ensureNoSymlinkPath(
	root string,
	target string,
) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	relativePath, err := filepath.Rel(
		absoluteRoot,
		absoluteTarget,
	)
	if err != nil {
		return err
	}

	if relativePath == ".." ||
		strings.HasPrefix(
			relativePath,
			".."+string(os.PathSeparator),
		) {
		return fmt.Errorf(
			"path escapes the committed-output root",
		)
	}

	current := absoluteRoot

	rootInfo, err := os.Lstat(current)
	if err != nil {
		return fmt.Errorf(
			"inspect committed-output root: %w",
			err,
		)
	}

	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf(
			"committed-output root is a symbolic link",
		)
	}

	for _, component := range strings.Split(
		relativePath,
		string(os.PathSeparator),
	) {
		if component == "" || component == "." {
			continue
		}

		current = filepath.Join(current, component)

		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf(
				"inspect committed-output path %s: %w",
				current,
				err,
			)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(
				"symbolic link found in committed-output path: %s",
				current,
			)
		}
	}

	return nil
}
