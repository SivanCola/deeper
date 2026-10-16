package main

import (
	"context"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/control"
)

type runtimeStatusSessionController struct {
	stubSessionAPI
	status event.RuntimeStateSnapshot
}

func (c *runtimeStatusSessionController) RuntimeStateSnapshot() event.RuntimeStateSnapshot {
	return runtimeSnapshotForTest(c.status)
}

func waitForTabReady(t *testing.T, app *App, tabID string) *WorkspaceTab {
	t.Helper()
	// 15s：tab.Ready 实测随负载在 1.3s~8s+ 波动，5s 在高负载机器上是 flaky 死线。
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.RLock()
		tab := app.tabs[tabID]
		ready := tab != nil && tab.Ready
		startupErr := ""
		if tab != nil {
			startupErr = tab.StartupErr
		}
		app.mu.RUnlock()
		if tab == nil {
			t.Fatalf("tab %q was not found", tabID)
		}
		if ready {
			if startupErr != "" {
				t.Fatalf("tab %q startup error: %s", tabID, startupErr)
			}
			if tab.Ctrl != nil {
				t.Cleanup(func() { tab.Ctrl.Close() })
			}
			return tab
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tab %q was not ready before timeout", tabID)
	return nil
}

func writeTopicSession(t *testing.T, dir, name, topicID, topicTitle, workspaceRoot string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := writeHistoricalMetadata(t, path, map[string]any{
		"created_at":     time.Now().Add(-time.Minute),
		"updated_at":     time.Now(),
		"scope":          "project",
		"workspace_root": workspaceRoot,
		"topic_id":       topicID,
		"topic_title":    topicTitle,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	return path
}

func writeTopicSessionWithPrompt(t *testing.T, dir, name, topicID, topicTitle, workspaceRoot, prompt string, updatedAt time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","content":`+strconv.Quote(prompt)+`}`+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	scope := "global"
	if strings.TrimSpace(workspaceRoot) != "" {
		scope = "project"
	}
	if err := writeHistoricalMetadata(t, path, map[string]any{
		"created_at":     updatedAt.Add(-time.Minute),
		"updated_at":     updatedAt,
		"scope":          scope,
		"workspace_root": workspaceRoot,
		"topic_id":       topicID,
		"topic_title":    topicTitle,
	}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}
	return path
}

func writeLegacySession(t *testing.T, dir, name, prompt string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"role":"user","content":`+strconv.Quote(prompt)+`}`+"\n"), 0o644); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes legacy session: %v", err)
	}
	return path
}

func TestSetTopicPinnedOrdersProjectTopics(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	for _, id := range []string{"topic_a", "topic_b"} {
		ref, _ := createCurrentRegistrySession(t, app, projectRoot, id, true)
		title := map[string]string{"topic_a": "Alpha", "topic_b": "Beta"}[id]
		if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, id, title); err != nil {
			t.Fatal(err)
		}
		if err := app.desktopSessionService("").SetTitle(t.Context(), ref, title); err != nil {
			t.Fatal(err)
		}
	}
	nodes := mustListProjectTree(t, app)
	if got := []string{nodes[0].Children[0].TopicID, nodes[0].Children[1].TopicID}; got[0] != "topic_b" || got[1] != "topic_a" {
		t.Fatalf("initial topic order = %v, want [topic_b topic_a]", got)
	}

	if err := app.SetTopicPinned("topic_a", true); err != nil {
		t.Fatalf("pin topic: %v", err)
	}
	nodes = mustListProjectTree(t, app)
	if got := []string{nodes[0].Children[0].TopicID, nodes[0].Children[1].TopicID}; got[0] != "topic_a" || got[1] != "topic_b" {
		t.Fatalf("pinned topic order = %v, want [topic_a topic_b]", got)
	}
	if !nodes[0].Children[0].Pinned {
		t.Fatalf("pinned topic should expose pinned=true")
	}

	if err := app.SetTopicPinned("topic_a", false); err != nil {
		t.Fatalf("unpin topic: %v", err)
	}
	nodes = mustListProjectTree(t, app)
	if nodes[0].Children[0].Pinned || nodes[0].Children[1].Pinned {
		t.Fatalf("unpin should clear pinned flags: %#v", nodes[0].Children)
	}
}

func TestSetProjectPinnedOrdersProjectFolders(t *testing.T) {
	isolateDesktopUserDirs(t)

	first := t.TempDir()
	second := t.TempDir()
	third := t.TempDir()
	if err := addProject(first, "First"); err != nil {
		t.Fatalf("add first project: %v", err)
	}
	if err := addProject(second, "Second"); err != nil {
		t.Fatalf("add second project: %v", err)
	}
	if err := addProject(third, "Third"); err != nil {
		t.Fatalf("add third project: %v", err)
	}

	app := NewApp()
	if err := app.ReorderProjects([]string{third, first, second}); err != nil {
		t.Fatalf("ReorderProjects: %v", err)
	}
	if err := app.SetProjectPinned(second, true); err != nil {
		t.Fatalf("pin project: %v", err)
	}
	nodes := mustListProjectTree(t, app)
	if got := []string{nodes[0].Root, nodes[1].Root, nodes[2].Root}; got[0] != second || got[1] != third || got[2] != first {
		t.Fatalf("pinned project order = %v, want %v", got, []string{second, third, first})
	}
	if !nodes[0].Pinned {
		t.Fatalf("pinned project should expose pinned=true")
	}

	if err := app.SetProjectPinned(second, false); err != nil {
		t.Fatalf("unpin project: %v", err)
	}
	nodes = mustListProjectTree(t, app)
	if got := []string{nodes[0].Root, nodes[1].Root, nodes[2].Root}; got[0] != third || got[1] != first || got[2] != second {
		t.Fatalf("unpinned project order = %v, want %v", got, []string{third, first, second})
	}
	if nodes[0].Pinned || nodes[1].Pinned || nodes[2].Pinned {
		t.Fatalf("unpin should clear pinned flags: %#v", nodes)
	}
}

func TestRenameProjectUpdatesSidebarTitle(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := NewApp().RenameProject(projectRoot, "Client API"); err != nil {
		t.Fatalf("rename project: %v", err)
	}

	nodes := mustListProjectTree(t, NewApp())
	if len(nodes) != 1 {
		t.Fatalf("project tree len = %d, want 1", len(nodes))
	}
	if got := nodes[0].Label; got != "Client API" {
		t.Fatalf("project label = %q, want Client API", got)
	}

	if err := NewApp().RenameProject(projectRoot, ""); err != nil {
		t.Fatalf("clear project title: %v", err)
	}
	nodes = mustListProjectTree(t, NewApp())
	if got, want := nodes[0].Label, filepath.Base(projectRoot); got != want {
		t.Fatalf("cleared project label = %q, want %q", got, want)
	}
}

func TestListWorkspacesUsesProjectRegistryTitles(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "Client API"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	retired, err := json.Marshal([]string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(desktopConfigDir(), "desktop-workspaces.json"), retired, 0o600); err != nil {
		t.Fatal(err)
	}

	workspaces := NewApp().ListWorkspaces()
	if len(workspaces) != 1 {
		t.Fatalf("workspaces len = %d, want 1: %+v", len(workspaces), workspaces)
	}
	if got := workspaces[0].Path; got != projectRoot {
		t.Fatalf("workspace path = %q, want %q", got, projectRoot)
	}
	if got := workspaces[0].Name; got != "Client API" {
		t.Fatalf("workspace name = %q, want Client API", got)
	}
}

func TestBuildTabControllerIgnoresStaleSessionModelWhenTabModelResolves(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPZORA_TEST_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "default-provider/default-model"

[[providers]]
name = "default-provider"
kind = "openai"
base_url = "https://default.invalid/v1"
models = ["default-model"]
api_key_env = "DEEPZORA_TEST_KEY"

[[providers]]
name = "tab-provider"
kind = "openai"
base_url = "https://tab.invalid/v1"
models = ["tab-model"]
api_key_env = "DEEPZORA_TEST_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	pinned := writeLegacySession(t, dir, "stale-model.jsonl", "resume with tab model", time.Now())
	if err := writeHistoricalMetadata(t, pinned, map[string]any{"model": "missing-provider/missing-model"}); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "tab_stale_model")
	tab.model = "tab-provider/tab-model"
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.buildTabController(tab)
	if tab.Ctrl == nil {
		t.Fatalf("tab controller was not built: %s", tab.StartupErr)
	}
	defer tab.Ctrl.Close()
	if tab.model != "tab-provider/tab-model" {
		t.Fatalf("tab model = %q, want valid tab model", tab.model)
	}
}

func TestBuildTabControllerSkipsCleanupPendingPinnedSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	pending := writeLegacySession(t, dir, "pending-startup.jsonl", "pending startup", time.Now())
	if err := os.WriteFile(strings.TrimSuffix(pending, ".jsonl")+".cleanup-pending.json", []byte(`{"action":"delete"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "tab_pending")
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.buildTabController(tab)
	if tab.Ctrl == nil {
		t.Fatalf("tab controller was not built: %s", tab.StartupErr)
	}
	defer tab.Ctrl.Close()

	if got := filepath.Clean(tab.Ctrl.SessionRoute()); got == filepath.Clean(pending) {
		t.Fatalf("startup bound cleanup-pending pinned session path %q", got)
	}
	for _, msg := range tab.Ctrl.History() {
		if msg.Content == "pending startup" {
			t.Fatalf("startup loaded cleanup-pending history: %+v", tab.Ctrl.History())
		}
	}
}

func TestReorderProjectsPersistsSidebarAndWorkspaceOrder(t *testing.T) {
	isolateDesktopUserDirs(t)

	first := t.TempDir()
	second := t.TempDir()
	third := t.TempDir()
	if err := addProject(first, "First"); err != nil {
		t.Fatalf("add first project: %v", err)
	}
	if err := addProject(second, "Second"); err != nil {
		t.Fatalf("add second project: %v", err)
	}
	if err := addProject(third, "Third"); err != nil {
		t.Fatalf("add third project: %v", err)
	}

	app := NewApp()
	if err := app.ReorderProjects([]string{third, first, second}); err != nil {
		t.Fatalf("ReorderProjects: %v", err)
	}

	nodes := mustListProjectTree(t, app)
	if len(nodes) != 3 {
		t.Fatalf("project tree len = %d, want 3: %+v", len(nodes), nodes)
	}
	if got := []string{nodes[0].Root, nodes[1].Root, nodes[2].Root}; got[0] != third || got[1] != first || got[2] != second {
		t.Fatalf("project tree order = %v, want %v", got, []string{third, first, second})
	}
	workspaces := app.ListWorkspaces()
	if len(workspaces) != 3 {
		t.Fatalf("workspaces len = %d, want 3: %+v", len(workspaces), workspaces)
	}
	if got := []string{workspaces[0].Path, workspaces[1].Path, workspaces[2].Path}; got[0] != third || got[1] != first || got[2] != second {
		t.Fatalf("workspace order = %v, want %v", got, []string{third, first, second})
	}
}

func TestReorderProjectsPersistsGlobalSidebarOrder(t *testing.T) {
	isolateDesktopUserDirs(t)

	first := t.TempDir()
	second := t.TempDir()
	if err := addProject(first, "First"); err != nil {
		t.Fatalf("add first project: %v", err)
	}
	if err := addProject(second, "Second"); err != nil {
		t.Fatalf("add second project: %v", err)
	}

	app := NewApp()
	if _, err := app.CreateTopic("global", "", "Global note"); err != nil {
		t.Fatalf("create global topic: %v", err)
	}
	if err := app.ReorderProjects([]string{second, desktopGlobalOrderToken, first}); err != nil {
		t.Fatalf("ReorderProjects with global: %v", err)
	}

	nodes := mustListProjectTree(t, app)
	if len(nodes) != 3 {
		t.Fatalf("project tree len = %d, want 3: %+v", len(nodes), nodes)
	}
	if got := []string{nodes[0].Root, nodes[1].Kind, nodes[2].Root}; got[0] != second || got[1] != "global_folder" || got[2] != first {
		t.Fatalf("project tree order = %v, want [%s global_folder %s]", got, second, first)
	}
	workspaces := app.ListWorkspaces()
	if len(workspaces) != 2 {
		t.Fatalf("workspaces len = %d, want 2: %+v", len(workspaces), workspaces)
	}
	if got := []string{workspaces[0].Path, workspaces[1].Path}; got[0] != second || got[1] != first {
		t.Fatalf("workspace order = %v, want %v", got, []string{second, first})
	}
}

func TestReorderProjectsRejectsInvalidOrder(t *testing.T) {
	isolateDesktopUserDirs(t)

	first := t.TempDir()
	second := t.TempDir()
	if err := addProject(first, "First"); err != nil {
		t.Fatalf("add first project: %v", err)
	}
	if err := addProject(second, "Second"); err != nil {
		t.Fatalf("add second project: %v", err)
	}
	app := NewApp()
	for name, order := range map[string][]string{
		"missing":          {first},
		"unknown":          {first, filepath.Join(t.TempDir(), "missing")},
		"duplicate":        {first, first},
		"duplicate-global": {desktopGlobalOrderToken, first, desktopGlobalOrderToken, second},
	} {
		t.Run(name, func(t *testing.T) {
			if err := app.ReorderProjects(order); err == nil {
				t.Fatalf("ReorderProjects(%v) succeeded, want error", order)
			}
		})
	}

	nodes := mustListProjectTree(t, app)
	if got := []string{nodes[0].Root, nodes[1].Root}; got[0] != first || got[1] != second {
		t.Fatalf("project tree order changed after invalid reorder: %v", got)
	}
}

func TestRemoveWorkspaceUsesSharedProjectRegistryForCurrentProject(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, "Current Project"); err != nil {
		t.Fatalf("add project: %v", err)
	}
	app := NewApp()
	tab := app.createTabEntryWithID("project", projectRoot, "topic_current", "tab_current")
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.RemoveWorkspace(projectRoot); err != nil {
		t.Fatalf("remove current project: %v", err)
	}
	if got := app.ListWorkspaces(); len(got) != 0 {
		t.Fatalf("workspaces after remove = %+v, want empty", got)
	}
	if got := mustListProjectTree(t, app); len(got) != 1 || got[0].Kind != "global_folder" {
		t.Fatalf("project tree after remove = %+v, want only Global folder", got)
	}
}

func TestUntitledProjectTopicUsesSameFallbackEverywhere(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	t.Cleanup(func() { app.shutdown(context.Background()) })
	topic, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := app.OpenProjectTab(projectRoot, topic.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForTabReady(t, app, meta.ID)

	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1", len(tabs))
	}
	if got := tabs[0].TopicTitle; got != defaultTopicTitle {
		t.Fatalf("tab title = %q, want %q", got, defaultTopicTitle)
	}
	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one project with one topic", nodes)
	}
	if got := nodes[0].Children[0].Label; got != defaultTopicTitle {
		t.Fatalf("tree title = %q, want %q", got, defaultTopicTitle)
	}
}

func TestCreateTopicDefaultsToAutoNewSessionTitle(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	before := time.Now().UnixMilli()
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	topic, err := app.CreateTopic("project", projectRoot, "")
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	if got := topic.Title; got != defaultTopicTitle {
		t.Fatalf("topic title = %q, want %q", got, defaultTopicTitle)
	}
	_, info := currentTopicInfo(t, app, topic.ID)
	if info.Title != "" || info.TitleSequence != 0 {
		t.Fatalf("automatic title was persisted as a manual title: %+v", info)
	}
	if got := info.CreatedAt.UnixMilli(); got < before || got > after {
		t.Fatalf("createdAt = %d, want between %d and %d", got, before, after)
	}

	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one project with one topic", nodes)
	}
	if got := nodes[0].Children[0].CreatedAt; got != topic.CreatedAt {
		t.Fatalf("project tree createdAt = %d, want %d", got, topic.CreatedAt)
	}
}

func TestCreateTopicAppearsFirstInProjectTree(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	first, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create first topic: %v", err)
	}
	waitForLaterTopicTimestamp(first.CreatedAt)
	second, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create second topic: %v", err)
	}
	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 2 {
		t.Fatalf("project tree = %#v, want one project with two topics", nodes)
	}
	if got := nodes[0].Children[0].TopicID; got != second.ID {
		t.Fatalf("first visible topic = %q, want newest %q", got, second.ID)
	}
	if got := nodes[0].Children[1].TopicID; got != first.ID {
		t.Fatalf("second visible topic = %q, want older %q", got, first.ID)
	}
}

func TestCreateGlobalTopicAppearsFirstInProjectTree(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	first, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatalf("create first global topic: %v", err)
	}
	waitForLaterTopicTimestamp(first.CreatedAt)
	second, err := app.CreateTopic("global", "", "")
	if err != nil {
		t.Fatalf("create second global topic: %v", err)
	}
	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 || nodes[0].Kind != "global_folder" || len(nodes[0].Children) != 2 {
		t.Fatalf("project tree = %#v, want Global with two topics", nodes)
	}
	if got := nodes[0].Children[0].TopicID; got != second.ID {
		t.Fatalf("first visible global topic = %q, want newest %q", got, second.ID)
	}
	if got := nodes[0].Children[1].TopicID; got != first.ID {
		t.Fatalf("second visible global topic = %q, want older %q", got, first.ID)
	}
}

func TestListProjectTreeShowsEmptyGlobalWhenNoProjects(t *testing.T) {
	isolateDesktopUserDirs(t)

	nodes := mustListProjectTree(t, NewApp())
	if len(nodes) != 1 {
		t.Fatalf("project tree = %#v, want one Global folder", nodes)
	}
	if nodes[0].Kind != "global_folder" || nodes[0].Label != "Global" || len(nodes[0].Children) != 0 {
		t.Fatalf("project tree = %#v, want empty Global folder", nodes)
	}
}

func TestSwitchWorkspaceRegistersDefaultTopicInProjectTree(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	if got, err := app.SwitchWorkspace(projectRoot); err != nil {
		t.Fatalf("SwitchWorkspace: %v", err)
	} else if got != projectRoot {
		t.Fatalf("SwitchWorkspace root = %q, want %q", got, projectRoot)
	}

	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 {
		t.Fatalf("project tree len = %d, want 1: %+v", len(nodes), nodes)
	}
	if got := nodes[0].Root; got != projectRoot {
		t.Fatalf("project root = %q, want %q", got, projectRoot)
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("project children len = %d, want 1: %+v", len(nodes[0].Children), nodes[0].Children)
	}
	child := nodes[0].Children[0]
	if got := child.Label; got != defaultTopicTitle {
		t.Fatalf("default topic label = %q, want %q", got, defaultTopicTitle)
	}
	if strings.TrimSpace(child.TopicID) == "" {
		t.Fatalf("default topic ID should be persisted in the project tree: %+v", child)
	}
	tabs := app.ListTabs()
	if len(tabs) != 1 || tabs[0].TopicID != child.TopicID {
		t.Fatalf("opened tab should use the persisted topic, tabs=%+v child=%+v", tabs, child)
	}
}

func TestRenameTopicLocksTitleManual(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	topic, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	if err := app.RenameTopic(topic.ID, "手动标题"); err != nil {
		t.Fatalf("rename topic: %v", err)
	}
	_, info := currentTopicInfo(t, app, topic.ID)
	if info.Title != "手动标题" || info.TitleSequence == 0 {
		t.Fatalf("manual title not committed: %+v", info)
	}
}

func TestRenameTopicUpdatesOpenTabMeta(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	topic, err := app.CreateTopic("project", projectRoot, "旧标题")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	tab, err := app.OpenProjectTab(projectRoot, topic.ID)
	if err != nil {
		t.Fatalf("open project tab: %v", err)
	}
	waitForTabReady(t, app, tab.ID)
	if tab.TopicTitle != "旧标题" {
		t.Fatalf("opened tab title = %q, want 旧标题", tab.TopicTitle)
	}

	if err := app.RenameTopic(topic.ID, "新标题"); err != nil {
		t.Fatalf("rename topic: %v", err)
	}
	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs len = %d, want 1: %+v", len(tabs), tabs)
	}
	if got := tabs[0].TopicTitle; got != "新标题" {
		t.Fatalf("open tab title = %q, want 新标题", got)
	}
}

func TestRenameCanonicalTopicPersistsForOpenTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	topic, err := app.CreateTopic("project", projectRoot, "旧标题")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	tab, err := app.OpenProjectTab(projectRoot, topic.ID)
	if err != nil {
		t.Fatalf("open project tab: %v", err)
	}
	waitForTabReady(t, app, tab.ID)

	if err := app.RenameTopic(topic.ID, "恢复标题"); err != nil {
		t.Fatalf("rename current topic: %v", err)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, presentation := range state.Presentation {
		if presentation.TopicID == topic.ID && presentation.Title == "恢复标题" {
			found = true
		}
	}
	if !found {
		t.Fatal("canonical topic title was not durably restored")
	}
	nodes := mustListProjectTree(t, app)
	if len(nodes) != 1 || len(nodes[0].Children) != 1 || nodes[0].Children[0].TopicID != topic.ID {
		t.Fatalf("project tree should still contain topic, got %#v", nodes)
	}
	if nodes[0].Children[0].Label != "恢复标题" {
		t.Fatalf("canonical title not visible: %+v", nodes)
	}
}

func TestOpenProjectTabPreservesManualDefaultTopicTitle(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := robustTempDir(t)
	app := NewApp()
	topic, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	if err := app.RenameTopic(topic.ID, defaultTopicTitle); err != nil {
		t.Fatalf("rename topic: %v", err)
	}
	ref, _ := currentTopicInfo(t, app, topic.ID)
	binding, err := app.desktopSessionService("").Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	appendSessionTestMessage(t, binding.Runtime(), "manual-default-prompt", provider.Message{ID: "manual-default-prompt", Role: provider.RoleUser, Content: "first prompt should not replace manual default"})
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.desktopSessionService("").Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.shutdown(context.Background()) })

	meta, err := app.OpenProjectTab(projectRoot, topic.ID)
	if err != nil {
		t.Fatalf("open project tab: %v", err)
	}
	waitForTabReady(t, app, meta.ID)
	if got := meta.TopicTitle; got != defaultTopicTitle {
		t.Fatalf("opened topic title = %q, want %q", got, defaultTopicTitle)
	}
	_, info := currentTopicInfo(t, app, topic.ID)
	if info.Title != defaultTopicTitle || info.TitleSequence == 0 {
		t.Fatalf("manual default title lost: %+v", info)
	}
}

func TestRenameTopicBlankCommitsExplicitClear(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	topic, err := app.CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	if err := app.RenameTopic(topic.ID, "   "); err != nil {
		t.Fatalf("rename blank topic: %v", err)
	}
	_, info := currentTopicInfo(t, app, topic.ID)
	if info.Title != "" || info.TitleSequence == 0 {
		t.Fatalf("explicit title clear not committed: %+v", info)
	}
}

func TestTrashTopicRejectsRunningSessionRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_running_trash"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "running-trash.jsonl", topicID, "Running trash", projectRoot)
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, Label: "test", WorkspaceRoot: projectRoot})
	defer ctrl.Close()
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"running": {
				ID:            "running",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Running trash",
				Ctrl:          ctrl,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
			"keep": {
				ID:            "keep",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       "topic_keep",
				TopicTitle:    "Keep",
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"running", "keep"},
		activeTabID: "running",
	}

	ctrl.Submit("long turn")
	<-runner.started
	defer close(runner.release)
	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicHasActiveWork) {
		t.Fatalf("trash running topic error = %v, want %v", err, errTopicHasActiveWork)
	}
	if !ctrl.Running() {
		t.Fatal("rejected archive should leave the controller running")
	}
	if _, ok := app.tabs["running"]; !ok {
		t.Fatal("rejected archive should keep the running topic tab")
	}
	if got := app.activeTabID; got != "running" {
		t.Fatalf("active tab = %q, want running", got)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("rejected archive should preserve the live session: %v", err)
	}
	trashPath := filepath.Join(dir, ".trash", "running-trash.jsonl", "running-trash.jsonl")
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("rejected archive created a trash entry, stat err = %v", err)
	}
	if got := app.tabs["running"].TopicTitle; got != "Running trash" {
		t.Fatalf("rejected archive topic title = %q, want Running trash", got)
	}
}

func TestTrashTopicRejectsRunningDetachedRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_detached_running_trash"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "detached-running-trash.jsonl", topicID, "Detached running trash", projectRoot)
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, Label: "test", WorkspaceRoot: projectRoot})
	defer ctrl.Close()
	defer close(runner.release)
	detachedKey := sessionRuntimeKey(sessionPath)
	detached := &WorkspaceTab{
		ID:            detachedRuntimeTabID(detachedKey),
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       topicID,
		TopicTitle:    "Detached running trash",

		Ctrl:        ctrl,
		Ready:       true,
		disabledMCP: map[string]ServerView{},
	}
	app := &App{
		tabs:             map[string]*WorkspaceTab{},
		detachedSessions: map[string]*WorkspaceTab{detachedKey: detached},
	}

	ctrl.Submit("long detached turn")
	<-runner.started
	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicHasActiveWork) {
		t.Fatalf("trash detached running topic error = %v, want %v", err, errTopicHasActiveWork)
	}
	if !ctrl.Running() {
		t.Fatal("rejected archive should leave the detached controller running")
	}
	if got := app.detachedSessions[detachedKey]; got != detached {
		t.Fatalf("rejected archive detached runtime = %p, want %p", got, detached)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("rejected archive should preserve the detached session: %v", err)
	}
	trashPath := filepath.Join(dir, ".trash", "detached-running-trash.jsonl", "detached-running-trash.jsonl")
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("rejected archive created a trash entry, stat err = %v", err)
	}
	if got := detached.TopicTitle; got != "Detached running trash" {
		t.Fatalf("rejected archive topic title = %q, want Detached running trash", got)
	}
}

func TestTrashTopicRejectsPendingPrompt(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_pending_trash"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"pending": {
				ID:            "pending",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Pending trash",
				Ctrl:          &runtimeStatusSessionController{status: event.RuntimeStateSnapshot{PendingPrompt: true}},
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"pending"},
		activeTabID: "pending",
	}

	if err := app.TrashTopic(topicID); !errors.Is(err, errTopicHasActiveWork) {
		t.Fatalf("trash pending topic error = %v, want %v", err, errTopicHasActiveWork)
	}
	if _, ok := app.tabs["pending"]; !ok {
		t.Fatal("rejected archive should keep the pending topic tab")
	}
	if got := app.tabs["pending"].TopicTitle; got != "Pending trash" {
		t.Fatalf("rejected archive topic title = %q, want Pending trash", got)
	}
}

func TestCloseTabKeepsCurrentBlankSession(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(app.closeSessionServices)
	root := t.TempDir()
	ref, _ := createCurrentRegistrySession(t, app, root, "indexed-blank", false)
	tab := &WorkspaceTab{ID: "indexed", Scope: "project", WorkspaceRoot: root, SessionID: ref.SessionID, Ready: true, disabledMCP: map[string]ServerView{}}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	if err := app.CloseTab(tab.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.desktopSessionService("").Query().Snapshot(t.Context(), ref); err != nil {
		t.Fatalf("closing blank tab removed durable session: %v", err)
	}
	if app.tabs[tab.ID] != nil {
		t.Fatal("closed tab remains visible")
	}
}

func TestNavigationDiscardsUnboundDraftWithoutCreatingHistory(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(app.closeSessionServices)
	root := t.TempDir()
	ref, _ := createCurrentRegistrySession(t, app, root, "target", true)
	app.tabs["draft"] = &WorkspaceTab{ID: "draft", Scope: "project", WorkspaceRoot: root, Ready: true}
	app.tabs["target"] = &WorkspaceTab{ID: "target", Scope: "project", WorkspaceRoot: root, SessionID: ref.SessionID, Ready: true}
	app.tabOrder, app.activeTabID = []string{"draft", "target"}, "draft"
	if _, err := app.keepOnlyVisibleTab("target"); err != nil {
		t.Fatal(err)
	}
	if app.tabs["draft"] != nil || app.tabs["target"] == nil {
		t.Fatal("single surface navigation retained the draft or lost the target")
	}
	if _, err := app.desktopSessionService("").Query().Snapshot(t.Context(), ref); err != nil {
		t.Fatalf("navigation lost target: %v", err)
	}
}

func TestConcurrentTopicCreationKeepsAllCurrentSessions(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(app.closeSessionServices)
	root := t.TempDir()
	const n = 12
	topics := make(chan TopicMeta, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			topic, err := app.CreateTopic("project", root, fmt.Sprintf("Concurrent %02d", i))
			if err != nil {
				t.Errorf("create current topic: %v", err)
				return
			}
			topics <- topic
		})
	}
	wg.Wait()
	close(topics)
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil || len(page.Items) != n {
		t.Fatalf("current sessions: count=%d error=%v", len(page.Items), err)
	}
	seen := map[string]string{}
	for _, node := range page.Items {
		if node.Session == nil {
			t.Fatal("topic has no current identity")
		}
		seen[node.TopicID] = node.Label
	}
	for topic := range topics {
		if seen[topic.ID] != topic.Title {
			t.Fatalf("lost current topic: %+v", topic)
		}
	}
}
