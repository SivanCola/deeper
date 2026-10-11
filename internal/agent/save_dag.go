package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/store"
)

// probeNativeLogForSave permits only current logs or an empty new path.
func (s *Session) probeNativeLogForSave(path string) (sessionEventLogProbe, error) {
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return probe, err
	}
	if probe.size > 0 && !probe.dag {
		return probe, fmt.Errorf("unsupported session log schema %d", probe.schemaVersion)
	}
	if probe.size == 0 {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return probe, fmt.Errorf("unsupported checkpoint-only session")
		} else if err != nil && !os.IsNotExist(err) {
			return probe, err
		}
	}
	return probe, nil
}

// saveDAGLocked persists msgs to a schema-2 log: it replays (or extends) the
// cached graph, diffs the in-memory transcript against this session's head,
// appends the resulting entries in one batch, and refreshes the derived
// files. Concurrent writers never conflict; a writer that only fell behind
// its own head reports a stale-prefix conflict so the caller adopts disk.
func (s *Session) saveDAGLocked(path string, mode sessionSaveMode, msgs []provider.Message, version uint64, rewriteVersion int, digest [sha256.Size]byte) error {
	ctx := context.Background()
	now := time.Now().UTC()
	baseRevision, ledgerDigest, err := sessionContentRevision(path)
	ledgerUnreadable := err != nil
	if ledgerUnreadable {
		// A persisted session with an unreadable ledger fails closed, as in
		// schema 1; a brand-new transcript still lands first and the record
		// step below reports the sidecar.
		if sessionArtifactsHaveContent(path) {
			return err
		}
		baseRevision = 0
	}
	if err := s.initializeSessionLog(path, now); err != nil {
		return err
	}
	st, err := s.dagStateForSave(ctx, path, now)
	if err != nil {
		return err
	}
	s.ensureMessageIDsForSave(msgs)
	plan, err := s.planDAGWrite(path, st, msgs, mode, now)
	if err != nil {
		return err
	}
	deferProjection := mode.defersProjection()
	pending := s.takePendingMarkers()
	for i := range pending {
		pending[i].Head = plan.head
	}
	plan.entries = append(plan.entries, pending...)
	if len(plan.entries) == 0 {
		s.adoptDAGPosition(st, plan)
		if ledgerDigest != digestString(digest) {
			baseRevision, err = recordSessionContentRevision(path, digest, baseRevision, 0)
			if err != nil {
				return err
			}
		}
		state := s.persistState(path)
		if !deferProjection && (state.projectionPending || !state.saveVerified || ledgerDigest != digestString(digest)) {
			s.republishDAGDerived(ctx, path, st, plan, msgs, digest, baseRevision)
		}
		s.maintainDAGLog(ctx, path, st, plan, mode, now)
		s.markCheckpointPersisted(path, digest, version, baseRevision, rewriteVersion, msgs, deferProjection)
		return nil
	}
	reserved, err := invalidateSessionListingProjection(path)
	if err != nil {
		if !ledgerUnreadable {
			return fmt.Errorf("invalidate session listing projection: %w", err)
		}
		reserved = 0
	}
	tail := st.lastGoodEnd
	if _, err := appendSessionDAGEntries(path, plan.entries, true); err != nil {
		s.requeuePendingMarkers(pending)
		return err
	}
	if err := st.replayFrom(ctx, tail, defaultSessionReplayLimits); err != nil {
		return err
	}
	if st.damaged {
		return fmt.Errorf("session log %s: appended entries did not replay", path)
	}
	plan.applyIDRenames(s)
	s.adoptDAGPosition(st, plan)
	// Publish the selected head's read model before its revision ledger.
	selected := st.selectedHead()
	displayCurrent := selected == plan.head && writeDAGCheckpointCache(path, plan, msgs, baseRevision)
	revision, err := recordSessionContentRevision(path, digest, baseRevision, reserved)
	if err != nil {
		return err
	}
	s.publishDAGDerived(ctx, path, st, plan, msgs, digest, revision, selected, displayCurrent, deferProjection)
	s.maintainDAGLog(ctx, path, st, plan, mode, now)
	s.markCheckpointPersisted(path, digest, version, revision, rewriteVersion, msgs, deferProjection)
	return nil
}

// dagStateForSave returns the replayed graph, extending the cached state by
// the bytes appended since its last observed tail when the generation and
// size still allow it, and settles a torn tail before any append.
func (s *Session) dagStateForSave(ctx context.Context, path string, now time.Time) (*sessionDAGState, error) {
	logPath := store.SessionEventLog(path)
	s.mu.RLock()
	cached := s.head.state
	s.mu.RUnlock()
	header, ok, err := readSessionDAGHeader(path)
	if err != nil {
		return nil, err
	}
	var st *sessionDAGState
	if cached != nil && ok && cached.path == logPath && header.generation == cached.generation && !cached.damaged {
		if info, err := os.Stat(logPath); err == nil && info.Size() >= cached.lastGoodEnd {
			st = cached
			if err := st.replayFrom(ctx, st.lastGoodEnd, defaultSessionReplayLimits); err != nil {
				return nil, err
			}
		}
	}
	if st == nil {
		st, err = replaySessionDAG(ctx, logPath, defaultSessionReplayLimits)
		if err != nil {
			return nil, err
		}
	}
	if st.damaged {
		if err := settleDAGTail(ctx, path, st, now); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// settleDAGTail waits out the quiet window a torn tail might still be
// finishing, then repairs it; appending after an unrepaired partial line
// would bury this writer's own entry inside it.
func settleDAGTail(ctx context.Context, path string, st *sessionDAGState, now time.Time) error {
	logPath := store.SessionEventLog(path)
	info, err := os.Stat(logPath)
	if err != nil {
		return err
	}
	if age := now.Sub(info.ModTime()); age < sessionDAGTailRepairMinAge {
		time.Sleep(sessionDAGTailRepairMinAge - age)
		st.damaged = false
		if err := st.replayFrom(ctx, st.lastGoodEnd, defaultSessionReplayLimits); err != nil {
			return err
		}
		if !st.damaged {
			return nil
		}
	}
	repaired, err := repairSessionDAGTail(path, st, time.Now().UTC())
	if err != nil {
		return err
	}
	if !repaired {
		return fmt.Errorf("session log %s has a torn tail that is still being written", path)
	}
	return nil
}

// ensureMessageIDsForSave mints ids for messages that reached the session
// without one and writes them back by position so the next save sees the
// same ids the log now holds.
func (s *Session) ensureMessageIDsForSave(msgs []provider.Message) {
	minted := false
	for i := range msgs {
		if msgs[i].ID == "" {
			msgs[i].ID = NewMessageID()
			minted = true
		}
	}
	if !minted {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range msgs {
		if i < len(s.Messages) && s.Messages[i].ID == "" && messagesEqualForStorage(s.Messages[i], msgs[i]) {
			s.Messages[i].ID = msgs[i].ID
		}
	}
}

func (s *Session) adoptDAGPosition(st *sessionDAGState, plan *dagWritePlan) {
	h := st.heads[plan.head]
	leaf := ""
	if h != nil {
		leaf = h.leaf
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.head.ref = HeadRef{HeadID: plan.head, LeafID: leaf, LogGeneration: st.generation, LogOffset: st.lastGoodEnd}
	s.head.dag = true
	s.head.state = st
	s.head.headCount = len(st.heads)
	if plan.forked {
		s.head.events = append(s.head.events, HeadEvent{Kind: HeadEventForkedConcurrent, HeadID: plan.head, OtherWriter: plan.otherWriter})
	}
}

// writeDAGCheckpointCache refreshes the .jsonl random-read model for the
// selected head, extending it in place for a pure append. It reports whether
// the cache now matches msgs; failures are logged, never fatal.
func writeDAGCheckpointCache(path string, plan *dagWritePlan, msgs []provider.Message, baseRevision int64) bool {
	if plan.pureAppend {
		current, err := appendSessionDisplayReadModel(path, msgs, plan.appendFrom, baseRevision)
		if err != nil {
			slog.Warn("session: keeping save after display read-model append failure", "path", path, "err", err)
		}
		if current {
			return true
		}
	}
	if err := writeSessionMessages(path, msgs); err != nil {
		slog.Warn("session: keeping save after display read-model write failure", "path", path, "err", err)
		return false
	}
	return true
}

// publishDAGDerived refreshes the display index when the .jsonl cache is
// current for this head, and the head index plus meta mirror on every save.
func (s *Session) publishDAGDerived(ctx context.Context, path string, st *sessionDAGState, plan *dagWritePlan, msgs []provider.Message, digest [sha256.Size]byte, revision int64, selected string, displayCurrent, deferProjection bool) {
	if displayCurrent {
		appendFrom := -1
		if plan.pureAppend {
			appendFrom = plan.appendFrom
		}
		if err := refreshCheckpointDisplayIndex(path, msgs, digest, revision, appendFrom, deferProjection); err != nil {
			slog.Warn("session: keeping save after display index write failure", "path", path, "err", err)
		}
	}
	if err := writeSessionDAGIndex(ctx, path, st); err != nil {
		slog.Warn("session: keeping save after head index write failure", "path", path, "err", err)
	}
	if err := UpdateBranchMeta(path, false, func(meta *BranchMeta) error {
		meta.HeadID = selected
		meta.HeadCount = len(st.heads)
		meta.LogSchema = sessionDAGSchemaVersion
		meta.LogGeneration = st.generation
		return nil
	}); err != nil {
		slog.Warn("session: head metadata update deferred", "path", path, "err", err)
	}
}

// maintainDAGLog rotates the log when it has outgrown its live chains or a
// redaction needs its bytes physically erased, but only under the
// single-writer proof; otherwise the log simply keeps growing for now.
func (s *Session) maintainDAGLog(ctx context.Context, path string, st *sessionDAGState, plan *dagWritePlan, mode sessionSaveMode, now time.Time) {
	if mode != sessionSaveRewriteCompact && st.holes == 0 && !sessionDAGLogOversized(st) {
		return
	}
	if err := sessionDAGSingleWriterProof(path, st, now); err != nil {
		slog.Info("session: log rotation deferred", "path", path, "reason", err)
		return
	}
	if err := rotateSessionDAG(path, st, now); err != nil {
		slog.Warn("session: log rotation failed", "path", path, "err", err)
		return
	}
	fresh, err := replaySessionDAG(ctx, store.SessionEventLog(path), defaultSessionReplayLimits)
	if err != nil {
		slog.Warn("session: replay after rotation failed", "path", path, "err", err)
		return
	}
	s.adoptDAGPosition(fresh, &dagWritePlan{head: plan.head})
	if err := writeSessionDAGIndex(ctx, path, fresh); err != nil {
		slog.Warn("session: head index write after rotation failed", "path", path, "err", err)
	}
}

// initializeSessionLog creates a new current DAG without importing other formats.
func (s *Session) initializeSessionLog(path string, now time.Time) error {
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return err
	}
	if probe.dag {
		return nil
	}
	if probe.size > 0 {
		return fmt.Errorf("unsupported session log schema %d", probe.schemaVersion)
	}
	if !probe.creatable {
		return fmt.Errorf("unsupported session event path")
	}
	return initializeSessionDAG(path, now)
}
