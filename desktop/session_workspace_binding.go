package main

import (
	"context"
	"errors"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

var errSessionWorkspaceConflict = errors.New("session workspace identity is inconsistent; the session files were left unchanged")

// canonicalTabBinding is what a controller build publishes to its tab once
// the durable session is bound: identity, workspace, and the projected name.
type canonicalTabBinding struct {
	ref         session.SessionRef
	workspaceID string
	title       string
	titleSource string
}

// canonicalSeedTitle records an explicit name, including a name equal to the
// localized default. Automatic UI labels are never persisted as manual names.
func canonicalSeedTitle(title, source string) string {
	if source != topicTitleSourceManual {
		return ""
	}
	return strings.TrimSpace(title)
}

// bindTabCanonicalSessionTopic binds the session, publishes its topic
// identity, and resolves the tab name from the session log and current
// presentation. Restored and reopened tabs use the same name projection.
func (a *App) bindTabCanonicalSessionTopic(
	ctx context.Context,
	identity control.IdentityLifecycle,
	cfg *config.Config,
	scope, workspaceRoot, sessionID, model string,
	modelFallback bool,
	topicID, seedTitle string,
) (canonicalTabBinding, error) {
	ref, workspaceID, err := a.bindTabCanonicalSession(ctx, identity, cfg, scope, workspaceRoot, sessionID, model, modelFallback)
	if err != nil {
		return canonicalTabBinding{}, err
	}
	if err := a.workspaceRegistry().EnsureSessionTopic(ctx, ref.SessionID, topicID, seedTitle); err != nil {
		return canonicalTabBinding{}, err
	}
	bound := canonicalTabBinding{ref: ref, workspaceID: workspaceID}
	if snapshot, loadErr := a.workspaceRegistry().VerifySnapshot(ctx); loadErr == nil {
		bound.title, bound.titleSource = a.canonicalTabTitleWithPresentation(ctx, snapshot.Session(ref.SessionID).Presentation, ref)
	}
	return bound, nil
}

// applyLocked publishes the binding to the tab. The caller holds App.mu.
func (b canonicalTabBinding) applyLocked(tab *WorkspaceTab) {
	tab.SessionID, tab.SessionWorkspace.ID = b.ref.SessionID, b.workspaceID
	if b.title != "" {
		tab.TopicTitle, tab.topicTitleSource = b.title, b.titleSource
	}
}

// Resolve navigation from durable membership and the immutable header, never
// from the current surface. Both authorities must agree before execution.
func (a *App) canonicalSessionWorkspace(ctx context.Context, ref session.SessionRef) (workspacestate.Workspace, error) {
	if err := validateLocalSessionRef(ref); err != nil {
		return workspacestate.Workspace{}, err
	}
	info, err := a.desktopSessionService("").Query().Stat(ctx, ref)
	if err != nil {
		return workspacestate.Workspace{}, err
	}
	snapshot, err := a.workspaceRegistry().VerifySnapshot(ctx)
	if err != nil {
		return workspacestate.Workspace{}, err
	}
	metadata := snapshot.Session(ref.SessionID)
	if metadata.State.Lifecycle == workspacestate.Deleted {
		return workspacestate.Workspace{}, session.ErrSessionNotFound
	}
	owner := metadata.Workspace
	if metadata.OwnershipConflict || owner.ID == "" || info.Origin == "" || strings.TrimSpace(info.CWD) == "" {
		return owner, errSessionWorkspaceConflict
	}
	same, identityErr := sameDesktopPathStrict(info.CWD, owner.Root)
	if identityErr != nil {
		return owner, identityErr
	}
	same = same || (owner.ID == workspacestate.GlobalWorkspaceID && isGlobalWorkspacePath(owner, info.CWD))
	if !same {
		return owner, errSessionWorkspaceConflict
	}
	return owner, nil
}

func canonicalWorkspaceScope(workspace workspacestate.Workspace) string {
	if workspace.ID == workspacestate.GlobalWorkspaceID {
		return "global"
	}
	return "project"
}

func canonicalWorkspaceChanged(snap tabRuntimeSnapshot, workspace workspacestate.Workspace) bool {
	return snap.scope != canonicalWorkspaceScope(workspace) || !sameDesktopPath(desktopWorkspaceRoot(snap.scope, snap.workspaceRoot), workspace.Root)
}

// The caller resolves workspaceChanged before taking App.mu, then publishes
// the session/controller in the same critical section. Path identity resolution
// may touch the filesystem and must never run while App.mu is held.
func applyCanonicalWorkspaceLocked(tab *WorkspaceTab, workspace workspacestate.Workspace, workspaceChanged bool) {
	if workspaceChanged {
		tab.TopicID, tab.TopicTitle, tab.topicTitleSource = "", "", ""
		tab.setPinnedFiles(nil)
	}
	tab.Scope, tab.WorkspaceRoot = canonicalWorkspaceScope(workspace), workspace.Root
	tab.SessionWorkspace.ID = workspace.ID
}

func canonicalSessionTopicIdentity(state workspacestate.State, sessionID string) (string, string) {
	presentation := state.Presentation[sessionID]
	topicID := strings.TrimSpace(presentation.TopicID)
	if topicID == "" {
		topicID = "canonical-" + sessionID
	}
	return topicID, presentation.Title
}

func (a *App) commitCanonicalSessionBinding(tab *WorkspaceTab, ctrl control.SessionAPI, ref session.SessionRef, workspace workspacestate.Workspace, navigation uint64) error {
	snapshot, err := a.workspaceRegistry().VerifySnapshot(a.bootContext())
	if err != nil {
		return err
	}
	presentation := snapshot.Session(ref.SessionID).Presentation
	topicID := strings.TrimSpace(presentation.TopicID)
	if topicID == "" {
		topicID = "canonical-" + ref.SessionID
	}
	// The tab name is re-derived from the session log on every bind so the
	// topicbar can never trail a rename committed while the tab was away.
	topicTitle, topicSource := a.canonicalTabTitleWithPresentation(a.bootContext(), presentation, ref)
	workspaceChanged := canonicalWorkspaceChanged(a.tabRuntimeSnapshot(tab), workspace)
	a.mu.Lock()
	defer a.mu.Unlock()
	if tab.removed || a.tabs[tab.ID] != tab || tab.Ctrl != ctrl || (navigation != 0 && a.desktopSessions.navigationSeq.Load() != navigation) {
		return errSessionNavigationSuperseded
	}
	applyCanonicalWorkspaceLocked(tab, workspace, workspaceChanged)
	setTabSessionIdentity(tab, sessionRoute(ref.SessionID))
	tab.TopicID, tab.TopicTitle, tab.topicTitleSource = topicID, topicTitle, topicSource
	a.bindSessionRuntimeKeyLocked(tab, tab.currentSessionIdentity())
	a.saveTabsLocked()
	return nil
}

// Cold surfaces must already agree with the current durable owner. Reject an
// inconsistent saved binding instead of rewriting historical layout data.
func (a *App) validateCanonicalTabWorkspace(ctx context.Context, tab *WorkspaceTab, generation uint64) error {
	a.mu.RLock()
	id, ctrl := tab.SessionID, tab.Ctrl
	a.mu.RUnlock()
	if id == "" || ctrl != nil {
		return nil
	}
	workspace, err := a.canonicalSessionWorkspace(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: id})
	if errors.Is(err, session.ErrSessionNotFound) {
		// Leave missing-session handling to controller startup.
		return nil
	}
	if err != nil {
		return err
	}
	snap := a.tabRuntimeSnapshot(tab)
	workspaceChanged := canonicalWorkspaceChanged(snap, workspace)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tabBuildSupersededLocked(tab, generation) || tab.SessionID != id || tab.Ctrl != nil {
		return errSessionNavigationSuperseded
	}
	if workspaceChanged || tab.Scope != snap.scope || tab.WorkspaceRoot != snap.workspaceRoot || tab.SessionWorkspace.ID != workspace.ID || validateTabSessionIdentity(id) != nil {
		return errSessionWorkspaceConflict
	}
	return nil
}

func (a *App) prepareTabControllerWorkspace(tab *WorkspaceTab, ctx context.Context, generation uint64, appCtx context.Context) bool {
	a.mu.Lock()
	// Keep a lease-blocked banner steady across background retries. Ordinary
	// builds reset readiness before resolving their persisted workspace.
	if !tab.removed && tab.Ctrl == nil && !tab.StartupErrLeaseHeld {
		tab.Ready = false
		clearTabStartupError(tab)
		a.setSessionRuntimePhaseLocked(tab, sessionRuntimeStarting, nil)
	}
	a.mu.Unlock()
	if err := a.validateCanonicalTabWorkspace(ctx, tab, generation); err != nil {
		a.recordTabStartupFailure(tab, generation, appCtx, friendlySessionLoadError(err))
		return false
	}

	return true
}
