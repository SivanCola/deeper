package main

import (
	"bytes"
	"deepzora/internal/config"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

func newSavedTabReconcileTestApp(t *testing.T) *App {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	pinDesktopSessionRoot(t, app)
	installNoopRuntimeEvents(app)
	return app
}

func finishSavedTabRecovery(app *App) {
	close(app.desktopRecoveryDone)
}

func savedProjectTab(id, sessionID, operationID, root, workspaceID string) desktopTabEntry {
	return desktopTabEntry{
		ID: id, Scope: "project", WorkspaceRoot: root, WorkspaceID: workspaceID,
		TopicID: "topic-" + id, SessionID: sessionID, CreateOperationID: operationID,
	}
}

func TestReconcileSavedTabsDropsMissingCanonicalWithoutOwner(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	finishSavedTabRecovery(app)
	root := t.TempDir()
	file := desktopTabsFile{
		Tabs:      []desktopTabEntry{savedProjectTab("stale", "missing-session", "", root, desktopWorkspaceID("project", root))},
		ActiveTab: "stale", TabOrder: []string{"stale"},
	}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if !changed || len(got.Tabs) != 0 || got.ActiveTab != "" || len(got.TabOrder) != 0 {
		t.Fatalf("stale tab reconciliation = changed:%v file:%+v", changed, got)
	}
}

func TestReconcileSavedTabsSelectsValidFallbackAfterDroppingActive(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	finishSavedTabRecovery(app)
	root := t.TempDir()
	file := desktopTabsFile{
		Tabs:       []desktopTabEntry{savedProjectTab("stale", "missing-session", "", root, desktopWorkspaceID("project", root))},
		RemoteTabs: []desktopRemoteTabEntry{{ID: "remote", HostID: "host", Workspace: "/workspace"}},
		ActiveTab:  "stale", RemoteTabOrder: []string{"remote"}, TabOrder: []string{"stale", "remote"},
	}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if !changed || len(got.Tabs) != 0 || len(got.RemoteTabs) != 1 || got.ActiveTab != "remote" {
		t.Fatalf("fallback selection = changed:%v file:%+v", changed, got)
	}
	single := singleSurfaceTabsFile(got)
	if len(single.RemoteTabs) != 1 || single.RemoteTabs[0].ID != "remote" {
		t.Fatalf("single-surface fallback discarded valid remote tab: %+v", single)
	}
}

func TestReconcileSavedTabsDiagnosticDoesNotExposeIdentityOrPath(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	finishSavedTabRecovery(app)
	secretPath := filepath.Join(t.TempDir(), "private-customer-path", "missing.jsonl")
	const secretSessionID = "private-session-identity"
	file := desktopTabsFile{Tabs: []desktopTabEntry{{
		ID: "private-tab", Scope: "global", TopicID: "topic", SessionID: secretSessionID, SessionPath: secretPath,
	}}, ActiveTab: "private-tab"}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	if _, changed := app.reconcileSavedTabs(t.Context(), file); !changed {
		t.Fatal("missing saved tab was not reconciled")
	}
	logBody := output.String()
	if strings.Contains(logBody, secretPath) || strings.Contains(logBody, secretSessionID) || strings.Contains(logBody, "private-tab") {
		t.Fatalf("saved-tab diagnostic exposed private identity: %s", logBody)
	}
	for _, field := range []string{"outcome=drop_stale_presentation", "reason=canonical_identity_absent", "identity_kind=canonical", "waited_for_recovery=false"} {
		if !strings.Contains(logBody, field) {
			t.Fatalf("saved-tab diagnostic missing %q: %s", field, logBody)
		}
	}
}

func TestReconcileSavedTabsRetainsMatchingPendingCreateAfterRecovery(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	finishSavedTabRecovery(app)
	root := t.TempDir()
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "project", root)
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "pending-session"
	const operationID = "pending-operation"
	if err := app.workspaceRegistry().BeginCreate(t.Context(), workspacestate.PendingCreate{
		OperationID: operationID, WorkspaceID: workspaceID, SessionID: sessionID,
	}); err != nil {
		t.Fatal(err)
	}
	file := desktopTabsFile{Tabs: []desktopTabEntry{savedProjectTab("pending", sessionID, operationID, root, workspaceID)}, ActiveTab: "pending"}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if changed || len(got.Tabs) != 1 || got.Tabs[0].CreateOperationID != operationID {
		t.Fatalf("pending create was not retained: changed=%v file=%+v", changed, got)
	}
}

func TestReconcileSavedTabsDropsArchivedPresentationButKeepsSession(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	root := t.TempDir()
	ref, workspaceID := createCurrentRegistrySession(t, app, root, "archived-session", true)
	if err := app.archiveSessionRefsWithOperation([]session.SessionRef{ref}, "archive-test"); err != nil {
		t.Fatal(err)
	}
	file := desktopTabsFile{Tabs: []desktopTabEntry{savedProjectTab("archived", ref.SessionID, "", root, workspaceID)}, ActiveTab: "archived"}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if !changed || len(got.Tabs) != 0 {
		t.Fatalf("archived presentation retained: changed=%v file=%+v", changed, got)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Archived {
		t.Fatalf("archived session lifecycle = %q", state.SessionStates[ref.SessionID].Lifecycle)
	}
	if _, err := app.desktopSessionService("").Query().Stat(t.Context(), ref); err != nil {
		t.Fatalf("archived session content was removed: %v", err)
	}
}

func TestReconcileSavedTabsPreservesUnregisteredCanonicalContent(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	root := t.TempDir()
	runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{
		SessionID: "unregistered-content", CWD: root, Origin: session.SessionOriginNew,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.desktopSessionService("").Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	finishSavedTabRecovery(app)
	file := desktopTabsFile{Tabs: []desktopTabEntry{savedProjectTab("recover", runtime.Ref().SessionID, "", root, desktopWorkspaceID("project", root))}, ActiveTab: "recover"}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if changed || len(got.Tabs) != 1 {
		t.Fatalf("unregistered canonical content was discarded: changed=%v file=%+v", changed, got)
	}
}

func TestReconcileSavedTabsPreservesEmptyWorkspaceConflictWithoutRetiredDraftReads(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	canonicalRoot := t.TempDir()
	ref, workspaceID := createCurrentRegistrySession(t, app, canonicalRoot, "empty-workspace-conflict", false)

	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[workspaceID]
	workspace.Root = t.TempDir()
	state.Workspaces[workspaceID] = workspace
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.workspaceRegistry().Path(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	finishSavedTabRecovery(app)
	file := desktopTabsFile{
		Tabs:       []desktopTabEntry{savedProjectTab("empty", ref.SessionID, "", canonicalRoot, workspaceID)},
		RemoteTabs: []desktopRemoteTabEntry{{ID: "remote", HostID: "host", Workspace: "/workspace"}},
		ActiveTab:  "remote", TabOrder: []string{"remote", "empty"},
	}
	body = mustMarshalJSON(t, file)
	if err := os.MkdirAll(desktopConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), tabsFileName), body, 0o600); err != nil {
		t.Fatal(err)
	}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if changed || len(got.Tabs) != 1 || len(got.RemoteTabs) != 1 || !got.Tabs[0].restoreBlocked {
		t.Fatalf("formal workspace conflict was not retained for repair: changed=%v file=%+v", changed, got)
	}
	if persisted := loadTabsFile(); len(persisted.RemoteTabs) != 1 {
		t.Fatalf("preflight archive overwrote unpublished tabs: %+v", persisted)
	}
	state, err = app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Active {
		t.Fatalf("empty formal session lifecycle = %q, want active", state.SessionStates[ref.SessionID].Lifecycle)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(config.DesktopSessionUIStatePath()), "drafts-v1.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("workspace conflict opened the retired draft database: %v", err)
	}
	if len(app.tabs) != 0 || len(app.runtimeByID) != 0 {
		t.Fatal("empty-session reconciliation created a replacement runtime")
	}
}

func TestReconcileSavedTabsPreservesNonEmptyWorkspaceConflict(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	canonicalRoot := t.TempDir()
	ref, workspaceID := createCurrentRegistrySession(t, app, canonicalRoot, "used-workspace-conflict", true)

	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := state.Workspaces[workspaceID]
	workspace.Root = t.TempDir()
	state.Workspaces[workspaceID] = workspace
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app.workspaceRegistry().Path(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	finishSavedTabRecovery(app)
	file := desktopTabsFile{Tabs: []desktopTabEntry{savedProjectTab("used", ref.SessionID, "", canonicalRoot, workspaceID)}, ActiveTab: "used"}

	got, changed := app.reconcileSavedTabs(t.Context(), file)
	if changed || len(got.Tabs) != 1 {
		t.Fatalf("non-empty stale-workspace session was discarded: changed=%v file=%+v", changed, got)
	}
	state, err = app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Active {
		t.Fatalf("non-empty session lifecycle = %q, want active", state.SessionStates[ref.SessionID].Lifecycle)
	}
}

func TestRestoreDropsStaleSavedTabWithoutReplacementRuntime(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	finishSavedTabRecovery(app)
	root := t.TempDir()
	file := desktopTabsFile{
		Tabs:      []desktopTabEntry{savedProjectTab("stale", "gone", "", root, desktopWorkspaceID("project", root))},
		ActiveTab: "stale", TabOrder: []string{"stale"},
	}
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(desktopConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), tabsFileName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	app.tabsRestored = make(chan struct{})

	app.restoreOrBuildTabs()
	if len(app.tabs) != 0 || len(app.runtimeByID) != 0 || len(app.runtimeBySessionKey) != 0 {
		t.Fatalf("stale restore created runtime state: tabs=%d runtimes=%d/%d", len(app.tabs), len(app.runtimeByID), len(app.runtimeBySessionKey))
	}
	persisted := loadTabsFile()
	if len(persisted.Tabs) != 0 || persisted.ActiveTab != "" {
		t.Fatalf("stale presentation remained persisted: %+v", persisted)
	}
	select {
	case <-app.tabsRestored:
	default:
		t.Fatal("restore completion gate remained open")
	}

	second := NewApp()
	second.ctx = t.Context()
	pinDesktopSessionRoot(t, second)
	finishSavedTabRecovery(second)
	second.tabsRestored = make(chan struct{})
	second.restoreOrBuildTabs()
	if len(second.tabs) != 0 || len(second.runtimeByID) != 0 || len(loadTabsFile().Tabs) != 0 {
		t.Fatal("saved-tab repair was not idempotent across restart")
	}
}

func TestDesktopTabsUnknownFieldsSurviveRoundTrip(t *testing.T) {
	var file desktopTabsFile
	raw := []byte(`{
  "tabs": [{"id":"local","scope":"global","workspaceRoot":"","topicId":"topic","futureLocal":{"enabled":true}}],
  "activeTab":"local",
  "remoteTabs":[{"id":"remote","hostId":"host","workspace":"/work","futureRemote":7}],
  "futureTop":{"version":4}
}`)
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["futureTop"]) != `{"version":4}` {
		t.Fatalf("top-level unknown field lost: %s", body)
	}
	var localFields, remoteFields map[string]json.RawMessage
	if err := json.Unmarshal(mustMarshalJSON(t, file.Tabs[0]), &localFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustMarshalJSON(t, file.RemoteTabs[0]), &remoteFields); err != nil {
		t.Fatal(err)
	}
	if string(localFields["futureLocal"]) != `{"enabled":true}` || string(remoteFields["futureRemote"]) != "7" {
		t.Fatalf("entry unknown fields lost: local=%s remote=%s", mustMarshalJSON(t, file.Tabs[0]), mustMarshalJSON(t, file.RemoteTabs[0]))
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestReconcileSavedTabsPreservesResidualCanonicalArtifacts(t *testing.T) {
	for _, fixture := range []struct {
		name       string
		legacyPath bool
	}{
		{name: "partial canonical directory"},
		{name: "legacy path on canonical tab", legacyPath: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			app := newSavedTabReconcileTestApp(t)
			finishSavedTabRecovery(app)
			entry := desktopTabEntry{ID: "saved", Scope: "global", SessionID: "missing-manifest"}
			artifact := filepath.Join(app.desktopSessions.root, entry.SessionID, "events.jsonl")
			if fixture.legacyPath {
				artifact = filepath.Join(t.TempDir(), "history.jsonl")
				entry.SessionPath = artifact
			}
			if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(artifact, []byte("{\"role\":\"user\",\"content\":\"history\"}\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			got, changed := app.reconcileSavedTabs(t.Context(), desktopTabsFile{Tabs: []desktopTabEntry{entry}})
			if fixture.legacyPath {
				if !changed || len(got.Tabs) != 0 {
					t.Fatal("unsupported path identity was restored")
				}
			} else if changed || len(got.Tabs) != 1 {
				t.Fatalf("current residual content was hidden: %+v", got)
			}
			if _, err := os.Stat(artifact); err != nil {
				t.Fatal("reconciliation removed content", err)
			}
		})
	}
}

func TestPersistReconciledTabsRejectsStaleStartupSnapshot(t *testing.T) {
	app := newSavedTabReconcileTestApp(t)
	expectedVersion := app.tabsSnapshotVersion()
	app.mu.Lock()
	app.tabs["new"] = &WorkspaceTab{ID: "new", Scope: "global", SessionID: "new-session"}
	app.tabOrder = []string{"new"}
	app.activeTabID = "new"
	dir, entries, activeID, version := app.saveTabsCollectLocked()
	app.mu.Unlock()
	app.saveTabsWrite(dir, entries, activeID, version)

	stale := desktopTabsFile{Tabs: []desktopTabEntry{{ID: "old", Scope: "global", SessionID: "old-session"}}, ActiveTab: "old"}
	if _, err := app.persistReconciledTabsFile(stale, expectedVersion); !errors.Is(err, errTabsSnapshotChanged) {
		t.Fatalf("stale startup write error = %v, want %v", err, errTabsSnapshotChanged)
	}
	if got := loadTabsFile(); got.ActiveTab != "new" {
		t.Fatalf("stale startup snapshot overwrote newer save: %+v", got)
	}
}
