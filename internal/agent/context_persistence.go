package agent

import (
	"context"

	"deepzora/internal/provider"
)

// SessionContextMaintenance records bookkeeping beside the exact current model
// context. The model messages themselves are committed once by the session.
type SessionContextMaintenance struct {
	Generation        uint64                     `json:"generation"`
	ProjectionVersion uint64                     `json:"projectionVersion"`
	LineageKey        string                     `json:"lineageKey"`
	Receipt           *ContextMaintenanceReceipt `json:"receipt"`
}

type SessionContextMaintenanceReader interface {
	SessionContextMaintenance() *SessionContextMaintenance
}

type SessionContextMaintenanceRecorder interface {
	RecordSessionContextMaintenance(context.Context, SessionContextMaintenance) (SessionModelContextCommitResult, error)
}

func CloneSessionContextMaintenance(state *SessionContextMaintenance) *SessionContextMaintenance {
	if state == nil {
		return nil
	}
	copy := *state
	if state.Receipt != nil {
		receipt := *state.Receipt
		copy.Receipt = &receipt
	}
	return &copy
}

func maintenanceState(state CompactionState) *SessionContextMaintenance {
	return CloneSessionContextMaintenance(&SessionContextMaintenance{
		Generation: state.Generation, ProjectionVersion: state.Projection.ProjectionVersion,
		LineageKey: state.PromptCacheKey, Receipt: state.LastReceipt,
	})
}

// RestoreSessionContextMaintenance runs after the host installs the current
// execution workset. Reopen needs no sidecar or reconstruction from UI history.
func (a *Agent) RestoreSessionContextMaintenance() {
	if a == nil || a.Session() == nil {
		return
	}
	reader, ok := a.svc.sessionCheckpointer.(SessionContextMaintenanceReader)
	if !ok {
		return
	}
	saved := reader.SessionContextMaintenance()
	if saved == nil || saved.Receipt == nil || saved.LineageKey != a.currentPromptCacheKey() {
		return
	}
	messages, version := a.Session().snapshotMessagesVersion()
	state := CompactionState{Generation: saved.Generation, PromptCacheKey: saved.LineageKey,
		TranscriptVersion: version, LastReceipt: saved.Receipt, UpdatedAt: saved.Receipt.CreatedAt}
	state.Projection.ProjectionVersion = saved.ProjectionVersion
	if saved.ProjectionVersion > 0 {
		state.Projection.Messages = provider.ModelMessages(messages)
		state.Projection.CoveredCount = len(messages)
		state.Projection.CoveredPrefixHash = coveredPrefixHash(messages, len(messages))
		state.Projection.PinnedContextHash = pinnedContextCoverageHash(messages, len(messages))
		state.Projection.SummaryHash = saved.Receipt.SummaryHash
		state.Projection.ProjectionTokens = saved.Receipt.ResultTokens
	}
	a.sess.compactionMu.Lock()
	a.sess.compactionState = state
	a.sess.checkpointState = "restored"
	a.sess.compactionMu.Unlock()
}
