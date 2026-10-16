package main

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

// Detaching a tab moves registry ownership without closing its current writer.
func TestDetachRuntimeForReplacementPreservesCurrentExecution(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	ctrl := newCurrentRebuildController(t, app, control.Options{Sink: event.Discard})
	ref, _ := ctrl.SessionRef()
	_, runtime, _ := ctrl.SessionBinding()
	generation := ctrl.ExecutionGeneration()
	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ctrl: ctrl, Ready: true}
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.mu.Lock()
	app.runtimeForTabLocked(tab)
	app.mu.Unlock()
	if !app.detachRuntimeForReplacement(tab) {
		t.Fatal("current runtime did not detach")
	}
	detached := app.detachedSessions[sessionRoute(ref.SessionID)]
	if detached == nil || detached.Ctrl != ctrl || detached.SessionID != ref.SessionID {
		t.Fatal("detach lost current controller identity")
	}
	if tab.runtimeID != "" || detached.runtimeID == "" {
		t.Fatal("runtime registry ownership did not transfer")
	}
	if !runtime.OwnsExecution(generation) {
		t.Fatal("detach released current execution ownership")
	}
	if _, err := runtime.Session().AppendBatch(t.Context(), "after-detach", []session.Event{{Kind: "diagnostic", Optional: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestDetachRuntimeForReplacementSkipsRemovedTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}

	removed := &WorkspaceTab{ID: "removed", removed: true}
	app := &App{tabs: map[string]*WorkspaceTab{"removed": removed}}
	if app.detachRuntimeForReplacement(removed) {
		t.Fatal("removed tab was detached into the background registry")
	}
	if len(app.detachedSessions) != 0 {
		t.Fatal("removed tab left an entry in detachedSessions")
	}

	orphan := &WorkspaceTab{ID: "orphan"}
	if app.detachRuntimeForReplacement(orphan) {
		t.Fatal("tab absent from a.tabs was detached into the background registry")
	}
	if len(app.detachedSessions) != 0 {
		t.Fatal("orphan tab left an entry in detachedSessions")
	}
}

// TestTabEventSinkEmitConcurrentRebind: Emit keeps running on the controller
// goroutine while detach/reattach rebinds the sink's tab routing. Run with
// -race; before setBinding existed the tabID write raced every Emit.
func TestTabEventSinkEmitConcurrentRebind(t *testing.T) {
	sink := &tabEventSink{tabID: "before"}
	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range iterations {
			sink.Emit(event.Event{Kind: event.Notice, Text: "hammer"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := range iterations {
			sink.setBinding(fmt.Sprintf("tab-%d", i%2), nil)
			sink.clearContext()
		}
	}()
	wg.Wait()
	if tabID, _ := sink.binding(); tabID == "" {
		t.Fatal("sink lost its tab binding")
	}
}

// TestTabBuildSupersededByRebindGeneration: a session rebind bumps
// buildGeneration to strand any in-flight async build, so its swap (and every
// mid-build field write) is rejected; synchronous rebuilds pass generation 0
// and rely on runtimeRebuildMu instead.
func TestTabBuildSupersededByRebindGeneration(t *testing.T) {
	tab := &WorkspaceTab{ID: "tab"}
	app := &App{tabs: map[string]*WorkspaceTab{"tab": tab}}
	app.mu.Lock()
	tab.buildGeneration = 3
	app.mu.Unlock()
	if app.tabBuildSuperseded(tab, 3) {
		t.Fatal("build with the current generation must not be superseded")
	}
	app.mu.Lock()
	tab.buildGeneration++ // the rebind-side invalidation
	app.mu.Unlock()
	if !app.tabBuildSuperseded(tab, 3) {
		t.Fatal("stale-generation build must be superseded after a rebind bump")
	}
	if app.tabBuildSuperseded(tab, 0) {
		t.Fatal("synchronous rebuild (generation 0) must not be superseded by generation bumps")
	}
	app.mu.Lock()
	tab.removed = true
	app.mu.Unlock()
	if !app.tabBuildSuperseded(tab, 0) {
		t.Fatal("removed tab must supersede every build, including synchronous ones")
	}
}

// Superseded builds release only their own controller and host reference.
func TestAbandonSupersededBuildPreservesNewBuildOwnership(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	replacement := newCurrentRebuildController(t, app, control.Options{Sink: event.Discard})
	_, runtime, _ := replacement.SessionBinding()
	generation := replacement.ExecutionGeneration()
	stale := control.New(control.Options{Sink: event.Discard})
	t.Cleanup(stale.Close)
	app.acquireSharedHost("root")
	app.acquireSharedHost("root")
	tab := &WorkspaceTab{ID: "tab", Ctrl: replacement, SharedHostKey: "root"}
	app.tabs[tab.ID] = tab
	app.abandonSupersededBuild(stale, "root")
	<-stale.Closed()
	if tab.Ctrl != replacement || tab.SharedHostKey != "root" || !runtime.OwnsExecution(generation) {
		t.Fatal("stale cleanup changed published ownership")
	}
	app.sharedHostsMu.Lock()
	entry := app.sharedHosts["root"]
	refs := 0
	if entry != nil {
		refs = entry.refs
	}
	app.sharedHostsMu.Unlock()
	if refs != 1 {
		t.Fatalf("shared host refs=%d, want replacement's one reference", refs)
	}
	app.releaseSharedHost("root")
}

func TestMetaForTabConcurrentWithBuildSwap(t *testing.T) {
	isolateDesktopUserDirs(t)
	tab := &WorkspaceTab{ID: "tab", Scope: "project", WorkspaceRoot: t.TempDir()}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"tab": tab},
		tabOrder:    []string{"tab"},
		activeTabID: "tab",
	}

	const iterations = 100
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range iterations {
			app.mu.Lock()
			tab.Ready = !tab.Ready
			tab.Label = fmt.Sprintf("model-%d", i)
			tab.StartupErr = ""
			tab.model = fmt.Sprintf("provider/m%d", i)
			tab.goal = fmt.Sprintf("goal-%d", i)
			app.mu.Unlock()
		}
	}()
	for range iterations {
		meta := app.MetaForTab("tab")
		if meta.EventChannel == "" {
			t.Fatal("MetaForTab returned zero meta for a live tab")
		}
	}
	<-done
}
