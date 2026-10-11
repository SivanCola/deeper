package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/store"
)

// fixedWriterRecoverySessionPath is the process-wide stable recovery path
// for originalPath. Nested -recovery- names peel back to the root branch.
func fixedWriterRecoverySessionPath(originalPath string) string {
	return stableRecoverySessionPath(originalPath, SessionWriterID())
}

func recoveryRootID(path string) string {
	if root, ok := RecoveryFilenameRootID(path); ok {
		return root
	}
	id := strings.TrimSpace(BranchID(path))
	if id == "" {
		return "session"
	}
	return id
}

// stableRecoverySessionPath is one recovery file per (root branch, generation).
// A bounded digest suffix keeps recovery identities stable across retries.
func stableRecoverySessionPath(originalPath, generation string) string {
	root := recoveryRootID(originalPath)
	if strings.TrimSpace(generation) == "" {
		generation = SessionWriterID()
	}
	sum := sha256.Sum256([]byte(root + "\x00" + generation))
	suffix := fmt.Sprintf("-recovery-%x", sum[:8])
	stem := recoveryParentStem(root)
	if stem+suffix == BranchID(originalPath) {
		return originalPath
	}
	return filepath.Join(filepath.Dir(originalPath), stem+suffix+".jsonl")
}

func (s *Session) recoveryGenerationKey() string {
	if s == nil {
		return SessionWriterID()
	}
	s.mu.Lock()
	if s.recoveryLane != "" {
		lane := s.recoveryLane
		s.mu.Unlock()
		return lane
	}
	s.mu.Unlock()
	candidate := ""
	if auth := s.WriteAuthority(); auth != nil && auth.Generation() != 0 {
		writerID := SessionWriterID()
		if writer := auth.Writer(); writer != nil && strings.TrimSpace(writer.WriterID()) != "" {
			writerID = writer.WriterID()
		}
		candidate = fmt.Sprintf("%s\x00gen-%d", writerID, auth.Generation())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recoveryLane == "" {
		if candidate == "" {
			candidate = newSessionWriterID()
		}
		s.recoveryLane = candidate
	}
	return s.recoveryLane
}

func (s *Session) isolatedRecoverySessionPath(originalPath string) (string, string) {
	gen := s.recoveryGenerationKey()
	return stableRecoverySessionPath(originalPath, gen), gen
}

func (s *Session) rotateRecoveryLane(current string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recoveryLane == "" || s.recoveryLane == current {
		s.recoveryLane = newSessionWriterID()
	}
}

// writeRecoveryEventLog writes a recovery event log. Isolated writer lanes
// compact so repeated in-place rewrites stay bounded.

// writeRecoveryEventLog persists recovery lanes using the current DAG writer.
func writeRecoveryEventLog(path string, msgs []provider.Message, isolated bool) error {
	branch, err := loadSessionUnlocked(path)
	if os.IsNotExist(err) {
		branch = NewSession("")
	} else if err != nil {
		return err
	}
	branch.Rewrite(msgs, "recovery")
	lease, err := TryAcquireSessionLease(path)
	if err != nil {
		return err
	}
	defer lease.Release()
	mode := sessionSaveSnapshot
	if isolated {
		mode = sessionSaveRewriteCompact
	}
	now := time.Now().UTC()
	if err := branch.initializeSessionLog(path, now); err != nil {
		return err
	}
	state, err := replaySessionDAG(context.Background(), store.SessionEventLog(path), defaultSessionReplayLimits)
	if err != nil {
		return err
	}
	branch.ensureMessageIDsForSave(msgs)
	plan, err := branch.planDAGWrite(path, state, msgs, mode, now)
	if err != nil {
		return err
	}
	if _, err := appendSessionDAGEntries(path, plan.entries, true); err != nil {
		return err
	}
	if err := state.replayFrom(context.Background(), state.lastGoodEnd, defaultSessionReplayLimits); err != nil {
		return err
	}
	if state.damaged {
		return fmt.Errorf("recovery DAG contains an incomplete batch")
	}
	if isolated {
		if err := sessionDAGSingleWriterProof(path, state, now); err != nil {
			return err
		}
		if err := rotateSessionDAG(path, state, now); err != nil {
			return err
		}
	}
	return nil
}
