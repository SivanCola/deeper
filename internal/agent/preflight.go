package agent

import (
	"errors"
	"strings"

	"deepzora/internal/provider"
)

// ErrCompactionRequired is returned when the prompt exceeds the provider limit
// and compaction could not produce a usable projection. Callers may retry.
var ErrCompactionRequired = errors.New("context exceeds provider limit and compaction failed")

// modelVisibleMessages returns the provider-bound message list: a valid
// projection plus any post-projection appends, otherwise the full canonical
// transcript. LocalOnly stripping still happens in prepareSamplingRequest.
func (a *Agent) modelVisibleMessages() []provider.Message {
	if a == nil || a.sess.conversation == nil {
		return nil
	}
	msgs, _ := a.sess.conversation.snapshotMessagesVersion()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	if projectionValid(st, msgs, a.currentPromptCacheKey()) {
		if visible := modelVisibleFromProjection(st.Projection, msgs); len(visible) > 0 {
			return visible
		}
	}
	return msgs
}

func (a *Agent) currentProjectionVersion() uint64 {
	if a == nil {
		return 0
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	return a.sess.compactionState.Projection.ProjectionVersion
}

// currentPromptCacheKey is the lineage key for the bound session + model.
func (a *Agent) currentPromptCacheKey() string {
	if a == nil {
		return ""
	}
	a.sess.compactionMu.Lock()
	defer a.sess.compactionMu.Unlock()
	return a.currentPromptCacheKeyLocked()
}

func (a *Agent) currentPromptCacheKeyLocked() string {
	return promptCacheKey(a.workspaceID, a.cacheSessionIdentity(), a.modelRef)
}

// InvalidateProjection drops the in-memory and on-disk projection after
// lineage-changing operations (rewind, branch, fork, system/model change).
func (a *Agent) InvalidateProjection() {
	if a == nil {
		return
	}
	// A strong reasoning-replay overlay is indexed against the old canonical
	// history. Clear it together with the compaction projection so rewind,
	// branch, and model/system lineage changes cannot reuse a stale anchor.
	a.sess.clearReasoningReplayStrongProjection()
	a.sess.compactionMu.Lock()
	a.sess.compactionState = CompactionState{}
	a.sess.compactionMu.Unlock()
	a.sess.compaction.stuck = false
	a.sess.compaction.stuckInputHash = ""
	a.sess.compaction.consecutive = 0
	a.sess.compaction.failedTurn.Store(0)
	a.sess.compaction.lastTurn.Store(0)
}

// InvalidateProjectionIfStale keeps the projection when it still matches the
// current transcript and performs the full invalidation otherwise. History
// rewrites that only touch messages past CoveredCount keep their fold.
func (a *Agent) InvalidateProjectionIfStale() {
	if a == nil {
		return
	}
	// This helper is called after a history rewrite even when the existing
	// compaction fold remains valid (for example a tail-only rewind). The
	// reasoning-replay overlay still belongs to the pre-rewrite shape.
	a.sess.clearReasoningReplayStrongProjection()
	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	if len(st.Projection.Messages) > 0 && a.sess.conversation != nil {
		msgs, _ := a.sess.conversation.snapshotMessagesVersion()
		if projectionValid(st, msgs, a.currentPromptCacheKeyLocked()) {
			a.sess.compactionMu.Unlock()
			return
		}
	}
	a.sess.compactionMu.Unlock()
	a.InvalidateProjection()
}

// promptCacheKey builds a stable lineage key for session + model identity.
// It deliberately excludes message counts, timestamps, and projection hashes.
func promptCacheKey(workspaceID, sessionLineage, modelRef string) string {
	parts := []string{
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(sessionLineage),
		strings.TrimSpace(modelRef),
	}
	return strings.Join(parts, "|")
}
