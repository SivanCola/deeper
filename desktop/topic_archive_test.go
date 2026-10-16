package main

import (
	"errors"
	"os"
	"testing"

	"context"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

type snapshotErrorSessionController struct {
	control.SessionAPI
	err error
}

func (c *snapshotErrorSessionController) Snapshot() error { return c.err }

func TestFailedStartupTopicSnapshotSkipsOnlyMissingCurrentRuntime(t *testing.T) {
	base := control.New(control.Options{})
	t.Cleanup(func() { base.Close(); <-base.Closed() })
	controller := &snapshotErrorSessionController{SessionAPI: base, err: session.ErrSessionNotRunning}
	item := removedSessionRuntime{ctrl: controller, failedStartup: true}
	app := &App{}
	if err := app.snapshotTopicRuntimeBindings([]removedSessionRuntime{item}); err != nil {
		t.Fatalf("failed startup could not be removed: %v", err)
	}
	item.failedStartup = false
	if err := app.snapshotTopicRuntimeBindings([]removedSessionRuntime{item}); !errors.Is(err, session.ErrSessionNotRunning) {
		t.Fatalf("normal runtime snapshot error hidden: %v", err)
	}
	item.failedStartup = true
	controller.err = errors.New("durable flush failed")
	if err := app.snapshotTopicRuntimeBindings([]removedSessionRuntime{item}); !errors.Is(err, controller.err) {
		t.Fatalf("failed startup durable error hidden: %v", err)
	}
}

func TestTrashTopicSnapshotFailureKeepsRuntimeAndFiles(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.desktopSessions.root = t.TempDir()
	t.Cleanup(app.closeSessionServices)
	base := controllerWithContent(t, app, "snapshot-failure")
	ref, _ := base.SessionRef()
	before, err := base.SessionService().Query().Stat(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	snapshotErr := errors.New("snapshot blocked")
	ctrl := &snapshotErrorSessionController{SessionAPI: base, err: snapshotErr}
	tab := &WorkspaceTab{ID: "snapshot-failure", Scope: "global", TopicID: "topic_snapshot_failure", TopicTitle: "Snapshot failure", SessionID: ref.SessionID, Ctrl: ctrl, Ready: true, disabledMCP: map[string]ServerView{}}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder, app.activeTabID = []string{tab.ID}, tab.ID
	if err := app.TrashTopic(tab.TopicID); !errors.Is(err, snapshotErr) {
		t.Fatalf("snapshot failure=%v", err)
	}
	if got := app.tabs[tab.ID]; got != tab || tab.removed || tab.Ctrl != ctrl {
		t.Fatal("snapshot failure changed runtime binding")
	}
	if after, bound := base.SessionRef(); !bound || after != ref {
		t.Fatal("snapshot failure changed current identity")
	}
	if _, err := os.Stat(before.Path); err != nil {
		t.Fatal("snapshot failure removed current storage", err)
	}
	if tab.TopicTitle != "Snapshot failure" {
		t.Fatal("snapshot failure changed topic title")
	}
}

func TestTrashTopicRejectsConcurrentRuntimeMutationWithoutWaiting(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_runtime_mutation_busy"
	tab := &WorkspaceTab{ID: "busy", TopicID: topicID, TopicTitle: "Runtime mutation busy"}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}}
	app.runtimeRebuildMu.Lock()
	err := app.TrashTopic(topicID)
	app.runtimeRebuildMu.Unlock()
	if !errors.Is(err, errTopicArchiveBusy) {
		t.Fatalf("TrashTopic error = %v, want %v", err, errTopicArchiveBusy)
	}
	// The rebuild mutex was held until after TrashTopic returned, so errTopicArchiveBusy
	// is a deterministic nonblocking proof without a shared-runner wall-clock limit.
	if got := tab.TopicTitle; got != "Runtime mutation busy" {
		t.Fatalf("busy archive changed topic title to %q", got)
	}
}

func TestTopicArchiveRejectsFailedRuntimeThatStartedRetrying(t *testing.T) {
	topicID := "topic_retrying_failure"
	tab := &WorkspaceTab{ID: "failed", Scope: "global", TopicID: topicID, SessionID: "retry-current"}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, tabOrder: []string{tab.ID}, activeTabID: tab.ID}
	app.mu.Lock()
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(sessionRoute(tab.SessionID)))
	app.markTabStartupFailureLocked(tab, errors.New("restore failed"), suppressStartupRestore)
	app.mu.Unlock()
	captured := app.captureTopicRuntimeBindings(topicID)

	app.mu.Lock()
	app.setSessionRuntimePhaseLocked(tab, sessionRuntimeStarting, nil)
	app.mu.Unlock()
	if _, unchanged := app.removeTopicRuntimeBindingsIfUnchanged(topicID, captured); unchanged {
		t.Fatal("archive detached a failed runtime after its retry changed generation state")
	}
	if app.tabs[tab.ID] != tab || tab.removed {
		t.Fatal("rejected stale archive changed the retrying tab")
	}
}

func TestTrashTopicForeignLeaseFailsBeforeCleanupCommit(t *testing.T) {
	app, ref := lifecycleFixture(t)
	const topicID = "topic_foreign_lease"
	if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, topicID, "Foreign lease"); err != nil {
		t.Fatal(err)
	}
	foreign, err := session.NewService(localDesktopHostID, session.NewFilesystemPersistence(app.desktopSessions.root))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := foreign.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	release := func() { _ = binding.Release(context.Background()); _ = foreign.Close(context.Background(), ref) }
	defer release()
	if err := app.TrashTopic(topicID); !errors.Is(err, errSessionBusyElsewhere) {
		t.Fatalf("foreign lease error = %v", err)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Active || state.Presentation[ref.SessionID].TopicID != topicID {
		t.Fatal("rejected archive changed identity or lifecycle")
	}
	release()
	if _, err := app.desktopSessionService("").Query().Snapshot(t.Context(), ref); err != nil {
		t.Fatalf("rejected archive removed session: %v", err)
	}
}
