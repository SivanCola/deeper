package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 1)
	close(ch)
	return ch, nil
}

func controllerWithContent(t *testing.T, app *App, id string) *control.Controller {
	t.Helper()
	service := app.desktopSessionService("")
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "remember this turn"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "acknowledged"})
	ag := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, Sink: event.Discard, SessionService: service, SessionCreateService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed() })
	return ctrl
}

// autosaveDeadlineMargin leaves room to report a stuck loop and run cleanups
// before the test binary's own timeout panics.
const autosaveDeadlineMargin = 5 * time.Second

// waitForAutosaveIdle waits for the loop to finish, which the bounded save
// waits and retries guarantee. It sets no bound of its own: those waits alone
// can outlast any fixed one on a slow disk, so only the binary deadline caps it.
func waitForAutosaveIdle(t *testing.T, tab *WorkspaceTab) {
	t.Helper()
	deadline, bounded := t.Deadline()
	for {
		tab.saveMu.Lock()
		idle := !tab.saving && !tab.saveAgain
		tab.saveMu.Unlock()
		if idle {
			return
		}
		if bounded && time.Until(deadline) < autosaveDeadlineMargin {
			t.Fatal("autosave loop still running at the test binary deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requireAutosaved waits for the loop the caller started, then checks what it
// left on disk; polling the file instead would put a clock on the disk.
func requireAutosaved(t *testing.T, tab *WorkspaceTab, want string) {
	t.Helper()
	waitForAutosaveIdle(t, tab)
	ctrl := tab.Ctrl.(*control.Controller)
	ref, _ := ctrl.SessionRef()
	state, err := ctrl.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range state.Projection.Messages {
		if strings.Contains(m.Content, want) {
			return
		}
	}
	t.Fatalf("current durable history does not contain %q", want)
}

func appWithTab(t *testing.T) (*App, *WorkspaceTab) {
	t.Helper()
	a := NewApp()
	a.desktopSessions.root = t.TempDir()
	t.Cleanup(a.closeSessionServices)
	ctrl := controllerWithContent(t, a, "autosave-current")
	tab := &WorkspaceTab{
		ID:            "test_tab",
		SessionID:     "autosave-current",
		Ctrl:          ctrl,
		Scope:         "global",
		WorkspaceRoot: "",
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: nil}
	a.tabs = map[string]*WorkspaceTab{"test_tab": tab}
	a.activeTabID = "test_tab"
	tab.sink.app = a
	t.Cleanup(func() { waitForAutosaveIdle(t, tab) })
	return a, tab
}

// TestTurnDonePersistsSession proves a completed turn is written to disk without
// any explicit Snapshot call — the desktop autosave the data-loss fix adds. A
// nil sink ctx (no webview) must not disable persistence.
func TestTurnDonePersistsSession(t *testing.T) {
	_, tab := appWithTab(t)

	tab.sink.Emit(event.Event{Kind: event.TurnDone})

	requireAutosaved(t, tab, "remember this turn")
}

// TestNonTurnDoneDoesNotPersist confirms only TurnDone triggers a save, so the
// per-token event storm doesn't thrash the disk.
type countingAutosaveController struct {
	control.SessionAPI
	calls int
}

func (c *countingAutosaveController) Snapshot() error { c.calls++; return c.SessionAPI.Snapshot() }

func TestNonTurnDoneDoesNotPersist(t *testing.T) {
	_, tab := appWithTab(t)
	counted := &countingAutosaveController{SessionAPI: tab.Ctrl}
	tab.Ctrl = counted
	tab.sink.Emit(event.Event{Kind: event.Text, Text: "tok"})
	waitForAutosaveIdle(t, tab)
	if counted.calls != 0 {
		t.Fatal("non-terminal event triggered an autosave")
	}
}

// TestScheduleSnapshotCoalesces hammers the scheduler concurrently to prove the
// single-flight loop neither panics nor drops the final write.
func TestScheduleSnapshotCoalesces(t *testing.T) {
	a, tab := appWithTab(t)
	_ = a

	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			tab.sink.Emit(event.Event{Kind: event.TurnDone})
		})
	}
	wg.Wait()

	requireAutosaved(t, tab, "acknowledged")
}

func TestAutosaveFailureRetriesAndRecoversOnNextTurnDone(t *testing.T) {
	a, tab := appWithTab(t)
	ctrl := tab.Ctrl
	// Retry ownership does not depend on how quickly the filesystem rejects
	// a write. Inject that failure, then use the real controller for recovery.
	tab.Ctrl = &snapshotErrorSessionController{SessionAPI: ctrl, err: os.ErrPermission}

	tab.sink.Emit(event.Event{Kind: event.TurnDone})
	waitForAutosaveIdle(t, tab)

	tab.saveMu.Lock()
	failures := tab.saveFailures
	tab.saveMu.Unlock()
	if failures != maxTabSnapshotFailureRetries+1 {
		t.Fatalf("autosave failures = %d, want %d attempts", failures, maxTabSnapshotFailureRetries+1)
	}
	a.mu.Lock()
	tab.Ctrl = ctrl
	a.mu.Unlock()
	tab.sink.Emit(event.Event{Kind: event.TurnDone})
	requireAutosaved(t, tab, "remember this turn")

	tab.saveMu.Lock()
	failures = tab.saveFailures
	tab.saveMu.Unlock()
	if failures != 0 {
		t.Fatalf("autosave failures after recovery = %d, want 0", failures)
	}
}

func TestSetActiveTabBlocksWhenCurrentSessionCannotPersist(t *testing.T) {
	a, _ := appWithSnapshotFailure(t)
	a.tabs["target_tab"] = &WorkspaceTab{
		ID:          "target_tab",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	a.tabOrder = []string{"test_tab", "target_tab"}

	err := a.SetActiveTab("target_tab")
	if err == nil || !strings.Contains(err.Error(), "save current session before switching tabs") {
		t.Fatalf("SetActiveTab error = %v, want persistence failure", err)
	}
	if a.activeTabID != "test_tab" {
		t.Fatalf("active tab = %q, want original tab after failed save", a.activeTabID)
	}
}

// Closing joins the current runtime's autosave before deletion, and stale
// callbacks cannot restart the saved tab or recreate its deleted session.
func TestCloseTabNoResurrectionFromAutosave(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	app.desktopSessions.root = t.TempDir()
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "remember this turn"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "acknowledged"})
	exec := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := newCurrentRebuildController(t, app, control.Options{Executor: exec, Sink: event.Discard})
	tab := &WorkspaceTab{ID: "closing", Ctrl: ctrl, Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	survivor := &WorkspaceTab{ID: "survivor", Scope: "global", Ready: true, disabledMCP: map[string]ServerView{}}
	survivor.sink = &tabEventSink{tabID: survivor.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab, survivor.ID: survivor}
	app.tabOrder = []string{tab.ID, survivor.ID}
	app.activeTabID = tab.ID
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	installNoopRuntimeEvents(app, tab.sink, survivor.sink)
	ref, _ := ctrl.SessionRef()
	service := ctrl.SessionService()

	saved, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	app.lifecycleCheckpointHook = func(phase string) {
		if phase == "before-autosave-publish" {
			close(saved)
			<-release
		}
	}
	app.scheduleTabSnapshot(tab.ID)
	awaitRemovalTest(t, saved)
	done := make(chan error, 1)
	go func() { done <- app.CloseTab(tab.ID) }()
	select {
	case err := <-done:
		t.Fatalf("close returned before joining autosave: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	if err := awaitRemovalTest(t, done); err != nil {
		t.Fatal(err)
	}
	<-ctrl.Closed()
	tab.saveMu.Lock()
	drained := tab.closing && !tab.saving && !tab.saveAgain
	tab.saveMu.Unlock()
	if !drained {
		t.Fatal("close did not drain the current autosave")
	}
	app.lifecycleCheckpointHook = nil
	if err := service.Delete(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	// Replaying a late completion must be harmless after both close and delete.
	app.scheduleTabSnapshot(tab.ID)
	waitForAutosaveIdle(t, tab)
	if _, err := service.Query().Stat(t.Context(), ref); err == nil {
		t.Fatal("deleted current session was recreated")
	}
	if _, running := service.Runtime(ref); running {
		t.Fatal("stale callback reopened a runtime")
	}
}

func TestCloseTabBlocksWhenSessionCannotPersist(t *testing.T) {
	a, tab := appWithSnapshotFailure(t)
	survivor := &WorkspaceTab{
		ID:          "survivor_tab",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	survivor.sink = &tabEventSink{tabID: survivor.ID, app: a}
	a.tabs[survivor.ID] = survivor
	a.tabOrder = []string{tab.ID, survivor.ID}

	err := a.CloseTab(tab.ID)
	if err == nil || !strings.Contains(err.Error(), "save current session before closing tab") {
		t.Fatalf("CloseTab error = %v, want persistence failure", err)
	}
	if _, ok := a.tabs[tab.ID]; !ok {
		t.Fatal("tab was removed even though its session could not be saved")
	}
	if tab.Ctrl == nil || tab.SessionID != "autosave-current" {
		t.Fatalf("tab controller/path changed after failed close: ctrl=%v path=%q", tab.Ctrl, tab.currentSessionIdentity())
	}
}

// TestCloseTabSurvivorKeepsAutosave ensures the survivor tab is untouched: the
// closing/drain logic is per-tab and must not leak to other tabs.
func TestCloseTabSurvivorKeepsAutosave(t *testing.T) {
	a, _ := appWithTab(t)
	survivorCtrl := controllerWithContent(t, a, "survivor-current")
	survivor := &WorkspaceTab{
		ID:          "survivor_tab",
		Ctrl:        survivorCtrl,
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	survivor.sink = &tabEventSink{tabID: survivor.ID, app: a}
	a.tabs["survivor_tab"] = survivor
	a.activeTabID = "test_tab"

	survivor.sink.Emit(event.Event{Kind: event.TurnDone})
	requireAutosaved(t, survivor, "acknowledged")

	if err := a.CloseTab("test_tab"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}

	if ref, _ := survivorCtrl.SessionRef(); ref.SessionID != "survivor-current" {
		t.Fatalf("survivor identity changed: %+v", ref)
	}
	if survivor.closing {
		t.Fatal("survivor tab was marked closing — closing flag leaked across tabs")
	}
}
