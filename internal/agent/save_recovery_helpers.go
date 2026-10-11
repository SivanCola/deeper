package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"deepzora/internal/provider"
)

// writeRecoveryBranchAtPath persists one bounded recovery lane. collision is
// true only when path already contains content this Session did not persist;
// callers rotate lanes instead of overwriting that independent transcript.
func (s *Session) writeRecoveryBranchAtPath(
	path string,
	opts RecoveryBranchOptions,
	msgs []provider.Message,
	digest [sha256.Size]byte,
	version uint64,
	rewriteVersion int,
	preview string,
	turns int,
	digestText string,
	recoveryDepth int,
	shutdown bool,
) (RecoveryBranchInfo, bool, error) {
	unlockPath := lockSessionSavePath(path)
	defer unlockPath()
	unlockFile, err := lockSessionFile(path)
	if err != nil {
		return RecoveryBranchInfo{}, false, fmt.Errorf("lock recovery session file: %w", err)
	}
	defer unlockFile()
	if loaded, loadErr := loadSessionUnlocked(path); loadErr == nil && loaded != nil {
		existingDigest, digestErr := digestSessionMessages(loaded.Snapshot())
		if digestErr != nil {
			return RecoveryBranchInfo{}, false, digestErr
		}
		if bytes.Equal(existingDigest[:], digest[:]) {
			unlockMeta, lockErr := LockSessionMetaPath(path)
			if lockErr != nil {
				return RecoveryBranchInfo{}, false, lockErr
			}
			defer unlockMeta()
			if _, err := copyValidContextProjection(opts.OriginalPath, path, msgs); err != nil {
				slog.Warn("session: recovery branch did not inherit context projection", "path", path, "err", err)
			}
			meta, err := s.saveRecoveryBranchMetaLocked(path, opts, preview, turns, digestText, recoveryDepth, true)
			if err != nil {
				return RecoveryBranchInfo{}, false, err
			}
			if err := writeSessionEventIndex(path, msgs, digest, meta.Revision); err != nil {
				slog.Warn("session: keeping recovery branch after event index write failure", "path", path, "err", err)
			}
			if err := refreshSessionDisplayIndex(path, msgs, digest, meta.Revision, -1); err != nil {
				slog.Warn("session: keeping recovery branch after display index write failure", "path", path, "err", err)
			}
			if err := s.adoptRecoveryDAGPosition(path); err != nil {
				return RecoveryBranchInfo{}, false, err
			}
			s.setPersistedBaseline(path, digest, version, meta.Revision, true, true, rewriteVersion, msgs)
			return RecoveryBranchInfo{Path: path, Digest: digestText, Existing: true, Meta: meta, Preview: preview, Turns: turns}, false, nil
		}
		state := s.persistState(path)
		if !state.ok || !bytes.Equal(state.digest[:], existingDigest[:]) {
			return RecoveryBranchInfo{}, true, nil
		}
	} else if loadErr != nil && !os.IsNotExist(loadErr) {
		return RecoveryBranchInfo{}, false, loadErr
	} else if sessionArtifactExists(path) {
		// Unsupported or incomplete content still occupies this identity.
		// Rotate the recovery lane without modifying its files.
		return RecoveryBranchInfo{}, true, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return RecoveryBranchInfo{}, false, fmt.Errorf("create recovery session dir: %w", err)
	}
	unlockMeta, err := LockSessionMetaPath(path)
	if err != nil {
		return RecoveryBranchInfo{}, false, err
	}
	defer unlockMeta()
	meta, err := s.prepareRecoveryBranchMetaLocked(path, opts, preview, turns, digestText, recoveryDepth, false)
	if err != nil {
		return RecoveryBranchInfo{}, false, err
	}
	if err := writeRecoveryEventLog(path, msgs, shutdown); err != nil {
		return RecoveryBranchInfo{}, false, err
	}
	if err := writeSessionMessages(path, msgs); err != nil {
		return RecoveryBranchInfo{}, false, err
	}
	if _, err := copyValidContextProjection(opts.OriginalPath, path, msgs); err != nil {
		slog.Warn("session: recovery branch did not inherit context projection", "path", path, "err", err)
	}
	if err := saveBranchMeta(path, meta, true); err != nil {
		return RecoveryBranchInfo{}, false, err
	}
	if err := writeSessionEventIndex(path, msgs, digest, meta.Revision); err != nil {
		slog.Warn("session: keeping recovery branch after event index write failure", "path", path, "err", err)
	}
	if err := refreshSessionDisplayIndex(path, msgs, digest, meta.Revision, -1); err != nil {
		slog.Warn("session: keeping recovery branch after display index write failure", "path", path, "err", err)
	}
	if err := s.adoptRecoveryDAGPosition(path); err != nil {
		return RecoveryBranchInfo{}, false, err
	}
	s.setPersistedBaseline(path, digest, version, meta.Revision, true, true, rewriteVersion, msgs)
	return RecoveryBranchInfo{Path: path, Digest: digestText, Meta: meta, Preview: preview, Turns: turns}, false, nil
}

// CopyValidContextProjection copies a parent's projection to this session's
// path only when the covered canonical prefix still matches. Forks use it to
// preserve compacted context without borrowing stale parent history.
func (s *Session) CopyValidContextProjection(originalPath, targetPath string) (bool, error) {
	if s == nil {
		return false, nil
	}
	return copyValidContextProjection(originalPath, targetPath, s.Snapshot())
}

func copyValidContextProjection(originalPath, targetPath string, msgs []provider.Message) (bool, error) {
	if _, ok, err := LoadCompactionState(targetPath); err != nil {
		return false, err
	} else if ok {
		return false, nil
	}
	st, ok, err := LoadCompactionState(originalPath)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	n := st.Projection.CoveredCount
	if len(st.Projection.Messages) == 0 || n <= 0 || n > len(msgs) ||
		st.Projection.CoveredPrefixHash == "" ||
		coveredPrefixHash(msgs, n) != st.Projection.CoveredPrefixHash {
		return false, nil
	}
	if err := SaveCompactionState(targetPath, st); err != nil {
		return false, err
	}
	return true, nil
}

// healEmptyCheckpointFromWAL rebuilds a missing or 0-byte checkpoint from its
// own valid event log. Healthy checkpoints return before probing the WAL.
func healEmptyCheckpointFromWAL(path string) error {
	info, statErr := os.Stat(path)
	needHeal := statErr != nil && os.IsNotExist(statErr)
	if statErr == nil {
		needHeal = info.Size() == 0
	}
	if !needHeal {
		return nil
	}
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return err
	}
	if !probe.dag || probe.size <= 0 || probe.futureSchema {
		return nil
	}
	msgs, fromEvents, damaged, err := loadSessionMessages(path)
	if err != nil || !fromEvents || damaged || len(msgs) == 0 {
		return nil
	}
	if err := writeSessionMessages(path, msgs); err != nil {
		return fmt.Errorf("rebuild checkpoint from WAL: %w", err)
	}
	return nil
}

// Pair the recovered transcript with its own head before a later cancellation
// or rewrite. The original file's head is never a baseline for this lane.
func (s *Session) adoptRecoveryDAGPosition(path string) error {
	result, err := loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.head.ref = result.head
	s.head.dag = result.dag
	s.head.state = result.state
	s.head.headCount = result.headCount
	s.mu.Unlock()
	return nil
}
