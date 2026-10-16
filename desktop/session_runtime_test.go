package main

import (
	"context"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
	"deepzora/internal/tool"
)

func TestSessionParentLive(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	path := sessionRoute("rebuild-current")
	otherPath := sessionRoute("other-current")
	if app.sessionParentLive(path) {
		t.Fatal("session without a tab or runtime reported live")
	}

	building := &WorkspaceTab{ID: "building", SessionID: "rebuild-current"}
	app.tabs[building.ID] = building
	if !app.sessionParentLive(path) {
		t.Fatal("building tab session did not report live")
	}
	if app.sessionParentLive(otherPath) {
		t.Fatal("unrelated session reported live")
	}
	delete(app.tabs, building.ID)

	ctrl := newCurrentRebuildController(t, app, control.Options{Label: "ready"})
	t.Cleanup(ctrl.Close)
	ready := &WorkspaceTab{ID: "ready", Ctrl: ctrl, Ready: true}
	app.tabs[ready.ID] = ready
	if !app.sessionParentLive(path) {
		t.Fatal("ready controller session did not report live")
	}
	delete(app.tabs, ready.ID)

	detached := &WorkspaceTab{ID: "detached"}
	app.detachedSessions["detached"] = detached
	app.mu.Lock()
	app.newSessionRuntimeLocked(detached, sessionRuntimeKey(path))
	app.mu.Unlock()
	if !app.sessionParentLive(path) {
		t.Fatal("detached runtime session did not report live")
	}
}

func TestCleanupStaleRunningWithCurrentParentWriter(t *testing.T) {
	sessionDir := t.TempDir()
	parentSession := "parent"
	parents, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(sessionDir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parents.Shutdown(context.Background()) })
	parent, err := parents.Create(t.Context(), session.CreateOptions{SessionID: parentSession, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	parentRef := parent.Ref()
	if err := parents.Close(t.Context(), parentRef); err != nil {
		t.Fatal(err)
	}

	children, err := parents.Subagents()
	if err != nil {
		t.Fatal(err)
	}
	store := agent.NewSubagentStore(subagentpersist.New(children))
	prepareRunning := func() string {
		run, err := store.PrepareFresh(agent.SubagentSpec{
			Kind:          "task",
			Name:          "task",
			WorkspaceRoot: t.TempDir(),
			ParentSession: parentSession,
			SystemPrompt:  "sys",
			Registry:      tool.NewRegistry(),
			Model:         "model",
		})
		if err != nil {
			t.Fatalf("PrepareFresh: %v", err)
		}
		if err := store.MarkRunning(run); err != nil {
			t.Fatalf("MarkRunning: %v", err)
		}
		ref := run.Ref
		run.Release()
		return ref
	}
	requireStatus := func(ref string, want agent.SubagentStatus) {
		meta, err := store.LoadMeta(ref)
		if err != nil {
			t.Fatalf("LoadMeta(%s): %v", ref, err)
		}
		if meta.Status != want {
			t.Fatalf("status(%s) = %q, want %q", ref, meta.Status, want)
		}
	}

	crashRef := prepareRunning()
	cleaned, err := store.WithParentIdleGuard(parents.Query().WhileIdle).CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning for initial owner build: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("cleaned for initial owner build = %d, want 1", cleaned)
	}
	requireStatus(crashRef, agent.SubagentInterrupted)

	binding, err := parents.Open(t.Context(), parentRef)
	if err != nil {
		t.Fatal(err)
	}
	liveRef := prepareRunning()
	cleaned, err = store.CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning with live parent: %v", err)
	}
	if cleaned != 0 {
		t.Fatalf("cleaned with live parent = %d, want 0", cleaned)
	}
	requireStatus(liveRef, agent.SubagentRunning)

	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := parents.Close(t.Context(), parentRef); err != nil {
		t.Fatal(err)
	}
	laterRef := prepareRunning()
	cleaned, err = agent.NewSubagentStore(subagentpersist.New(children)).
		WithParentIdleGuard(parents.Query().WhileIdle).
		CleanupStaleRunning()
	if err != nil {
		t.Fatalf("CleanupStaleRunning after parent exit: %v", err)
	}
	if cleaned != 2 {
		t.Fatalf("cleaned after parent exit = %d, want 2", cleaned)
	}
	requireStatus(liveRef, agent.SubagentInterrupted)
	requireStatus(laterRef, agent.SubagentInterrupted)
}

func TestMetaNeverReportsReadyWithoutController(t *testing.T) {
	app := NewApp()
	tab := &WorkspaceTab{
		ID:         "tab_broken_ready",
		Scope:      "global",
		Ready:      true, // compatibility field from an older persisted/runtime path
		StartupErr: "startup failed",
	}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	meta := app.MetaForTab(tab.ID)
	if meta.Ready {
		t.Fatal("Meta reported ready with a nil controller")
	}
	if meta.Runtime.Phase != sessionRuntimeFailed {
		t.Fatalf("runtime phase = %q, want %q", meta.Runtime.Phase, sessionRuntimeFailed)
	}
}

func TestDeferredStartupWaitsForCurrentWriterAndPreservesIdentity(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "STARTUP_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "local/test"
	cfg.Desktop.ProviderAccess = []string{"local"}
	cfg.Providers = []config.ProviderEntry{{Name: "local", Kind: "openai", Models: []string{"test"}, Default: "test", Model: "test", BaseURL: "https://example.invalid/v1", APIKeyEnv: "STARTUP_TEST_KEY"}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.desktopSessionService("")
	owner, err := session.NewService("local", session.NewFilesystemPersistence(app.desktopSessions.root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.CloseAll(context.Background()) })
	runtime, err := owner.Create(t.Context(), session.CreateOptions{SessionID: "startup-current", CWD: globalTabWorkspaceRoot(), Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	if err := owner.SetModel(t.Context(), ref, "local/test", ""); err != nil {
		t.Fatal(err)
	}
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "local/test", "waiting")
	tab.SessionID = ref.SessionID
	workspaceID, err := app.attachDesktopSession(t.Context(), tab.Scope, tab.WorkspaceRoot, ref)
	if err != nil {
		t.Fatal(err)
	}
	tab.SessionWorkspace.ID = workspaceID
	setTabStartupError(tab, session.ErrWriterOwned)
	tab.Ready = false
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	installNoopRuntimeEvents(app, tab.sink)
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if ctrl := app.controllerForTab(tab); ctrl != nil {
			ctrl.Close()
		}
		app.releaseSharedHost(tab.SharedHostKey)
		app.closeSessionServices()
	})
	app.mu.Lock()
	app.bindSessionRuntimeKeyLocked(tab, sessionRoute(ref.SessionID))
	app.setSessionRuntimePhaseLocked(tab, sessionRuntimeLeaseBlocked, session.ErrWriterOwned)
	app.mu.Unlock()
	app.scheduleDeferredStartupBuild(tab.ID)

	before := tab.buildGeneration
	app.retryDeferredStartupBuild(tab.ID, tab)
	if tab.Ctrl != nil || !tab.StartupErrLeaseHeld || tab.Ready {
		t.Fatalf("occupied session became usable: ctrl=%v held=%v ready=%v error=%q", tab.Ctrl != nil, tab.StartupErrLeaseHeld, tab.Ready, tab.StartupErr)
	}
	if strings.Contains(tab.StartupErr, ref.SessionID) || strings.Contains(tab.StartupErr, app.desktopSessions.root) {
		t.Fatalf("startup error exposes storage identity: %s", tab.StartupErr)
	}
	app.mu.RLock()
	view := app.sessionRuntimeViewLocked(tab)
	app.mu.RUnlock()
	if view.Phase != sessionRuntimeLeaseBlocked || view.Issue == nil || !view.Issue.Retryable {
		t.Fatalf("occupied session lost retry state: %+v", view)
	}
	if err := owner.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	app.retryDeferredStartupBuild(tab.ID, tab)
	if tab.Ctrl == nil {
		t.Fatalf("released session did not reopen: %s", tab.StartupErr)
	}
	gotRef, ok := tab.Ctrl.(interface {
		SessionRef() (session.SessionRef, bool)
	}).SessionRef()
	if !ok || gotRef != ref {
		t.Fatalf("retry changed identity: got=%+v want=%+v", gotRef, ref)
	}
	if tab.buildGeneration != before+2 {
		t.Fatalf("build generation = %d, want one occupied attempt and one successful retry from %d", tab.buildGeneration, before)
	}
	app.mu.RLock()
	view = app.sessionRuntimeViewLocked(tab)
	runtimeCount := len(app.runtimeBySessionKey)
	app.mu.RUnlock()
	if view.Phase != sessionRuntimeReady || tab.StartupErrLeaseHeld || !tab.Ready {
		t.Fatalf("released session did not become ready: %+v", view)
	}
	if runtimeCount != 1 {
		t.Fatalf("runtime key count = %d, want one", runtimeCount)
	}
}

func TestStartingRuntimePlaceholderIsNotOverwrittenByDuplicateTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	path := sessionRoute("rebuild-current")
	first := &WorkspaceTab{
		ID:        "first",
		SessionID: "rebuild-current",
		sink:      &tabEventSink{tabID: "first", app: app},
	}
	second := &WorkspaceTab{
		ID:        "second",
		SessionID: "rebuild-current",
		sink:      &tabEventSink{tabID: "second", app: app},
	}
	app.tabs[first.ID] = first
	app.tabs[second.ID] = second

	app.mu.Lock()
	app.setSessionRuntimePhaseLocked(first, sessionRuntimeStarting, nil)
	app.setSessionRuntimePhaseLocked(second, sessionRuntimeStarting, nil)
	key := sessionRuntimeKey(path)
	owner := app.runtimeBySessionKey[key]
	runtimeCount := len(app.runtimeBySessionKey)
	secondRuntimeID := second.runtimeID
	app.mu.Unlock()

	if owner == nil || owner.Owner != first {
		t.Fatalf("starting placeholder owner = %#v, want first tab", owner)
	}
	if runtimeCount != 1 {
		t.Fatalf("runtime key count = %d, want one", runtimeCount)
	}
	if secondRuntimeID != "" {
		t.Fatalf("duplicate tab created private runtime %q before claim", secondRuntimeID)
	}

	// A starting placeholder is not an attachable runtime: its controller and
	// lease are still private to the owner build. Attaching it would remove the
	// owner tab and cause both candidate builds to retire.
	if app.attachExistingSessionRuntime(second, path, context.Background()) {
		t.Fatal("starting runtime attached before its controller was published")
	}
	if app.tabs[first.ID] != first || app.tabs[second.ID] != second {
		t.Fatal("starting runtime attach removed one of the restoring tabs")
	}
	if first.Ctrl != nil || second.Ctrl != nil {
		t.Fatal("starting runtime attach published a controller")
	}

	// Once the owner publishes its controller and ready phase, the same claim
	// attaches that runtime and retires only the duplicate visible tab.
	ctrl := newCurrentRebuildController(t, app, control.Options{Label: "ready"})
	t.Cleanup(ctrl.Close)
	app.mu.Lock()
	first.Ctrl = ctrl
	first.Ready = true
	app.advanceSessionRuntimeEpochLocked(first)
	app.mu.Unlock()
	if !app.claimSessionRuntime(second, path, context.Background()) {
		t.Fatal("ready runtime was not attached by the duplicate claim")
	}
	app.mu.RLock()
	readyOwner := app.runtimeBySessionKey[key]
	firstStillVisible := app.tabs[first.ID]
	secondStillVisible := app.tabs[second.ID]
	view := app.sessionRuntimeViewLocked(second)
	app.mu.RUnlock()
	if readyOwner == nil || readyOwner.Owner != second || second.Ctrl != ctrl {
		t.Fatalf("ready runtime owner/controller = %#v/%p, want second/%p", readyOwner, second.Ctrl, ctrl)
	}
	if firstStillVisible != nil || secondStillVisible != second {
		t.Fatalf("visible tabs after attach = first %#v second %#v", firstStillVisible, secondStillVisible)
	}
	if view.Phase != sessionRuntimeReady {
		t.Fatalf("attached runtime phase = %q, want ready", view.Phase)
	}
}

func TestBackendRejectsSubmitWhenRuntimeIsNotReady(t *testing.T) {
	app := NewApp()
	ctrl := &runtimeStatusSessionController{}
	tab := &WorkspaceTab{ID: "blocked", Ctrl: ctrl, Ready: true}
	app.tabs[tab.ID] = tab
	app.activeTabID = tab.ID
	app.mu.Lock()
	app.setSessionRuntimePhaseLocked(tab, sessionRuntimeLeaseBlocked, &sessionLeaseBusyError{})
	app.mu.Unlock()

	err := app.SubmitToTabWithID(tab.ID, "must not send", newSessionRuntimeID("test-submit"))
	if err == nil || !strings.Contains(err.Error(), "workspace failed to start") {
		t.Fatalf("SubmitToTab error = %v, want runtime readiness failure", err)
	}
	if status := ctrl.RuntimeStateSnapshot(); status.ActiveWork() || status.CancelRequested || status.Cancellable {
		t.Fatalf("controller status changed after rejected submit: %+v", status)
	}
}
