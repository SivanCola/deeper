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

func TestLegacyTrashTopicCommittedCleanupFailureReconcilesWithoutFailureResponse(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	topicID := "topic_committed_cleanup"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Committed cleanup"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, dir, "committed-cleanup.jsonl", topicID, "Committed cleanup", projectRoot, "preserve me", time.Now())
	topicArchiveCleanupHookForTest = func() error { return errors.New("injected cleanup failure") }
	defer func() { topicArchiveCleanupHookForTest = nil }()

	app := NewApp()
	if err := app.trashTopic(topicID); err != nil {
		t.Fatalf("committed TrashTopic returned a failure: %v", err)
	}
	if !agent.IsCleanupPending(sessionPath) {
		t.Fatal("committed cleanup failure did not retain its durable marker")
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("committed archive retained topic title %q", got)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("deferred session disappeared before reconciliation: %v", err)
	}

	topicArchiveCleanupHookForTest = nil
	if err := reconcileDesktopCleanupPending(dir); err != nil {
		t.Fatalf("reconcile committed cleanup: %v", err)
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("reconciliation retained the cleanup marker")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("reconciled session still exists, err=%v", err)
	}
}

func TestTopicArchiveIntentCompletesSessionsAndMetadataAfterRestart(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	topicID := "topic_restart_commit"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Restart commit"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSessionWithPrompt(t, dir, "restart-commit.jsonl", topicID, "Restart commit", projectRoot, "preserve me", time.Now())
	targets := []topicTrashTarget{{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)}}
	if err := markTopicArchiveMetadataPending(topicID, targets); err != nil {
		t.Fatalf("mark archive intent: %v", err)
	}

	app := NewApp()
	if err := reconcileTopicArchiveMetadataPending(app.deleteTopic); err != nil {
		t.Fatalf("reconcile archive intent: %v", err)
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Fatalf("reconciled live session still exists, err=%v", err)
	}
	trashPath := filepath.Join(sessionTrashPath(dir), filepath.Base(sessionPath), filepath.Base(sessionPath))
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("reconciled session was not preserved in trash: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("reconciled archive retained topic title %q", got)
	}
	if agent.IsCleanupPending(sessionPath) {
		t.Fatal("reconciled archive retained session cleanup marker")
	}
	if _, err := os.Stat(topicArchiveMetadataPendingPath(topicID)); !os.IsNotExist(err) {
		t.Fatalf("reconciled archive retained topic marker, err=%v", err)
	}
}

func TestTopicArchiveOwnershipRollbackRestoresLocalLease(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(dir, "rollback-lease.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	tab := &WorkspaceTab{ID: "rollback", SessionPath: sessionPath, Ready: true}
	if err := tab.ensureSessionLease(sessionPath); err != nil {
		t.Fatalf("ensureSessionLease: %v", err)
	}
	defer tab.releaseSessionLease()
	removed := []removedSessionRuntime{{tab: tab, sessionDir: dir, sessionPath: sessionPath}}
	targets := []topicTrashTarget{{dir: dir, sessionPath: sessionPath, key: filepath.Base(sessionPath)}}

	batch, err := acquireTopicArchiveOwnership(targets, removed)
	if err != nil {
		t.Fatalf("acquireTopicArchiveOwnership: %v", err)
	}
	if tab.sessionLeaseRuntimeKey() != "" {
		batch.rollback()
		t.Fatal("converted ownership remained attached to the runtime tab")
	}
	batch.rollback()
	if got := tab.sessionLeaseRuntimeKey(); got != sessionRuntimeKey(sessionPath) {
		t.Fatalf("rolled back lease key = %q, want %q", got, sessionRuntimeKey(sessionPath))
	}
	if next, err := agent.TryAcquireSessionLease(sessionPath); !errors.Is(err, agent.ErrSessionLeaseHeld) {
		if next != nil {
			next.Release()
		}
		t.Fatalf("competing lease after rollback err = %v, want ErrSessionLeaseHeld", err)
	}
}

func TestUnusedTransientBlankSessionOnlyMatchesZeroByteFiles(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
	zero := filepath.Join(dir, "zero.jsonl")
	writeZeroByteSession(t, zero)
	if !unusedTransientBlankSession(dir, zero) {
		t.Fatal("zero-byte unindexed session should be an unused transient blank")
	}
	nonzero := filepath.Join(dir, "nonzero.jsonl")
	if err := os.WriteFile(nonzero, []byte(`{"role":"system","content":"identity"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write non-empty session: %v", err)
	}
	if unusedTransientBlankSession(dir, nonzero) {
		t.Fatal("non-empty session must not be classified as an unused transient blank")
	}
}

func writeZeroByteSession(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write zero-byte session %s: %v", path, err)
	}
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
