package main

import (
	"context"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Repeated model changes must keep one current session and its conversation.
func TestCurrentRebuildsKeepOneSession(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, nil, nil)
	setDesktopTestCredential(t, "DEDUP_MODEL_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "p/model-a"
	cfg.Desktop.ProviderAccess = []string{"p"}
	cfg.Providers = []config.ProviderEntry{{Name: "p", Kind: "openai", Models: []string{"model-a", "model-b"}, Default: "model-a", Model: "model-a", BaseURL: "https://example.invalid/v1", APIKeyEnv: "DEDUP_MODEL_KEY"}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.AdoptRebuiltModelContext([]provider.Message{
		{ID: "system", Role: provider.RoleSystem, Content: "system"},
		{ID: "user", Role: provider.RoleUser, Content: "hello", Origin: provider.MessageOriginUser},
		{ID: "assistant", Role: provider.RoleAssistant, Content: "hi"},
	}); err != nil {
		t.Fatal(err)
	}
	before := ctrl.History()
	ref, _ := ctrl.SessionRef()
	service, runtime, _ := ctrl.SessionBinding()
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})
	for i := range 5 {
		model := "p/model-a"
		if i%2 == 0 {
			model = "p/model-b"
		}
		if err := app.SetModelForTab(tab.ID, model); err != nil {
			t.Fatal(err)
		}
		nextService, nextRuntime, bound := exclusiveSessionBinding(tab.Ctrl)
		if !bound || nextService != service || nextRuntime != runtime || tab.SessionID != ref.SessionID {
			t.Fatalf("rebuild %d changed session identity", i)
		}
		if !reflect.DeepEqual(tab.Ctrl.History(), before) {
			t.Fatalf("rebuild %d changed conversation", i)
		}
		if err := tab.Ctrl.Snapshot(); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.Query().List(t.Context(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sessions) != 1 || page.Sessions[0].Ref != ref || page.NextCursor != "" {
		t.Fatalf("rebuilds created duplicate history entries: %+v", page)
	}
}

// EnsureBlankTab reuses an already-open blank tab rather than creating a second one.

func TestEnsureBlankTabReusesExistingBlankTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	first, err := app.ensureBlankTab("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID == "" || first.SessionPath != "" {
		t.Fatalf("EnsureBlankTab identity = id %q path %q", first.SessionID, first.SessionPath)
	}
	service := app.desktopSessionService(app.activeSessionDir())
	if _, err := service.Query().Snapshot(t.Context(), session.SessionRef{HostID: service.HostID(), SessionID: first.SessionID}); err != nil {
		t.Fatalf("pre-created blank v3 session should exist: %v", err)
	}
	second, err := app.ensureBlankTab("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("EnsureBlankTab created duplicate blank tab: first=%q second=%q", first.ID, second.ID)
	}
	if tabs := app.ListTabs(); len(tabs) != 1 {
		t.Fatalf("ListTabs length = %d, want 1: %+v", len(tabs), tabs)
	}
}

func TestEnsureBlankTabReusesPrecreatedBlankBeforeControllerReady(t *testing.T) {
	isolateDesktopUserDirs(t)

	globalRoot := globalWorkspaceRoot()
	if err := os.MkdirAll(globalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	topic, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	app.tabs["blank"] = &WorkspaceTab{
		ID:            "blank",
		Scope:         "global",
		WorkspaceRoot: globalRoot,
		TopicID:       topic.ID,
		TopicTitle:    defaultTopicTitle,
		disabledMCP:   map[string]ServerView{},
	}
	app.tabOrder = []string{"blank"}
	app.activeTabID = "blank"

	meta, err := app.ensureBlankTab("global", "")
	if err != nil {
		t.Fatalf("EnsureBlankTab: %v", err)
	}
	if meta.ID != "blank" {
		t.Fatalf("EnsureBlankTab created duplicate blank tab %q, want existing pre-created blank", meta.ID)
	}
}

func TestEnsureBlankTabReusesIndexedTopicWithEmptyStub(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	topic, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	globalRoot := globalWorkspaceRoot()
	dir := desktopSessionDir(globalRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	stubPath := filepath.Join(dir, "empty-stub.jsonl")
	if err := os.WriteFile(stubPath, nil, 0o644); err != nil {
		t.Fatalf("write empty stub: %v", err)
	}
	now := time.Now()
	if err := writeHistoricalMetadata(t, stubPath, map[string]any{
		"created_at":     now.Add(-time.Minute),
		"updated_at":     now,
		"scope":          "global",
		"workspace_root": globalRoot,
		"topic_id":       topic.ID,
		"topic_title":    defaultTopicTitle,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}

	meta, err := app.ensureBlankTab("global", "")
	if err != nil {
		t.Fatalf("EnsureBlankTab: %v", err)
	}
	if meta.TopicID != topic.ID {
		t.Fatalf("EnsureBlankTab topic = %q, want reused empty topic %q", meta.TopicID, topic.ID)
	}
}

func TestEnsureBlankTabStoresCreatedAt(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	before := time.Now().UnixMilli()
	meta, err := app.ensureBlankTab("global", "")
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("EnsureBlankTab: %v", err)
	}

	_, info := currentTopicInfo(t, app, meta.TopicID)
	createdAt := info.CreatedAt.UnixMilli()
	if createdAt < before || createdAt > after {
		t.Fatalf("createdAt = %d, want between %d and %d", createdAt, before, after)
	}

	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 || nodes[0].Kind != "global_folder" || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want Global with one topic", nodes)
	}
	if got := nodes[0].Children[0].CreatedAt; got != createdAt {
		t.Fatalf("project tree createdAt = %d, want %d", got, createdAt)
	}
}

// EnsureBlankTab reuses an already-open project-scoped blank tab.

func TestEnsureBlankTabCreatesOneBlankPerProject(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	first, err := app.ensureBlankTab("project", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.ensureBlankTab("project", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("EnsureBlankTab created duplicate project blank tab: first=%q second=%q", first.ID, second.ID)
	}
	if tabs := app.ListTabs(); len(tabs) != 1 {
		t.Fatalf("ListTabs length = %d, want 1: %+v", len(tabs), tabs)
	}
}

func TestEnsureBlankTabStartsProjectRuntimeWithCurrentWorkspaceContext(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectA := robustTempDir(t)
	projectB := robustTempDir(t)
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}

	app := NewApp()
	first, err := app.ensureBlankTab("project", projectA)
	if err != nil {
		t.Fatalf("EnsureBlankTab(project A): %v", err)
	}
	tabA := waitForTabReady(t, app, first.ID)
	if got := normalizeProjectRoot(tabA.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(projectA) {
		t.Fatalf("project A controller workspace root = %q, want %q", got, normalizeProjectRoot(projectA))
	}

	second, err := app.ensureBlankTab("project", projectB)
	if err != nil {
		t.Fatalf("EnsureBlankTab(project B): %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("EnsureBlankTab reused project A tab %q for project B", second.ID)
	}
	tabB := waitForTabReady(t, app, second.ID)

	if got := normalizeProjectRoot(tabB.WorkspaceRoot); got != normalizeProjectRoot(projectB) {
		t.Fatalf("project B tab workspace root = %q, want %q", got, normalizeProjectRoot(projectB))
	}
	if got := normalizeProjectRoot(tabB.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(projectB) {
		t.Fatalf("project B controller workspace root = %q, want %q", got, normalizeProjectRoot(projectB))
	}
	if !sameDesktopPath(tabB.Ctrl.SessionDir(), desktopSessionDir(projectB)) {
		t.Fatalf("project B controller session dir = %q, want %q", tabB.Ctrl.SessionDir(), desktopSessionDir(projectB))
	}
	identity, ok := tabB.Ctrl.(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		t.Fatalf("project B controller did not use exclusive v3 identity")
	}
	if ref, bound := identity.SessionRef(); !bound || strings.TrimSpace(ref.SessionID) == "" {
		t.Fatalf("project B controller identity = %+v bound=%v legacyPath=%q", ref, bound, tabB.Ctrl.SessionRoute())
	}
	sys := tabB.Ctrl.SystemPrompt()
	if strings.Contains(sys, "Current workspace:") {
		t.Fatalf("dynamic workspace leaked into project B system prompt:\n%s", sys)
	}
	ctrl := installStubControllerWithCurrentPrompt(t, app, tabB)
	submitStubTurnAndWaitForCheckpoint(t, ctrl, "project B context turn")
	assertWorkspaceSessionContext(t, ctrl, projectB, projectA)
}

func TestColdSessionIdentityIsNotReusedAsBlank(t *testing.T) {
	app := NewApp()
	root := t.TempDir()
	tab := &WorkspaceTab{ID: "cold", Scope: "project", WorkspaceRoot: root, SessionID: "persisted-session"}
	if app.blankTabMatchesTargetLocked(tab, "project", root) {
		t.Fatal("cold durable session was treated as an empty draft")
	}
	tab.SessionID = ""
	if !app.blankTabMatchesTargetLocked(tab, "project", root) {
		t.Fatal("new draft could not be reused")
	}
	if app.blankTabMatchesTargetLocked(tab, "project", t.TempDir()) {
		t.Fatal("draft from another workspace was reused")
	}
}

func TestForkKeepsProjectWorkspaceContext(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectA := robustTempDir(t)
	projectB := robustTempDir(t)
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}

	app := NewApp()
	first, err := app.ensureBlankTab("project", projectA)
	if err != nil {
		t.Fatalf("EnsureBlankTab(project A): %v", err)
	}
	waitForTabReady(t, app, first.ID)

	second, err := app.ensureBlankTab("project", projectB)
	if err != nil {
		t.Fatalf("EnsureBlankTab(project B): %v", err)
	}
	tabB := waitForTabReady(t, app, second.ID)
	ctrl := installStubControllerWithCurrentPrompt(t, app, tabB)
	submitStubTurnAndWaitForCheckpoint(t, ctrl, "project B turn")
	targets, err := ctrl.ForkTargets()
	if err != nil || len(targets.Targets) != 1 {
		t.Fatalf("current fork targets: %+v, %v", targets, err)
	}
	turn := ctrl.Checkpoints()[0].Turn

	forked, err := app.ForkForTab(tabB.ID, turn)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if forked.ID == "" || forked.ID == second.ID {
		t.Fatalf("forked tab ID = %q, want a fresh tab distinct from %q", forked.ID, second.ID)
	}
	forkTab := waitForTabReady(t, app, forked.ID)
	if got := normalizeProjectRoot(forkTab.WorkspaceRoot); got != normalizeProjectRoot(projectB) {
		t.Fatalf("fork tab workspace root = %q, want %q", got, normalizeProjectRoot(projectB))
	}
	if got := normalizeProjectRoot(forkTab.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(projectB) {
		t.Fatalf("fork controller workspace root = %q, want %q", got, normalizeProjectRoot(projectB))
	}
	sys := forkTab.Ctrl.SystemPrompt()
	if strings.Contains(sys, "Current workspace:") {
		t.Fatalf("fork system prompt contains dynamic workspace:\n%s", sys)
	}
	forkCtrl := installStubControllerWithCurrentPrompt(t, app, forkTab)
	submitStubTurnAndWaitForCheckpoint(t, forkCtrl, "project B after fork")
	assertWorkspaceSessionContext(t, forkCtrl, projectB, projectA)
}

func TestRewindReinjectsProjectWorkspaceContext(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectA := robustTempDir(t)
	projectB := robustTempDir(t)
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}

	app := NewApp()
	first, err := app.ensureBlankTab("project", projectA)
	if err != nil {
		t.Fatalf("EnsureBlankTab(project A): %v", err)
	}
	waitForTabReady(t, app, first.ID)

	second, err := app.ensureBlankTab("project", projectB)
	if err != nil {
		t.Fatalf("EnsureBlankTab(project B): %v", err)
	}
	tabB := waitForTabReady(t, app, second.ID)
	ctrl := installStubControllerWithCurrentPrompt(t, app, tabB)
	turn := submitStubTurnAndWaitForCheckpoint(t, ctrl, "project B turn")

	if result := app.CommitRewindForTab(tabB.ID, "", turn, "conversation"); !result.OK {
		t.Fatalf("CommitRewindForTab: %+v", result)
	}
	if got := normalizeProjectRoot(tabB.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(projectB) {
		t.Fatalf("rewound controller workspace root = %q, want %q", got, normalizeProjectRoot(projectB))
	}
	sys := tabB.Ctrl.SystemPrompt()
	if strings.Contains(sys, "Current workspace:") {
		t.Fatalf("rewound system prompt contains dynamic workspace:\n%s", sys)
	}
	tabB = waitForTabReady(t, app, second.ID)
	ctrl = installStubControllerWithCurrentPrompt(t, app, tabB)
	ctrl.SubmitUserTurn("project B after rewind", "project B after rewind")
	waitNotRunning(t, ctrl)
	assertWorkspaceSessionContext(t, ctrl, projectB, projectA)
}

func submitStubTurnAndWaitForCheckpoint(t *testing.T, ctrl control.SessionAPI, input string) int {
	t.Helper()
	if err := ctrl.RunTurn(t.Context(), input); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		checkpoints := ctrl.Checkpoints()
		if len(checkpoints) > 0 {
			return checkpoints[len(checkpoints)-1].Turn
		}
		if time.Now().After(deadline) {
			t.Fatal("controller did not record a checkpoint")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEnsureBlankTabResetsReusableAutoTopicTitle(t *testing.T) {
	app, tab, _, _ := currentClearFixture(t, nil, nil)
	installNoopRuntimeEvents(app, tab.sink)
	if err := app.assignFreshSessionTopic(tab); err != nil {
		t.Fatal(err)
	}
	tab.TopicTitle, tab.topicTitleSource = "Old auto title", topicTitleSourceAuto
	meta, err := app.ensureBlankTab(tab.Scope, tab.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != tab.ID || meta.TopicTitle != defaultTopicTitle {
		t.Fatalf("auto blank reuse: %+v", meta)
	}
	_, info := currentTopicInfo(t, app, tab.TopicID)
	if info.Title != "" || info.TitleSequence != 0 {
		t.Fatalf("auto reset wrote a manual title: %+v", info)
	}
}

func TestEnsureBlankTabPreservesReusableManualTopicTitle(t *testing.T) {
	app, tab, _, _ := currentClearFixture(t, nil, nil)
	installNoopRuntimeEvents(app, tab.sink)
	if err := app.assignFreshSessionTopic(tab); err != nil {
		t.Fatal(err)
	}
	if err := app.RenameTopic(tab.TopicID, "Manual title"); err != nil {
		t.Fatal(err)
	}
	meta, err := app.ensureBlankTab(tab.Scope, tab.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != tab.ID || meta.TopicTitle != "Manual title" {
		t.Fatalf("manual blank reuse: %+v", meta)
	}
	_, info := currentTopicInfo(t, app, tab.TopicID)
	if info.Title != "Manual title" || info.TitleSequence == 0 {
		t.Fatalf("manual title lost: %+v", info)
	}
}

// EnsureBlankTab picks up an existing blank topic created in the sidebar
// instead of creating a fresh topic, for global scope.

func TestEnsureBlankTabOpensExistingSidebarBlankTopic(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	topic, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatal(err)
	}

	ref, _ := currentTopicInfo(t, app, topic.ID)

	meta, err := app.ensureBlankTab("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.TopicID != topic.ID {
		t.Fatalf("EnsureBlankTab opened topic %q, want existing blank topic %q", meta.TopicID, topic.ID)
	}
	if meta.SessionID != ref.SessionID {
		t.Fatalf("blank open changed session identity: %+v -> %+v", ref, meta)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || len(state.SessionStates) != 1 {
		t.Fatalf("duplicate blank session: %+v, %v", state.SessionStates, err)
	}
}

// EnsureBlankTab picks up an existing blank topic created in the sidebar
// instead of creating a fresh topic, for project scope.

func TestEnsureBlankTabOpensExistingProjectSidebarBlankTopic(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	topic, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatal(err)
	}

	ref, _ := currentTopicInfo(t, app, topic.ID)

	meta, err := app.ensureBlankTab("project", projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if meta.TopicID != topic.ID {
		t.Fatalf("EnsureBlankTab opened topic %q, want existing blank topic %q", meta.TopicID, topic.ID)
	}
	if meta.SessionID != ref.SessionID {
		t.Fatalf("blank open changed session identity: %+v -> %+v", ref, meta)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || len(state.SessionStates) != 1 {
		t.Fatalf("duplicate blank session: %+v, %v", state.SessionStates, err)
	}
}

func TestEnsureBlankTabDoesNotReuseProjectTopicWithSession(t *testing.T) {
	app, root, refs := canonicalOrganizationFixture(t, "existing")
	before := refs["existing"]
	installNoopRuntimeEvents(app)
	app.readyHook = func() {}
	meta, err := app.ensureBlankTab("project", root)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID == before.SessionID || meta.TopicID == "existing" {
		t.Fatalf("blank reused a formal session: %+v", meta)
	}
	if _, err := app.desktopSessionService("").Query().Stat(t.Context(), before); err != nil {
		t.Fatal("blank creation removed existing history")
	}
}

func TestIndexedBlankTopicReservesFormalAndPendingIdentities(t *testing.T) {
	t.Run("formal", func(t *testing.T) {
		app, root, refs := canonicalOrganizationFixtureWithTopic(t, "topic-alias", "existing")
		app.mu.Lock()
		got, _, err := app.indexedBlankTopicIDLocked("project", root)
		app.mu.Unlock()
		if err != nil || got != "" {
			t.Fatalf("formal empty session reused as draft: %q, %v", got, err)
		}
		if _, err := app.desktopSessionService("").Query().Stat(t.Context(), refs["existing"]); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pending", func(t *testing.T) {
		app, root, _ := canonicalOrganizationFixture(t)
		workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "project", root)
		if err != nil {
			t.Fatal(err)
		}
		const topicID = "pending-topic"
		if _, err := app.workspaceRegistry().BeginCreateAtRoot(t.Context(), workspacestate.PendingCreate{
			OperationID: "create", SessionID: "pending-session", WorkspaceID: workspaceID,
			Presentation: &workspacestate.Presentation{TopicID: topicID},
		}, root); err != nil {
			t.Fatal(err)
		}
		app.mu.Lock()
		got, _, err := app.indexedBlankTopicIDLocked("project", root)
		app.mu.Unlock()
		if err != nil || got != "" {
			t.Fatalf("pending creation reused as draft: %q, %v", got, err)
		}
	})
}

func TestEnsureBlankTabDoesNotReuseArchivedSession(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	topic, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := currentTopicInfo(t, app, topic.ID)
	if err := app.TrashTopic(topic.ID); err != nil {
		t.Fatal(err)
	}
	meta, err := app.ensureBlankTab("global", "")
	if err != nil {
		t.Fatal(err)
	}
	if meta.TopicID == topic.ID || meta.SessionID == ref.SessionID {
		t.Fatalf("blank reused archived session: %+v", meta)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil || state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Archived {
		t.Fatalf("archived session resurrected: %+v, %v", state.SessionStates[ref.SessionID], err)
	}
}

// NewSession skips the snapshot when the current tab has no real conversation content.

func TestNewSessionNoopsWhenCurrentTabIsBlank(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, nil, nil)
	before, _ := ctrl.SessionRef()
	if err := app.NewSession(); err != nil {
		t.Fatal(err)
	}
	after, ok := ctrl.SessionRef()
	if !ok || after != before || tab.Ctrl != ctrl || tab.SessionID != before.SessionID {
		t.Fatalf("blank NewSession changed current identity: before=%+v after=%+v", before, after)
	}
}

func TestNewSessionUsesFreshTopicIdentity(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	beforeID := tab.SessionID
	tab.TopicID, tab.TopicTitle = "source-topic", "Source title"
	app.ctx = context.Background()
	installNoopRuntimeEvents(app)
	app.readyHook = func() {}
	if err := app.NewSession(); err != nil {
		t.Fatal(err)
	}
	if tab.TopicID == "" || tab.TopicID == "source-topic" || tab.SessionID == "" || tab.SessionID == beforeID {
		t.Fatalf("new session retained source identity: %+v", app.tabMeta(tab, true))
	}
	if tab.TopicTitle != defaultTopicTitle {
		t.Fatalf("new title = %q", tab.TopicTitle)
	}
}
