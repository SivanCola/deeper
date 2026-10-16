package main

import (
	"context"
	"deepzora/internal/event"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"deepzora/internal/checkpoint"
	"deepzora/internal/control"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

type tabScopedActionController struct {
	stubSessionAPI
	history           []provider.Message
	newSessionCalls   int
	clearSessionCalls int
	rewindCalls       int
	compactCalls      int
	forkCalls         int
	summarizeFrom     int
	summarizeUpTo     int
}

func newTabScopedActionController() *tabScopedActionController {
	return &tabScopedActionController{history: []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "keep this tab distinct"},
	}}
}

func (c *tabScopedActionController) RuntimeStateSnapshot() event.RuntimeStateSnapshot {
	return runtimeSnapshotForTest(event.RuntimeStateSnapshot{})
}
func (c *tabScopedActionController) PlanMode() bool         { return false }
func (c *tabScopedActionController) AutoApproveTools() bool { return false }
func (c *tabScopedActionController) Goal() string           { return "" }
func (c *tabScopedActionController) ToolApprovalMode() string {
	return control.ToolApprovalReadOnly
}
func (c *tabScopedActionController) History() []provider.Message {
	return append([]provider.Message(nil), c.history...)
}
func (c *tabScopedActionController) WorkspaceRoot() string { return "" }
func (c *tabScopedActionController) SessionDir() string    { return "" }
func (c *tabScopedActionController) SessionRoute() string  { return "" }
func (c *tabScopedActionController) PinnedContextPath(context.Context) (string, error) {
	return "", nil
}
func (c *tabScopedActionController) Snapshot() error { return nil }
func (c *tabScopedActionController) NewSession() error {
	c.newSessionCalls++
	c.history = []provider.Message{{Role: provider.RoleSystem, Content: "system"}}
	return nil
}
func (c *tabScopedActionController) ClearSession() error {
	c.clearSessionCalls++
	c.history = []provider.Message{{Role: provider.RoleSystem, Content: "system"}}
	return nil
}
func (c *tabScopedActionController) PrepareRewind(_ int, _ control.RewindScope) (checkpoint.RewindPlan, error) {
	return checkpoint.RewindPlan{PlanID: "tab-scoped", CanFiles: true, CanConversation: true}, nil
}
func (c *tabScopedActionController) CommitRewind(_ string) (checkpoint.RewindResult, error) {
	c.rewindCalls++
	return checkpoint.RewindResult{OK: true}, nil
}
func (c *tabScopedActionController) Compact(_ context.Context, _ string) error {
	c.compactCalls++
	return nil
}
func (c *tabScopedActionController) SummarizeFrom(_ context.Context, _ int) error {
	c.summarizeFrom++
	return nil
}
func (c *tabScopedActionController) SummarizeUpTo(_ context.Context, _ int) error {
	c.summarizeUpTo++
	return nil
}

type forkActionController struct {
	*tabScopedActionController
}

func (c *forkActionController) ForkTargets() (session.ForkTargetSet, error) {
	return session.ForkTargetSet{Source: session.SessionRef{HostID: localDesktopHostID, SessionID: "source"},
		Verifiable: true, Targets: []session.ForkTarget{{TurnNumber: 1, TurnID: "turn", BoundarySequence: 3, Available: true}}}, nil
}

func (c *forkActionController) CreateForkSession(session.ForkRequest, string) (string, error) {
	c.forkCalls++
	return "", errors.New("stop after selecting source controller")
}

func (c *forkActionController) CheckpointForkRequest(int) (session.ForkRequest, error) {
	return session.ForkRequest{Source: session.SessionRef{HostID: localDesktopHostID, SessionID: "source"}, TurnID: "turn", BeforeTurn: true}, nil
}

type blockingForkTabController struct {
	*tabScopedActionController
	service *session.Service
	source  session.SessionRef
	childID string
	started chan struct{}
	release chan struct{}
}

func (c *blockingForkTabController) ForkTargets() (session.ForkTargetSet, error) {
	return c.service.ForkTargetSetFor(context.Background(), c.source)
}

func (c *blockingForkTabController) CheckpointForkRequest(turn int) (session.ForkRequest, error) {
	set, err := c.ForkTargets()
	if err != nil {
		return session.ForkRequest{}, err
	}
	if turn < 0 || turn >= len(set.Targets) {
		return session.ForkRequest{}, errors.New("unknown checkpoint")
	}
	target := set.Targets[turn]
	return session.ForkRequest{Source: set.Source, TurnID: target.TurnID, BeforeTurn: true, BoundarySequence: target.StartSequence - 1}, nil
}

func (c *blockingForkTabController) CreateForkSession(request session.ForkRequest, _ string) (string, error) {
	close(c.started)
	<-c.release
	result, err := c.service.CreateFork(context.Background(), request)
	c.childID = result.Child.SessionID
	return c.childID, err
}

func enableBlockingForkPersistence(t *testing.T, app *App, ctrl *blockingForkTabController) {
	t.Helper()
	app.ctx = t.Context()
	pinDesktopSessionRoot(t, app)
	ctrl.service = app.desktopSessionService("")
	t.Cleanup(app.closeSessionServices)
	tab := app.tabs["test"]
	runtime, err := ctrl.service.Create(t.Context(), session.CreateOptions{SessionID: "source",
		CWD: desktopWorkspaceRoot(tab.Scope, tab.WorkspaceRoot), Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	ctrl.source = runtime.Ref()
	tab.SessionID = ctrl.source.SessionID
	workspace, err := app.attachDesktopSession(t.Context(), tab.Scope, tab.WorkspaceRoot, ctrl.source)
	if err != nil {
		t.Fatal(err)
	}
	tab.SessionWorkspace.ID = workspace
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "turn", TurnID: "turn", Events: []session.Event{
		{Kind: "turn/start"}, {Kind: "message/complete", Payload: json.RawMessage(`{"message":{"id":"answer","role":"assistant","content":"saved fork history"}}`)},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "second", TurnID: "second", Events: []session.Event{
		{Kind: "turn/start"}, {Kind: "message/complete", Payload: json.RawMessage(`{"message":{"id":"second-answer","role":"assistant","content":"excluded from fork"}}`)},
		{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestTabScopedSessionActionsIgnoreFocusedTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	targetCtrl := &forkActionController{newTabScopedActionController()}
	focusedCtrl := newTabScopedActionController()
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	installNoopRuntimeEvents(app)
	app.tabs = map[string]*WorkspaceTab{
		"target":  {ID: "target", SessionID: "source", Scope: "global", Ready: true, Ctrl: targetCtrl, disabledMCP: map[string]ServerView{}},
		"focused": {ID: "focused", Scope: "global", Ready: true, Ctrl: focusedCtrl, disabledMCP: map[string]ServerView{}},
	}
	app.tabOrder, app.activeTabID = []string{"target", "focused"}, "focused"
	app.tabs["target"].model, _ = desktopNewSessionDefaults("global", "")
	// The action spy tests routing; presentation writes still require the same
	// registered current identity as a production tab.
	runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{
		SessionID: "source", CWD: globalWorkspaceRoot(), Origin: session.SessionOriginNew,
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := app.attachDesktopSession(t.Context(), "global", "", runtime.Ref())
	if err != nil {
		t.Fatal(err)
	}
	app.tabs["target"].SessionWorkspace.ID = workspace
	if err := app.desktopSessionService("").Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}

	if err := app.NewSessionForTab("target"); err != nil {
		t.Fatalf("NewSessionForTab: %v", err)
	}
	// Restore content so ClearSession exercises its non-blank path too.
	targetCtrl.history = []provider.Message{{Role: provider.RoleSystem, Content: "system"}, {Role: provider.RoleUser, Content: "clear me"}}
	if _, err := app.ClearSessionForTab("target"); err != nil {
		t.Fatalf("ClearSessionForTab: %v", err)
	}
	if result := app.CommitRewindForTab("target", "", 1, "conversation"); !result.OK {
		t.Fatalf("CommitRewindForTab: %+v", result)
	}
	if err := app.CompactForTab("target"); err != nil {
		t.Fatalf("CompactForTab: %v", err)
	}
	if _, err := app.ForkForTab("target", 1); err == nil || err.Error() != "stop after selecting source controller" {
		t.Fatalf("ForkForTab error = %v, want source-controller sentinel", err)
	}
	if err := app.SummarizeFromForTab("target", 1); err != nil {
		t.Fatalf("SummarizeFromForTab: %v", err)
	}
	if err := app.SummarizeUpToForTab("target", 1); err != nil {
		t.Fatalf("SummarizeUpToForTab: %v", err)
	}

	if targetCtrl.newSessionCalls != 1 || targetCtrl.clearSessionCalls != 1 || targetCtrl.rewindCalls != 1 || targetCtrl.compactCalls != 1 || targetCtrl.forkCalls != 1 || targetCtrl.summarizeFrom != 1 || targetCtrl.summarizeUpTo != 1 {
		t.Fatalf("target calls = new:%d clear:%d rewind:%d compact:%d fork:%d from:%d upto:%d, want all 1",
			targetCtrl.newSessionCalls, targetCtrl.clearSessionCalls, targetCtrl.rewindCalls, targetCtrl.compactCalls, targetCtrl.forkCalls, targetCtrl.summarizeFrom, targetCtrl.summarizeUpTo)
	}
	if focusedCtrl.newSessionCalls != 0 || focusedCtrl.clearSessionCalls != 0 || focusedCtrl.rewindCalls != 0 || focusedCtrl.compactCalls != 0 || focusedCtrl.forkCalls != 0 || focusedCtrl.summarizeFrom != 0 || focusedCtrl.summarizeUpTo != 0 {
		t.Fatalf("focused tab received scoped action: %+v", focusedCtrl)
	}
	if app.activeTabID != "focused" {
		t.Fatalf("active tab = %q, want focused", app.activeTabID)
	}
}

func TestForkForTabDoesNotOverrideLaterActiveTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	ctrl := &blockingForkTabController{
		tabScopedActionController: newTabScopedActionController(),
		started:                   make(chan struct{}),
		release:                   make(chan struct{}),
	}
	// Only the persistence-to-publish interleaving matters here; fork itself
	// need not run in another goroutine or race a wall-clock deadline.
	close(ctrl.release)
	app := NewApp()
	app.ctx = context.Background()
	app.setTestCtrl(ctrl, "")
	app.tabs["test"].TopicTitle = "Source topic"
	enableBlockingForkPersistence(t, app, ctrl)
	app.tabs["other"] = &WorkspaceTab{ID: "other", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	app.tabOrder = []string{"test", "other"}

	// Match the app's asynchronous build path, but cancel and join the build
	// before it acquires controllers or session leases unrelated to this test.
	buildRelease := make(chan struct{})
	app.tabBuildStartHook = func(string) { <-buildRelease }
	t.Cleanup(func() {
		app.mu.Lock()
		var builds []<-chan struct{}
		for _, tab := range app.tabs {
			if tab.buildDone != nil {
				builds = append(builds, tab.buildDone)
			}
			app.supersedeTabBuildLocked(tab)
		}
		app.mu.Unlock()
		close(buildRelease)
		for _, done := range builds {
			if !waitChannelBefore(done, time.Now().Add(5*time.Second)) {
				t.Error("fork controller build did not exit after cancellation")
			}
		}
	})
	hook := func() {
		if err := app.SetActiveTab("other"); err != nil {
			t.Fatalf("SetActiveTab(other): %v", err)
		}
	}
	forkTabBeforePublishHookForTest.Store(&hook)
	t.Cleanup(func() { forkTabBeforePublishHookForTest.Store(nil) })

	meta, err := app.ForkForTab("test", 1)
	if err != nil {
		t.Fatalf("ForkForTab: %v", err)
	}
	if meta.ID == "" || meta.ID == "test" {
		t.Fatalf("fork tab ID = %q, want a fresh tab", meta.ID)
	}
	if meta.Active {
		t.Fatalf("fork meta active = true, want false after later tab switch")
	}
	if app.activeTabID != "other" {
		t.Fatalf("active tab = %q, want later selection %q", app.activeTabID, "other")
	}
}
