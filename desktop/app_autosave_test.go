package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }

func (stubProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 1)
	close(ch)
	return ch, nil
}

func controllerWithContent(t *testing.T, path string) *control.Controller {
	t.Helper()
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "remember this turn"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "acknowledged"})
	ag := agent.New(stubProvider{}, tool.NewRegistry(), sess, agent.Options{}, event.Discard)
	ctrl := control.New(control.Options{Executor: ag, SessionDir: filepath.Dir(path), SessionPath: path, Sink: event.Discard})
	t.Cleanup(ctrl.Close)
	return ctrl
}

func waitForFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("file %q never contained %q", path, want)
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
func requireAutosaved(t *testing.T, tab *WorkspaceTab, path, want string) {
	t.Helper()
	waitForAutosaveIdle(t, tab)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session file after autosave: %v", err)
	}
	if !strings.Contains(string(b), want) {
		t.Fatalf("session file %q does not contain %q after autosave went idle", path, want)
	}
}

func appWithTab(t *testing.T, path string) (*App, *WorkspaceTab) {
	t.Helper()
	ctrl := controllerWithContent(t, path)
	tab := &WorkspaceTab{
		ID:            "test_tab",
		Ctrl:          ctrl,
		Scope:         "global",
		WorkspaceRoot: "",
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: nil}
	a := &App{
		tabs:        map[string]*WorkspaceTab{"test_tab": tab},
		activeTabID: "test_tab",
	}
	tab.sink.app = a
	t.Cleanup(func() { waitForAutosaveIdle(t, tab) })
	return a, tab
}

// TestTurnDonePersistsSession proves a completed turn is written to disk without
// any explicit Snapshot call — the desktop autosave the data-loss fix adds. A
// nil sink ctx (no webview) must not disable persistence.
func TestTurnDonePersistsSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	_, tab := appWithTab(t, path)

	tab.sink.Emit(event.Event{Kind: event.TurnDone})

	requireAutosaved(t, tab, path, "remember this turn")
}

// TestNonTurnDoneDoesNotPersist confirms only TurnDone triggers a save, so the
// per-token event storm doesn't thrash the disk.
func TestNonTurnDoneDoesNotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	a, tab := appWithTab(t, path)
	_ = a

	tab.sink.Emit(event.Event{Kind: event.Text, Text: "tok"})

	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a non-TurnDone event wrote the session file (err=%v)", err)
	}
}

// TestScheduleSnapshotCoalesces hammers the scheduler concurrently to prove the
// single-flight loop neither panics nor drops the final write.
func TestScheduleSnapshotCoalesces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	a, tab := appWithTab(t, path)
	_ = a

	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			tab.sink.Emit(event.Event{Kind: event.TurnDone})
		})
	}
	wg.Wait()

	requireAutosaved(t, tab, path, "acknowledged")
}

func TestAutosaveFailureRetriesAndRecoversOnNextTurnDone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.jsonl")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir blocked path: %v", err)
	}
	a, tab := appWithTab(t, path)
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
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("blocked session path should still be the directory, info=%v err=%v", info, err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove blocked dir: %v", err)
	}
	a.mu.Lock()
	tab.Ctrl = ctrl
	a.mu.Unlock()
	tab.sink.Emit(event.Event{Kind: event.TurnDone})
	requireAutosaved(t, tab, path, "remember this turn")

	tab.saveMu.Lock()
	failures = tab.saveFailures
	tab.saveMu.Unlock()
	if failures != 0 {
		t.Fatalf("autosave failures after recovery = %d, want 0", failures)
	}
}

func TestSetActiveTabBlocksWhenCurrentSessionCannotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.jsonl")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir blocked path: %v", err)
	}
	a, _ := appWithTab(t, path)
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

func TestRebindSessionBlocksWhenCurrentSessionCannotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.jsonl")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir blocked path: %v", err)
	}
	a, tab := appWithTab(t, path)
	target := filepath.Join(t.TempDir(), "target.jsonl")
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "target prompt"})
	if err := sess.Save(target); err != nil {
		t.Fatalf("save target: %v", err)
	}
	loaded, err := agent.LoadSession(target)
	if err != nil {
		t.Fatalf("load target: %v", err)
	}

	err = a.rebindTabToLoadedSessionPath(tab, target, loaded)
	if err == nil || !strings.Contains(err.Error(), "save current session before switching sessions") {
		t.Fatalf("rebind error = %v, want persistence failure", err)
	}
	if tab.Ctrl == nil || tab.Ctrl.SessionPath() != path {
		t.Fatalf("tab controller/path changed after failed save: ctrl=%v path=%q", tab.Ctrl, tab.currentSessionPath())
	}
}

// TestCloseTabNoResurrectionFromAutosave is the regression test for #4384.
// It proves that after CloseTab returns, the per-turn autosave goroutine can no
// longer write the session file — even when it is in flight at the moment the
// tab is closed. Pre-fix, the loop held a raw *WorkspaceTab pointer and a
// captured session path, so its Snapshot() call landed after DeleteSession
// trashed the file, "resurrecting" it.
func TestCloseTabNoResurrectionFromAutosave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")

	doomed, doomedTab := appWithTab(t, path)
	// CloseTab needs >1 tab and mutates activeTabID, so add a survivor tab.
	survivor := &WorkspaceTab{
		ID:          "survivor_tab",
		Scope:       "global",
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	survivor.sink = &tabEventSink{tabID: survivor.ID, app: doomed}
	doomed.tabs["survivor_tab"] = survivor
	doomed.activeTabID = "test_tab"

	// Write the session file once via the autosave loop, then wait for idle so
	// the next TurnDone reliably kicks off a fresh loop.
	doomedTab.sink.Emit(event.Event{Kind: event.TurnDone})
	requireAutosaved(t, doomedTab, path, "acknowledged")

	// Kick the autosave loop and close the tab in close succession. The loop
	// will be in flight when CloseTab runs — exactly the #4384 window.
	doomedTab.sink.Emit(event.Event{Kind: event.TurnDone})
	if err := doomed.CloseTab("test_tab"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}

	// CloseTab must have returned only after the autosave loop finished. Remove
	// the file the way DeleteSession would (move to trash is just a remove here
	// since we only care that nothing rewrites the original path).
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove session file: %v", err)
	}

	// Give any would-be resurrection a chance to strike. If the autosave loop
	// were still alive (the bug), the file reappears here.
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file resurrected after CloseTab + delete (stat err=%v) — autosave loop not drained", err)
	}

	// And the controller's session path must be cleared so no future Snapshot
	// can write either.
	if got := doomedTab.Ctrl.SessionPath(); got != "" {
		t.Fatalf("controller session path = %q after CloseTab, want empty so snapshots no-op", got)
	}
}

func TestCloseTabBlocksWhenSessionCannotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.jsonl")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir blocked path: %v", err)
	}
	a, tab := appWithTab(t, path)
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
	if tab.Ctrl == nil || tab.Ctrl.SessionPath() != path {
		t.Fatalf("tab controller/path changed after failed close: ctrl=%v path=%q", tab.Ctrl, tab.currentSessionPath())
	}
}

// TestCloseTabSurvivorKeepsAutosave ensures the survivor tab is untouched: the
// closing/drain logic is per-tab and must not leak to other tabs.
func TestCloseTabSurvivorKeepsAutosave(t *testing.T) {
	doomedPath := filepath.Join(t.TempDir(), "doomed.jsonl")
	survivorPath := filepath.Join(t.TempDir(), "survivor.jsonl")

	a, _ := appWithTab(t, doomedPath)
	survivorCtrl := controllerWithContent(t, survivorPath)
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
	requireAutosaved(t, survivor, survivorPath, "acknowledged")

	if err := a.CloseTab("test_tab"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}

	if got := survivor.Ctrl.SessionPath(); got != survivorPath {
		t.Fatalf("survivor session path = %q, want %q", got, survivorPath)
	}
	if survivor.closing {
		t.Fatal("survivor tab was marked closing — closing flag leaked across tabs")
	}
}

func TestLegacyDeleteSessionClearsRemovedRuntimeSessionPath(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "delete-open.jsonl")
	ctrl := controllerWithContent(t, path)
	tab := &WorkspaceTab{
		ID:          "delete_open",
		Scope:       "global",
		Ready:       true,
		Ctrl:        ctrl,
		disabledMCP: map[string]ServerView{},
	}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"delete_open": tab},
		activeTabID: "delete_open",
	}
	if err := ctrl.Snapshot(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if err := app.deleteSession(path); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if got := ctrl.SessionPath(); got != "" {
		t.Fatalf("removed controller session path = %q, want empty before trash move can race Windows file locks", got)
	}
	trashPath := filepath.Join(dir, sessionTrashDir, "delete-open.jsonl", "delete-open.jsonl")
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("session should be in trash: %v", err)
	}
}
