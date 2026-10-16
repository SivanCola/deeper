package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
	"deepzora/internal/worktree"
)

const rewindForkAttachError = "conversation fork was created but could not be opened; open the recovery branch from session history"

// forkTabBeforePublishHookForTest forces the persistence-to-publish interleaving.
var forkTabBeforePublishHookForTest atomic.Pointer[func()]

type forkedSessionTabOpen struct {
	tab                 TabMeta
	workspaceReferenced bool
}

// ForkWorktreeResultView distinguishes a real isolated fork from a safe shared
// fallback and from a dirty-source refusal.
type ForkWorktreeResultView struct {
	Tab              TabMeta `json:"tab"`
	Isolated         bool    `json:"isolated"`
	FallbackToShared bool    `json:"fallbackToShared,omitempty"`
	SourceDirty      bool    `json:"sourceDirty,omitempty"`
	Branch           string  `json:"branch,omitempty"`
}

// prepareForkWorktree owns availability, dirty-source refusal and rollback of
// a worktree whose source changes during creation.
func (a *App) prepareForkWorktree(scope, root string, ctrl control.SessionAPI) (worktree.Result, ForkWorktreeResultView, error) {
	shared := ForkWorktreeResultView{FallbackToShared: true}
	if scope != "project" || strings.TrimSpace(root) == "" {
		return worktree.Result{}, shared, nil
	}
	repo, _ := workspaceRepo(root, ctrl)
	available := inspectDeliveryWorktree(a.bootContext(), repo)
	if !available.Available {
		return worktree.Result{}, shared, nil
	}
	if available.SourceDirty {
		return worktree.Result{}, ForkWorktreeResultView{SourceDirty: true}, nil
	}
	release, err := a.beginWorkspaceRuntimeAdmission(root)
	if err != nil {
		return worktree.Result{}, ForkWorktreeResultView{}, err
	}
	created, err := createDeliveryWorktree(a.bootContext(), repo, config.DeliveryWorktreeDir())
	release()
	if err != nil {
		return worktree.Result{}, ForkWorktreeResultView{}, fmt.Errorf("create isolated fork worktree: %w", err)
	}
	if created.SourceDirty {
		if err := rollbackDeliveryWorktree(a.bootContext(), created); err != nil {
			return worktree.Result{}, ForkWorktreeResultView{}, fmt.Errorf("source changed while creating isolated worktree at %s; automatic cleanup failed: %w", created.WorktreeRoot, err)
		}
		return worktree.Result{}, ForkWorktreeResultView{SourceDirty: true}, nil
	}
	return created, ForkWorktreeResultView{Isolated: true, Branch: created.Branch}, nil
}

// forkForTabWithOptions forks the requested source tab, optionally creating an
// isolated Git worktree for the new tab so changes in the fork do not mutate the
// source workspace.
func (a *App) forkForTabWithOptions(tabID string, turn int, isolateWorkspace bool) (ForkWorktreeResultView, error) {
	sourceTab, ctrl := a.tabAndCtrlByID(tabID)
	if sourceTab == nil || ctrl == nil {
		return ForkWorktreeResultView{}, nil
	}
	if a.tabIsReadOnly(sourceTab) {
		return ForkWorktreeResultView{}, readOnlyChannelErr()
	}
	if err := a.validateTabControllerWorkspace(sourceTab); err != nil {
		return ForkWorktreeResultView{}, err
	}
	a.mu.RLock()
	if a.tabs[sourceTab.ID] != sourceTab || sourceTab.Ctrl == nil {
		a.mu.RUnlock()
		return ForkWorktreeResultView{}, nil
	}
	ctrl = sourceTab.Ctrl
	scope := sourceTab.Scope
	srcRoot := sourceTab.WorkspaceRoot
	sourceID, generation := sourceTab.SessionID, sourceTab.SessionGeneration
	a.mu.RUnlock()

	result := ForkWorktreeResultView{}
	var created worktree.Result
	if isolateWorkspace {
		var err error
		created, result, err = a.prepareForkWorktree(scope, srcRoot, ctrl)
		if err != nil || result.SourceDirty {
			return result, err
		}
	}

	creator, ok := ctrl.(interface {
		CheckpointForkRequest(int) (session.ForkRequest, error)
		CreateForkSession(session.ForkRequest, string) (string, error)
	})
	if !ok {
		return ForkWorktreeResultView{}, a.rollbackUnusedForkWorktree(created, errors.New("fork is unsupported"))
	}
	request, err := creator.CheckpointForkRequest(turn)
	if err != nil {
		return ForkWorktreeResultView{}, a.rollbackUnusedForkWorktree(created, err)
	}
	a.mu.RLock()
	stale := a.tabs[sourceTab.ID] != sourceTab || sourceTab.Ctrl != ctrl ||
		sourceTab.SessionID != sourceID || sourceTab.SessionGeneration != generation || request.Source.SessionID != sourceID
	a.mu.RUnlock()
	if stale {
		return ForkWorktreeResultView{}, a.rollbackUnusedForkWorktree(created, &session.ForkUnavailableError{Reason: session.ForkStaleSource})
	}
	childID := "desktop-" + strings.TrimPrefix(newTabID(), "tab_")
	operationID := "fork-" + strings.TrimPrefix(newTabID(), "tab_")
	workspaceRoot := srcRoot
	if result.Isolated {
		workspaceRoot = created.WorkspaceRoot
	}
	workspaceID, err := a.ensureDesktopWorkspace(a.bootContext(), scope, workspaceRoot)
	if err != nil {
		return ForkWorktreeResultView{}, a.rollbackUnusedForkWorktree(created, err)
	}
	if err := a.workspaceRegistry().BeginCreate(a.bootContext(), workspacestate.PendingCreate{
		OperationID: operationID, WorkspaceID: workspaceID, SessionID: childID, ParentSessionID: request.Source.SessionID,
	}); err != nil {
		return ForkWorktreeResultView{}, a.rollbackUnusedForkWorktree(created, err)
	}
	request.ChildID, request.OperationID = childID, operationID
	request.CWD = desktopWorkspaceRoot(scope, workspaceRoot)
	_, err = creator.CreateForkSession(request, "")
	if err != nil {
		_ = a.workspaceRegistry().AbortCreate(a.bootContext(), childID)
		return ForkWorktreeResultView{}, a.rollbackUnusedForkWorktree(created, err)
	}
	// Once durable, the child owns this workspace even if publication or the
	// source tab disappears. Keep pending creation for startup recovery.
	if err := a.validateDesktopWorkspaceMembership(a.bootContext(), workspaceID, session.SessionRef{HostID: localDesktopHostID, SessionID: childID}); err != nil {
		return result, err
	}
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err != nil {
		return result, err
	}
	beforeID := ""
	for i, id := range state.Workspaces[workspaceID].SessionIDs {
		if id == sourceID && i+1 < len(state.Workspaces[workspaceID].SessionIDs) {
			beforeID = state.Workspaces[workspaceID].SessionIDs[i+1]
			break
		}
	}
	if err := a.workspaceRegistry().AttachSession(a.bootContext(), operationID, workspaceID, childID, beforeID); err != nil {
		return result, fmt.Errorf("publish fork workspace membership: %w", err)
	}
	locator := forkedSessionLocator{SessionID: childID, SourceSessionID: sourceID, SourceGeneration: generation}
	opened, err := a.openForkedSessionTabWithWorkspace(sourceTab, locator, created.WorkspaceRoot)
	result.Tab = opened.tab
	if err != nil {
		return result, err
	}
	if result.Tab.ID == "" {
		return result, errors.New(rewindForkAttachError)
	}
	return result, nil
}

func (a *App) rollbackUnusedForkWorktree(created worktree.Result, cause error) error {
	if strings.TrimSpace(created.WorktreeRoot) == "" {
		return cause
	}
	if err := rollbackDeliveryWorktree(a.bootContext(), created); err != nil {
		return errors.Join(cause, fmt.Errorf("preserve unused isolated worktree at %s after cleanup failed: %w", created.WorktreeRoot, err))
	}
	return cause
}

// openForkedSessionTab attaches an already-written fork session to a new tab.
// The source tab keeps its controller and transcript. The fork becomes active
// only while the source tab still owns focus.
func (a *App) openForkedSessionTab(sourceTab *WorkspaceTab, sessionID string) (TabMeta, error) {
	if err := a.attachForkedDesktopSession(a.bootContext(), sourceTab, sessionID); err != nil {
		return TabMeta{}, err
	}
	opened, err := a.openForkedSessionTabWithWorkspace(sourceTab, forkedSessionLocator{SessionID: sessionID}, "")
	return opened.tab, err
}

// forkedSessionLocator addresses only a current immutable session identity.
type forkedSessionLocator struct {
	SessionID        string
	SourceSessionID  string
	SourceGeneration uint64
}

func normalizeForkedSessionLocator(sourceTab *WorkspaceTab, locator forkedSessionLocator) (forkedSessionLocator, error) {
	locator.SessionID = strings.TrimSpace(locator.SessionID)
	if sourceTab == nil {
		return forkedSessionLocator{}, errors.New("fork source is unavailable")
	}
	if err := validateLocalSessionRef(session.SessionRef{HostID: localDesktopHostID, SessionID: locator.SessionID}); err != nil {
		return forkedSessionLocator{}, err
	}
	if err := session.ValidateSessionID(locator.SessionID); err != nil {
		return forkedSessionLocator{}, err
	}
	return locator, nil
}

// prepareForkedTopic commits the presentation before a tab can publish it.
// Retries reuse the durable topic identity rather than making a second one.
func (a *App) prepareForkedTopic(locator forkedSessionLocator, scope, workspaceRoot, sourceTitle string) (string, string, string, error) {
	topicID := newTopicID()
	topicTitle := a.forkTopicTitle(sourceTitle)
	if err := a.workspaceRegistry().EnsureSessionTopic(a.bootContext(), locator.SessionID, topicID, topicTitle); err != nil {
		return "", "", "", err
	}
	root := ""
	if scope == "project" {
		root = workspaceRoot
	}
	a.emitProjectTreeChangedV2(a.currentSessionCatalogStatus().Revision, []string{root}, "membership")
	snapshot, err := a.workspaceRegistry().VerifySnapshot(a.bootContext())
	if err != nil {
		return "", "", "", err
	}
	presentation := snapshot.Session(locator.SessionID).Presentation
	topicTitle, titleSource := a.canonicalTabTitleWithPresentation(a.bootContext(), presentation,
		session.SessionRef{HostID: localDesktopHostID, SessionID: locator.SessionID})
	return presentation.TopicID, topicTitle, titleSource, nil
}

// openForkedSessionTabWithWorkspace attaches an already-written fork session to a new tab,
// optionally overriding the workspace root (e.g. for isolated Git worktrees).
func (a *App) openForkedSessionTabWithWorkspace(sourceTab *WorkspaceTab, locator forkedSessionLocator, workspaceRootOverride string) (forkedSessionTabOpen, error) {
	locator, err := normalizeForkedSessionLocator(sourceTab, locator)
	if err != nil {
		return forkedSessionTabOpen{}, err
	}
	a.mu.RLock()
	if a.tabs[sourceTab.ID] != sourceTab || (locator.SourceSessionID != "" &&
		(sourceTab.SessionID != locator.SourceSessionID || sourceTab.SessionGeneration != locator.SourceGeneration)) {
		a.mu.RUnlock()
		return forkedSessionTabOpen{}, nil
	}
	scope := sourceTab.Scope
	workspaceRoot := sourceTab.WorkspaceRoot
	if strings.TrimSpace(workspaceRootOverride) != "" {
		workspaceRoot = workspaceRootOverride
	}
	sourceTitle := sourceTab.TopicTitle
	model := sourceTab.model
	effort := cloneStringPtr(sourceTab.effort)
	mode := currentTabMode(sourceTab)
	disabledMCP := cloneServerViewMap(sourceTab.disabledMCP)
	mcpOrder := append([]string(nil), sourceTab.mcpOrder...)
	pinnedFiles := sourceTab.pinnedFilesState()
	sourceCtrl, sourceID, sourceGeneration := sourceTab.Ctrl, sourceTab.SessionID, sourceTab.SessionGeneration
	a.mu.RUnlock()
	// A fork is a new session: the source's preset was chosen for the source.
	toolApprovalMode := a.sessionPresets.restore(locator.SessionID, newSessionPreset(config.LoadForEdit(config.UserConfigPath())))
	mode = tabModeForPlan(tabModeHasPlan(mode))
	if scope == "project" {
		releaseAdmission, err := a.beginWorkspaceRuntimeAdmission(workspaceRoot)
		if err != nil {
			return forkedSessionTabOpen{}, err
		}
		defer releaseAdmission()
	}

	topicID, topicTitle, titleSource, err := a.prepareForkedTopic(locator, scope, workspaceRoot, sourceTitle)
	if err != nil {
		return forkedSessionTabOpen{}, err
	}
	workspace, err := a.canonicalSessionWorkspace(a.bootContext(), session.SessionRef{HostID: localDesktopHostID, SessionID: locator.SessionID})
	if err != nil {
		return forkedSessionTabOpen{}, err
	}
	if canonicalWorkspaceScope(workspace) != scope || !sameDesktopPath(workspace.Root, desktopWorkspaceRoot(scope, workspaceRoot)) {
		return forkedSessionTabOpen{}, errSessionWorkspaceConflict
	}
	opened := forkedSessionTabOpen{workspaceReferenced: strings.TrimSpace(workspaceRootOverride) != ""}
	if len(pinnedFiles) != 0 {
		if err := savePinnedContextState(desktopPinnedContextPath(locator.SessionID), pinnedFiles); err != nil {
			return opened, fmt.Errorf("persist fork pinned context: %w", err)
		}
	}

	if opened.workspaceReferenced && scope == "project" {
		if err := a.registerProjectRoot(workspaceRoot); err != nil {
			return forkedSessionTabOpen{}, err
		}
	}
	if hook := forkTabBeforePublishHookForTest.Load(); hook != nil {
		(*hook)()
	}

	a.mu.Lock()
	if a.tabs[sourceTab.ID] != sourceTab || sourceTab.Ctrl != sourceCtrl || sourceTab.SessionID != sourceID || sourceTab.SessionGeneration != sourceGeneration {
		a.mu.Unlock()
		return opened, nil
	}
	newTabID := a.newUniqueTabIDLocked()
	childID := locator.SessionID
	tab := &WorkspaceTab{
		ID:               newTabID,
		Scope:            scope,
		WorkspaceRoot:    workspaceRoot,
		TopicID:          topicID,
		TopicTitle:       topicTitle,
		topicTitleSource: titleSource,
		SessionID:        childID,
		SessionWorkspace: desktopTabWorkspace{ID: workspace.ID},
		model:            model,
		effort:           effort,
		mode:             mode,
		toolApprovalMode: toolApprovalMode,
		disabledMCP:      disabledMCP,
		mcpOrder:         mcpOrder,
	}
	tab.setPinnedFiles(pinnedFiles)
	tab.sink = &tabEventSink{tabID: newTabID, app: a}
	a.tabs[newTabID] = tab
	a.tabOrder = append(a.tabOrder, newTabID)
	activateFork := a.activeTabID == sourceTab.ID
	if activateFork {
		a.activeTabID = newTabID
	}
	a.saveTabsLocked()
	meta := a.tabMeta(tab, activateFork)
	a.mu.Unlock()

	if opened.workspaceReferenced && scope == "project" {
		if activateFork {
			saveWorkspace(workspaceRoot)
		}
	}
	a.emitProjectTreeChangedEvent()
	a.startTabControllerBuild(tab)
	opened.tab = meta
	return opened, nil
}

// attachForkedRewindTab fails closed when the durable branch cannot be attached
// to a tab. In particular, callers must not treat the source tab as the rewind
// target and accidentally resubmit the edited prompt into the parent session.
func (a *App) attachForkedRewindTab(sourceTab *WorkspaceTab, view RewindResultView) RewindResultView {
	meta, err := a.openForkedSessionTab(sourceTab, view.Branch)
	if err != nil || meta.ID == "" {
		slog.Warn("rewind: fork created but tab attach failed", "err", err)
		view.OK = false
		view.Partial = true
		view.Error = rewindForkAttachError
		return view
	}
	view.TabID = meta.ID
	view.Tab = &meta
	return view
}
