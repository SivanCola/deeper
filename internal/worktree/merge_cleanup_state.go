package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"deepzora/internal/fileutil"
)

const (
	cleanupStateVersion = 2
	cleanupStateName    = "cleanup-state.json"

	cleanupStagePlanned  = "planned"
	cleanupStageRetained = "retained"
)

// cleanupState v2 records a retained, registered recovery checkout. It never
// describes deletion work: old and unknown writers can only preserve it.
type cleanupState struct {
	Version        int    `json:"version"`
	OriginalRoot   string `json:"originalRoot"`
	RecoveryRoot   string `json:"recoveryRoot"`
	WorktreeBranch string `json:"worktreeBranch"`
	WorktreeHead   string `json:"worktreeHead"`
	Stage          string `json:"stage"`
}

type cleanupJournal struct {
	Current *cleanupState
}

func cleanupJournalPath(metadata mergeMetadata) string {
	return filepath.Join(filepath.Dir(metadata.WorktreeRoot), cleanupStateName)
}

func writeCleanupState(metadata mergeMetadata, state cleanupState) error {
	body, err := encodeCleanupState(state)
	if err != nil {
		return err
	}
	if err := fileutil.AtomicWriteFileStrict(cleanupJournalPath(metadata), body, 0o600); err != nil {
		return fmt.Errorf("publish cleanup state: %w", err)
	}
	return nil
}

func createCleanupState(metadata mergeMetadata, state cleanupState) error {
	body, err := encodeCleanupState(state)
	if err != nil {
		return err
	}
	if err := fileutil.AtomicCreateFile(cleanupJournalPath(metadata), body, 0o600); err != nil {
		return fmt.Errorf("publish initial cleanup state: %w", err)
	}
	return nil
}

func encodeCleanupState(state cleanupState) ([]byte, error) {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode cleanup state: %w", err)
	}
	return append(body, '\n'), nil
}

func readCleanupState(metadata mergeMetadata, expectedHead string) (cleanupJournal, bool, error) {
	path := cleanupJournalPath(metadata)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return cleanupJournal{}, false, nil
	}
	if err != nil {
		return cleanupJournal{}, false, fmt.Errorf("inspect cleanup state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return cleanupJournal{}, false, errors.New("cleanup state is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return cleanupJournal{}, false, errors.New("cleanup state permissions are too broad")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return cleanupJournal{}, false, fmt.Errorf("read cleanup state: %w", err)
	}
	var envelope struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return cleanupJournal{}, false, fmt.Errorf("decode cleanup state version: %w", err)
	}
	switch envelope.Version {
	case cleanupStateVersion:
		var state cleanupState
		if err := decodeCleanupJSON(body, &state); err != nil {
			return cleanupJournal{}, false, err
		}
		if err := validateCleanupState(metadata, expectedHead, state); err != nil {
			return cleanupJournal{}, false, err
		}
		return cleanupJournal{Current: &state}, true, nil
	default:
		return cleanupJournal{}, false, fmt.Errorf("unsupported cleanup state version %d", envelope.Version)
	}
}

func decodeCleanupJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode cleanup state: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("decode cleanup state: trailing JSON content")
	}
	return nil
}

func validateCleanupState(metadata mergeMetadata, expectedHead string, state cleanupState) error {
	if state.Version != cleanupStateVersion {
		return fmt.Errorf("unsupported cleanup state version %d", state.Version)
	}
	if !sameCleanupPath(state.OriginalRoot, metadata.WorktreeRoot) ||
		state.WorktreeBranch != metadata.WorktreeBranch || state.WorktreeHead != expectedHead {
		return errors.New("cleanup state identity does not match the merge receipt")
	}
	if state.Stage != cleanupStagePlanned && state.Stage != cleanupStageRetained {
		return fmt.Errorf("unsupported cleanup stage %q", state.Stage)
	}
	return validateCleanupRecoveryPath(metadata, state.RecoveryRoot)
}

func validateCleanupRecoveryPath(metadata mergeMetadata, path string) error {
	cleanupDir := filepath.Join(filepath.Dir(metadata.WorktreeRoot), ".deepzora-cleanup")
	cleanupInfo, err := os.Lstat(cleanupDir)
	if err != nil || !cleanupInfo.IsDir() || cleanupInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("cleanup recovery directory is not a real directory")
	}
	realCleanupDir, err := filepath.EvalSymlinks(cleanupDir)
	if err != nil {
		return errors.New("cleanup recovery directory cannot be resolved")
	}
	realPath, err := resolveMissingCleanupPath(path)
	if err != nil {
		return errors.New("cleanup recovery path cannot be resolved")
	}
	rel, err := filepath.Rel(filepath.Clean(realCleanupDir), filepath.Clean(realPath))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.Contains(rel, string(filepath.Separator)) {
		return errors.New("cleanup state path escapes the allocation recovery directory")
	}
	return nil
}
