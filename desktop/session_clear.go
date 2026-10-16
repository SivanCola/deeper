package main

import (
	"deepzora/internal/session"
)

// SessionClearResult is the post-clear session identity the frontend must apply
// atomically so hydrate/mode-switch cannot re-bind to the destroyed transcript.
type SessionClearResult struct {
	SessionPath       string              `json:"sessionPath"`
	SessionID         string              `json:"sessionId,omitempty"`
	Session           *session.SessionRef `json:"session,omitempty"`
	SessionRevision   int64               `json:"sessionRevision,omitempty"`
	SessionDigest     string              `json:"sessionDigest,omitempty"`
	SessionGeneration uint64              `json:"sessionGeneration"`
}

// ClearSession discards the current conversation and rotates to a fresh unsaved one.
func (a *App) ClearSession() (SessionClearResult, error) {
	return a.ClearSessionForTab("")
}

// ClearSessionForTab clears the requested tab regardless of later focus changes.
// On success it returns the replacement session identity (path/revision/digest
// and a tab-local generation) so the frontend can retire the old transcript
// without waiting for a later MetaForTab round trip.
func (a *App) ClearSessionForTab(tabID string) (SessionClearResult, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return SessionClearResult{}, readOnlyChannelErr()
	}
	if ctrl == nil {
		return SessionClearResult{}, a.workspaceNotReadyErr(tab)
	}
	if err := a.validateTabControllerWorkspace(tab); err != nil {
		return SessionClearResult{}, err
	}
	ctrl = a.controllerForTab(tab)
	if ctrl == nil {
		return SessionClearResult{}, a.workspaceNotReadyErr(tab)
	}
	if controllerHasActiveRuntimeWork(ctrl) {
		return a.clearActiveSessionRuntime(tab, ctrl)
	}
	unlockRuntime := a.lockRuntimeMutation("clear session")
	defer unlockRuntime()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	ctrl = a.controllerForTab(tab)
	if ctrl == nil {
		return SessionClearResult{}, a.workspaceNotReadyErr(tab)
	}
	if controllerHasActiveRuntimeWork(ctrl) {
		return SessionClearResult{}, errTopicHasActiveWork
	}
	if err := ctrl.ClearSession(); err != nil {
		a.syncTabSessionIdentity(tab, ctrl)
		return SessionClearResult{}, err
	}
	a.syncTabSessionIdentity(tab, ctrl)
	tab.setPinnedFiles(nil)
	tab.resetTelemetry(tab.currentSessionIdentity())
	// Mirror the controller: ClearSession cleared the active goal.
	a.clearTabGoal(tab)
	a.rememberTabSessionPath(tab, ctrl.SessionRoute())
	a.invalidatePromptHistoryCache()
	return a.bumpAndSnapshotSessionClear(tab), nil
}

func (a *App) bumpAndSnapshotSessionClear(tab *WorkspaceTab) SessionClearResult {
	if tab == nil {
		return SessionClearResult{}
	}
	a.mu.Lock()
	tab.SessionGeneration++
	gen := tab.SessionGeneration
	if tab.sink != nil {
		tab.sink.setSessionGeneration(gen)
	}
	sessionID := tab.SessionID
	revision, digest, _ := a.canonicalTabHistoryFingerprint(tab)
	a.mu.Unlock()
	var sessionRef *session.SessionRef
	if sessionID != "" {
		ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID}
		sessionRef = &ref
	}
	return SessionClearResult{
		SessionID: sessionID, Session: sessionRef,
		SessionRevision: revision, SessionDigest: digest, SessionGeneration: gen,
	}
}

// clearTabGoal drops the tab's persisted goal copy so rebuilds and restarts
// cannot re-seed a goal the controller has already cleared on session rotation.
func (a *App) clearTabGoal(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	a.mu.Lock()
	tab.goal = ""
	if current := a.tabs[tab.ID]; current == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
}
