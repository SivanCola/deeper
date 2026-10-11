package agent

import "deepzora/internal/provider"

// ContextReport is a point-in-time view of context pressure: the declared
// window, the thresholds derived from it, what the model currently sees, and how
// the last maintenance pass ended. A misconfigured window and a genuinely full
// one produce the same notices, so the numbers behind the decision have to be
// inspectable rather than inferred.
type ContextReport struct {
	Window       int
	HardCeiling  int
	OutputBudget int

	LatestPrompt     int
	CanonicalTokens  int
	ProjectionTokens int
	Projected        bool

	SoftThreshold  int
	SnipThreshold  int
	FoldThreshold  int
	ForceThreshold int

	LastTrigger   string
	LastMode      string
	LastSource    int
	LastResult    int
	CacheState    string
	BlockedReason string
}

// ContextReport samples the current context state. Compaction is disabled when
// Window is zero, in which case the thresholds carry no meaning.
func (a *Agent) ContextReport() ContextReport {
	if a == nil {
		return ContextReport{}
	}
	rep := ContextReport{
		Window:       a.contextWindow,
		HardCeiling:  a.hardInputCeiling(),
		OutputBudget: a.maxOutputTokens,
		CacheState:   a.CacheState(),
	}
	if u := a.sess.output.lastUsage.Load(); u != nil {
		rep.LatestPrompt = u.LatestPromptTokens()
	}
	if a.sess.conversation != nil {
		canonical, _ := a.sess.conversation.snapshotMessagesVersion()
		rep.CanonicalTokens = a.estimatedPromptTokens(provider.ModelMessages(canonical))
	}
	visible := a.modelVisibleMessages()
	rep.ProjectionTokens = a.estimatedPromptTokens(provider.ModelMessages(visible))
	rep.Projected = rep.ProjectionTokens != rep.CanonicalTokens

	if a.contextWindow > 0 {
		rep.FoldThreshold = a.compactTrigger()
		if _, reason := a.contextMaintenanceBlocked(a.contextMaintenanceInputHash(visible), 0); reason != "" {
			rep.BlockedReason = reason
		}
	}

	a.sess.compactionMu.Lock()
	st := a.sess.compactionState
	a.sess.compactionMu.Unlock()
	// The current receipt is authoritative for compaction reporting.
	if r := st.LastReceipt; r != nil && r.Status == "applied" {
		rep.LastTrigger = r.Trigger
		if r.Action == "summary" {
			rep.LastMode = CompactionModeSummarized
		} else if r.Action != "" {
			rep.LastMode = r.Action
		}
		rep.LastSource, rep.LastResult = r.InputTokens, r.ResultTokens
	}
	if rep.BlockedReason == "" {
		if r := st.LastReceipt; r != nil && (r.Status == "blocked" || r.Status == "failed") {
			rep.BlockedReason = r.Reason
		}
	}
	return rep
}
