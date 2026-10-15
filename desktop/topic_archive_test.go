package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"context"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

type snapshotErrorSessionController struct {
	control.SessionAPI
	err error
}

func (c *snapshotErrorSessionController) Snapshot() error { return c.err }

func TestTrashTopicSnapshotFailureKeepsRuntimeAndFiles(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	topicID := "topic_snapshot_failure"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Snapshot failure"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, dir, "snapshot-failure.jsonl", topicID, "Snapshot failure", projectRoot, "preserve me", time.Now())
	base := controllerWithContent(t, sessionPath)
	defer base.Close()
	snapshotErr := errors.New("snapshot blocked")
	ctrl := &snapshotErrorSessionController{SessionAPI: base, err: snapshotErr}
	tab := &WorkspaceTab{ID: "snapshot-failure", Scope: "project", WorkspaceRoot: projectRoot, TopicID: topicID,
		TopicTitle: "Snapshot failure", SessionPath: sessionPath, Ctrl: ctrl, Ready: true, disabledMCP: map[string]ServerView{}}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, tabOrder: []string{tab.ID}, activeTabID: tab.ID}
	if err := app.TrashTopic(topicID); !errors.Is(err, snapshotErr) {
		t.Fatalf("TrashTopic snapshot error = %v, want %v", err, snapshotErr)
	}
	if got := app.tabs[tab.ID]; got != tab || tab.removed {
		t.Fatalf("snapshot failure changed runtime binding: got=%p removed=%v", got, tab.removed)
	}
	if got := ctrl.SessionPath(); !sameDesktopPath(got, sessionPath) {
		t.Fatalf("snapshot failure session path = %q, want %q", got, sessionPath)
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("snapshot failure must not publish a cleanup-pending marker")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("snapshot failure removed the session file: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "Snapshot failure" {
		t.Fatalf("snapshot failure topic title = %q", got)
	}
}

func TestTrashTopicRejectsConcurrentRuntimeMutationWithoutWaiting(t *testing.T) {
	isolateDesktopUserDirs(t)
	topicID := "topic_runtime_mutation_busy"
	if err := setTopicTitle("", topicID, "Runtime mutation busy"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	app := &App{}
	app.runtimeRebuildMu.Lock()
	err := app.TrashTopic(topicID)
	app.runtimeRebuildMu.Unlock()
	if !errors.Is(err, errTopicArchiveBusy) {
		t.Fatalf("TrashTopic error = %v, want %v", err, errTopicArchiveBusy)
	}
	// The rebuild mutex was held until after TrashTopic returned, so errTopicArchiveBusy
	// is a deterministic nonblocking proof without a shared-runner wall-clock limit.
	if got := loadTopicTitle("", topicID); got != "Runtime mutation busy" {
		t.Fatalf("busy archive changed topic title to %q", got)
	}
}

func TestSnapshotTopicRuntimeConflictLogOmitsSessionPath(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "private-session.jsonl")
	base := control.New(control.Options{})
	defer base.Close()
	ctrl := &snapshotErrorSessionController{SessionAPI: base, err: &agent.SessionSnapshotConflictError{
		Path: privatePath,
		Kind: agent.SessionSnapshotConflictDiverged,
	}}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	defer slog.SetDefault(previous)

	app := NewApp()
	if err := app.snapshotTopicRuntimeBindings([]removedSessionRuntime{{ctrl: ctrl}}); err != nil {
		t.Fatalf("snapshotTopicRuntimeBindings: %v", err)
	}
	logged := output.String()
	if strings.Contains(logged, privatePath) {
		t.Fatalf("snapshot conflict log exposed the session path: %s", logged)
	}
	if !strings.Contains(logged, string(agent.SessionSnapshotConflictDiverged)) {
		t.Fatalf("snapshot conflict log omitted the safe conflict kind: %s", logged)
	}
}

// topicIndexedInRegistry is an assertion helper, not a production topic lookup.
func topicIndexedInRegistry(scope, workspaceRoot, topicID string) bool {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return false
	}
	if strings.TrimSpace(loadTopicTitles(topicTitleRoot(scope, workspaceRoot))[topicID]) != "" {
		return true
	}
	f := loadProjectsFile()
	if scope != "project" {
		return containsDesktopString(f.GlobalTopics, topicID)
	}
	if i := projectIndexByRoot(f.Projects, workspaceRoot); i >= 0 {
		return containsDesktopString(f.Projects[i].Topics, topicID)
	}
	return false
}

func TestTopicArchiveRejectsFailedRuntimeThatStartedRetrying(t *testing.T) {
	topicID := "topic_retrying_failure"
	tab := &WorkspaceTab{ID: "failed", Scope: "global", TopicID: topicID, SessionPath: "retrying.jsonl"}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, tabOrder: []string{tab.ID}, activeTabID: tab.ID}
	app.mu.Lock()
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(tab.SessionPath))
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
