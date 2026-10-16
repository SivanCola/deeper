package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

type blockingPinnedProvider struct {
	started    chan struct{}
	release    chan struct{}
	cancelled  chan struct{}
	once       sync.Once
	cancelOnce sync.Once
}

func (p *blockingPinnedProvider) Name() string { return "blocking-pinned" }

func (p *blockingPinnedProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	p.once.Do(func() { close(p.started) })
	if p.cancelled == nil {
		<-p.release
	} else {
		select {
		case <-p.release:
		case <-ctx.Done():
			p.cancelOnce.Do(func() { close(p.cancelled) })
			<-p.release
		}
	}
	ch := make(chan provider.Chunk, 2)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: "ok"}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func pinnedConcurrencyFixture(t *testing.T, prov provider.Provider) (*App, *WorkspaceTab, *control.Controller, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	app := NewApp()
	pinDesktopSessionRoot(t, app)
	app.ctx = context.Background()
	exec := agent.New(prov, tool.NewRegistry(), agent.NewSession("BASE"), agent.Options{}, event.Discard)
	ctrl := newCurrentRebuildController(t, app, control.Options{
		Runner: exec, Executor: exec, SystemPrompt: "BASE", WorkspaceRoot: root,
		PinnedContextLoader: pinnedContextLoader(root), Label: "test", Sink: event.Discard,
	})
	path, err := ctrl.PinnedContextPath(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{
		ID:            "pinned-race",
		Scope:         "project",
		WorkspaceRoot: root,
		Ready:         true,
		Ctrl:          ctrl,
		disabledMCP:   map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(ctrl.Close)
	return app, tab, ctrl, path
}

func TestPinAndUnpinRejectWhileTurnIsRunning(t *testing.T) {
	prov := &blockingPinnedProvider{started: make(chan struct{}), release: make(chan struct{})}
	app, tab, ctrl, path := pinnedConcurrencyFixture(t, prov)
	if err := os.WriteFile(filepath.Join(tab.WorkspaceRoot, "context.md"), []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PinFileForTab(tab.ID, "context.md"); err != nil {
		t.Fatalf("initial pin: %v", err)
	}
	turnDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(prov.release) }) }
	go func() { defer close(finished); turnDone <- ctrl.RunTurn(ctx, "hold") }()
	t.Cleanup(func() {
		release()
		cancel()
		<-finished
		ctrl.Close()
		<-ctrl.Closed()
	})
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		stacks := make([]byte, 2<<20)
		t.Logf("provider startup timeout goroutines:\n%s", stacks[:runtime.Stack(stacks, true)])
		t.Fatal("turn did not reach provider")
	}

	if err := app.UnpinFileForTab(tab.ID, "context.md"); err == nil {
		t.Fatal("Unpin succeeded while a turn was running")
	}
	if _, err := app.PinFileForTab(tab.ID, "context.md"); err == nil {
		t.Fatal("duplicate Pin succeeded while a turn was running")
	}
	state, err := loadPinnedContextState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Files) != 1 || state.Files[0] != "context.md" {
		t.Fatalf("busy mutations changed sidecar: %v", state.Files)
	}
	release()
	if err := <-turnDone; err != nil {
		t.Fatalf("turn: %v", err)
	}
}

func TestGetPinnedFilesForTabReturnsNonNilEmptyList(t *testing.T) {
	app, tab, _, _ := pinnedConcurrencyFixture(t, nil)
	infos, err := app.GetPinnedFilesForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if infos == nil || len(infos) != 0 {
		t.Fatalf("empty pinned files = %#v, want []", infos)
	}
}

func TestGetPinnedFilesForTabDoesNotOverwriteNewerCachedPins(t *testing.T) {
	app, tab, _, path := pinnedConcurrencyFixture(t, nil)
	for _, name := range []string{"old.md", "new.md"} {
		if err := os.WriteFile(filepath.Join(tab.WorkspaceRoot, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := savePinnedContextState(path, []string{"old.md"}); err != nil {
		t.Fatal(err)
	}

	// Model the stale-read window: Get loaded the old sidecar while a newer
	// Pin/Unpin or session binding already published the current tab cache.
	tab.setPinnedFiles([]string{"new.md"})
	infos, err := app.GetPinnedFilesForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Path != "old.md" {
		t.Fatalf("GetPinnedFilesForTab = %#v, want old sidecar snapshot", infos)
	}
	if got := tab.GetPinnedFiles(); len(got) != 1 || got[0] != "new.md" {
		t.Fatalf("read-only GetPinnedFilesForTab overwrote newer cache: %v", got)
	}
}

func TestCanonicalPinnedContextRestoresBySessionIdentity(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	if err := os.WriteFile(filepath.Join(tab.WorkspaceRoot, "context.md"), []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PinFileForTab(tab.ID, "context.md"); err != nil {
		t.Fatal(err)
	}
	restored := &WorkspaceTab{SessionID: tab.SessionID}
	restoreTabPinnedContext(restored)
	if got := restored.GetPinnedFiles(); len(got) != 1 || got[0] != "context.md" {
		t.Fatalf("restored pins = %v", got)
	}
	if got, err := app.GetPinnedFilesForTab(tab.ID); err != nil || len(got) != 1 || got[0].Path != "context.md" {
		t.Fatalf("current pins = %+v, %v", got, err)
	}
	if tab.Ctrl.SessionRoute() != sessionRoute(tab.SessionID) {
		t.Fatal("pinned context changed the execution identity")
	}
}

func TestNewSessionWaitsForPinAndClearsItsResult(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	root := tab.WorkspaceRoot
	if err := os.WriteFile(filepath.Join(root, "context.md"), []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}

	readStarted := make(chan struct{})
	releaseRead := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRead) }) }
	t.Cleanup(release)
	hook := func() {
		once.Do(func() { close(readStarted) })
		<-releaseRead
	}
	pinnedFileReadHookForTest.Store(&hook)
	t.Cleanup(func() { pinnedFileReadHookForTest.Store(nil) })
	pinDone := make(chan error, 1)
	go func() {
		_, err := app.PinFileForTab(tab.ID, "context.md")
		pinDone <- err
	}()
	select {
	case <-readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Pin did not reach file read")
	}
	newBeforeLock := make(chan struct{})
	var newBeforeLockOnce sync.Once
	app.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "new session" {
			newBeforeLockOnce.Do(func() { close(newBeforeLock) })
		}
	}
	newDone := make(chan error, 1)
	go func() { newDone <- app.NewSessionForTab(tab.ID) }()
	select {
	case <-newBeforeLock:
	case <-time.After(5 * time.Second):
		t.Fatal("NewSession did not reach the runtime mutation barrier")
	}
	select {
	case err := <-newDone:
		t.Fatalf("NewSession bypassed in-flight Pin: %v", err)
	default:
	}
	release()
	if err := <-pinDone; err != nil {
		t.Fatalf("PinFileForTab: %v", err)
	}
	if err := <-newDone; err != nil {
		t.Fatalf("NewSessionForTab: %v", err)
	}
	statePath, err := tab.Ctrl.PinnedContextPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadPinnedContextState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Files) != 0 || len(tab.GetPinnedFiles()) != 0 {
		t.Fatalf("new session inherited racing pin: sidecar=%v cache=%v", state.Files, tab.GetPinnedFiles())
	}
	identity, ok := tab.Ctrl.(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		t.Fatal("new session did not enter the current store")
	}
	ref, bound := identity.SessionRef()
	if !bound {
		t.Fatal("new session has no durable identity")
	}
	history, err := identity.SessionService().Query().History(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if agent.IsPinnedContextRevision(message) {
			t.Fatal("new session persisted the racing pin")
		}
	}
	if got := tab.Ctrl.SystemPrompt(); got == "" || strings.Contains(got, "<pinned_context>") {
		t.Fatalf("new controller retained pinned prompt: %q", got)
	}
}

func TestRunningClearDoesNotMigrateOldPinnedCache(t *testing.T) {
	prov := &blockingPinnedProvider{started: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
	app, tab, ctrl, oldPath := pinnedConcurrencyFixture(t, prov)
	oldRef, _ := configureSwitchableDefaultModels(t)
	t.Cleanup(func() {
		if tab.Ctrl != nil && tab.Ctrl != ctrl {
			tab.Ctrl.Close()
		}
	})
	tab.model = oldRef
	if err := os.WriteFile(filepath.Join(tab.WorkspaceRoot, "context.md"), []byte("context"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PinFileForTab(tab.ID, "context.md"); err != nil {
		t.Fatal(err)
	}
	go ctrl.Submit("hold")
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not start")
	}
	clearDone := make(chan error, 1)
	go func() {
		_, err := app.ClearSessionForTab(tab.ID)
		clearDone <- err
	}()
	select {
	case <-prov.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("clear did not cancel the running turn")
	}
	select {
	case err := <-clearDone:
		t.Fatalf("clear completed before the blocked provider stopped: %v", err)
	default:
	}
	close(prov.release)
	if err := <-clearDone; err != nil {
		t.Fatalf("ClearSessionForTab: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old pinned sidecar survived clear: %v", err)
	}
	if got := tab.GetPinnedFiles(); len(got) != 0 {
		t.Fatalf("running clear retained cached pins: %v", got)
	}
	statePath, err := tab.Ctrl.PinnedContextPath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadPinnedContextState(statePath)
	if err != nil || len(state.Files) != 0 {
		t.Fatalf("replacement pins = %+v, err=%v", state, err)
	}
}
