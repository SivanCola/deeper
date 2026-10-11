package main

import (
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

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/provider"
)

type runtimeStatusSessionController struct {
	stubSessionAPI
	status control.RuntimeStatus
}

func (c *runtimeStatusSessionController) RuntimeStatus() control.RuntimeStatus {
	return c.status
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
	if err := agent.SaveBranchMeta(path, agent.BranchMeta{
		CreatedAt:     time.Now().Add(-time.Minute),
		UpdatedAt:     time.Now(),
		Scope:         "project",
		WorkspaceRoot: workspaceRoot,
		TopicID:       topicID,
		TopicTitle:    topicTitle,
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
	if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
		CreatedAt:     updatedAt.Add(-time.Minute),
		UpdatedAt:     updatedAt,
		Scope:         scope,
		WorkspaceRoot: workspaceRoot,
		TopicID:       topicID,
		TopicTitle:    topicTitle,
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

func TestTopicMetadataUpdatesPreserveExistingEntriesWhenTimedReadSlotsFull(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := saveTopicTitles(projectRoot, map[string]string{"old": "Old"}); err != nil {
		t.Fatalf("save old title: %v", err)
	}
	if err := saveTopicTitleSources(projectRoot, map[string]string{"old": topicTitleSourceManual}); err != nil {
		t.Fatalf("save old source: %v", err)
	}
	if err := saveTopicCreatedAts(projectRoot, map[string]int64{"old": 100}); err != nil {
		t.Fatalf("save old created-at: %v", err)
	}

	release := occupyReadFileWithTimeoutSlots(t)
	if err := setTopicTitleWithSource(projectRoot, "new", "New", topicTitleSourceAuto); err != nil {
		t.Fatalf("setTopicTitleWithSource: %v", err)
	}
	if err := setTopicCreatedAt(projectRoot, "new", 200); err != nil {
		t.Fatalf("setTopicCreatedAt: %v", err)
	}
	release()

	titles := loadTopicTitles(projectRoot)
	if got := titles["old"]; got != "Old" {
		t.Fatalf("old title = %q, want Old (all titles: %v)", got, titles)
	}
	if got := titles["new"]; got != "New" {
		t.Fatalf("new title = %q, want New (all titles: %v)", got, titles)
	}
	sources := loadTopicTitleSources(projectRoot)
	if got := sources["old"]; got != topicTitleSourceManual {
		t.Fatalf("old source = %q, want %q (all sources: %v)", got, topicTitleSourceManual, sources)
	}
	if got := sources["new"]; got != topicTitleSourceAuto {
		t.Fatalf("new source = %q, want %q (all sources: %v)", got, topicTitleSourceAuto, sources)
	}
	created := loadTopicCreatedAts(projectRoot)
	if got := created["old"]; got != 100 {
		t.Fatalf("old created-at = %d, want 100 (all created: %v)", got, created)
	}
	if got := created["new"]; got != 200 {
		t.Fatalf("new created-at = %d, want 200 (all created: %v)", got, created)
	}
}

func TestDeleteTopicKeepsSessionHistory(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_keep_history"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Keep history"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "keep.jsonl", topicID, "Keep history", projectRoot)

	if err := NewApp().DeleteTopic(topicID); err != nil {
		t.Fatalf("delete topic: %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("delete topic should keep session history: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("topic title should be removed, got %q", got)
	}
}

func TestSetTopicPinnedOrdersProjectTopics(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, "topic_a", "Alpha"); err != nil {
		t.Fatalf("set topic a title: %v", err)
	}
	if err := setTopicTitle(projectRoot, "topic_b", "Beta"); err != nil {
		t.Fatalf("set topic b title: %v", err)
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

func TestDeleteTopicClearsPinnedTopic(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, "topic_pinned_delete", "Pinned"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	app := NewApp()
	if err := app.SetTopicPinned("topic_pinned_delete", true); err != nil {
		t.Fatalf("pin topic: %v", err)
	}
	if err := app.DeleteTopic("topic_pinned_delete"); err != nil {
		t.Fatalf("delete topic: %v", err)
	}
	projects := loadProjectsFile().Projects
	if len(projects) != 1 {
		t.Fatalf("projects len = %d, want 1", len(projects))
	}
	if got := projects[0].PinnedTopics; len(got) != 0 {
		t.Fatalf("pinned topics after delete = %v, want empty", got)
	}
}

func assertTopicFullyDeleted(t *testing.T, projectRoot, topicID string) {
	t.Helper()
	if got := loadTopicTitle(projectRoot, topicID); got != "" {
		t.Fatalf("topic title = %q, want deleted", got)
	}
	if got := loadTopicTitleSources(projectRoot); got[topicID] != "" {
		t.Fatalf("title source = %q, want deleted (all sources: %v)", got[topicID], got)
	}
	if got := loadTopicCreatedAts(projectRoot); got[topicID] != 0 {
		t.Fatalf("created-at = %d, want deleted (all created: %v)", got[topicID], got)
	}
	if got := loadTopicAutoTitleMeta(projectRoot); got != nil {
		if meta, ok := got[topicID]; ok {
			t.Fatalf("auto-title meta = %+v, want deleted (all auto-title meta: %v)", meta, got)
		}
	}
	f := loadProjectsFile()
	i := projectIndexByRoot(f.Projects, projectRoot)
	if i < 0 {
		t.Fatalf("projects = %#v, want entry for %q", f.Projects, projectRoot)
	}
	if containsDesktopString(f.Projects[i].Topics, topicID) {
		t.Fatalf("project topics = %#v, %q should be removed", f.Projects[i].Topics, topicID)
	}
	if !containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("deletedTopics = %#v, want tombstone for %q", f.DeletedTopics, topicID)
	}
}

func TestDeleteTopicWithoutTitleEntryStillRemovesIndexAndTombstones(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_leftover_delete"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Doomed"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	if err := setTopicCreatedAt(projectRoot, topicID, 4242); err != nil {
		t.Fatalf("set created-at: %v", err)
	}
	if err := prependTopicInProjectsFile(projectRoot, topicID, false); err != nil {
		t.Fatalf("index topic: %v", err)
	}
	// Strip just the title entry to mimic an interrupted earlier deletion:
	// sources, created-at, and the sidebar index survived it.
	titles, err := loadTopicTitlesForUpdate(projectRoot)
	if err != nil {
		t.Fatalf("load titles: %v", err)
	}
	delete(titles, topicID)
	if err := saveTopicTitles(projectRoot, titles); err != nil {
		t.Fatalf("save titles: %v", err)
	}

	if err := NewApp().DeleteTopic(topicID); err != nil {
		t.Fatalf("delete topic: %v", err)
	}
	assertTopicFullyDeleted(t, projectRoot, topicID)
}

func TestDeleteTopicIgnoresUnrelatedProjectMetadataDamage(t *testing.T) {
	isolateDesktopUserDirs(t)

	// The broken project is added first so the cleanup sweep meets it before
	// reaching the target root.
	brokenRoot := t.TempDir()
	targetRoot := t.TempDir()
	seedLegacyTopicBridge(t, brokenRoot)
	topicID := "topic_target_delete"
	if err := addProject(brokenRoot, ""); err != nil {
		t.Fatalf("add broken project: %v", err)
	}
	if err := addProject(targetRoot, ""); err != nil {
		t.Fatalf("add target project: %v", err)
	}
	if err := setTopicTitle(brokenRoot, "topic_unrelated", "Unrelated"); err != nil {
		t.Fatalf("set unrelated topic title: %v", err)
	}
	if err := setTopicTitle(targetRoot, topicID, "Doomed"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	if err := setTopicCreatedAt(targetRoot, topicID, 4242); err != nil {
		t.Fatalf("set created-at: %v", err)
	}
	if err := prependTopicInProjectsFile(targetRoot, topicID, false); err != nil {
		t.Fatalf("index topic: %v", err)
	}

	// Unrelated unreadable legacy metadata must not abort target deletion.
	for _, path := range []string{topicTitlesPath(brokenRoot), topicTitleSourcesPath(brokenRoot)} {
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("block %s: %v", path, err)
		}
	}

	if err := NewApp().DeleteTopic(topicID); err != nil {
		t.Fatalf("delete topic with unrelated broken project: %v", err)
	}
	assertTopicFullyDeleted(t, targetRoot, topicID)

	f := loadProjectsFile()
	if i := projectIndexByRoot(f.Projects, brokenRoot); i < 0 {
		t.Fatalf("projects = %#v, want entry for broken root", f.Projects)
	}
	if containsDesktopString(f.DeletedTopics, "topic_unrelated") {
		t.Fatalf("deletedTopics = %#v, unrelated topic must not be tombstoned", f.DeletedTopics)
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

func TestBatchPrependRespectsTombstoneWrittenAfterScanSnapshot(t *testing.T) {
	isolateDesktopUserDirs(t)

	topicID := "legacy_race_000000000001"
	// Simulate a DeleteTopic landing between a repair scan's DeletedTopics
	// snapshot and its batch write: the tombstone exists by the time the
	// prepend runs, so the batch must drop the topic instead of resurrecting it.
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.DeletedTopics = prependUniqueString(f.DeletedTopics, topicID)
		return true, nil
	}); err != nil {
		t.Fatalf("seed tombstone: %v", err)
	}
	if err := prependTopicsInProjectsFile("", []string{topicID, "legacy_live_000000000002"}, false); err != nil {
		t.Fatalf("batch prepend: %v", err)
	}
	f := loadProjectsFile()
	if containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("globalTopics = %#v, tombstoned topic %q must not be batch-prepended", f.GlobalTopics, topicID)
	}
	if !containsDesktopString(f.GlobalTopics, "legacy_live_000000000002") {
		t.Fatalf("globalTopics = %#v, live topic should still be prepended", f.GlobalTopics)
	}
	if !containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("deletedTopics = %#v, tombstone should survive a batch prepend", f.DeletedTopics)
	}

	// Intentional single-topic writes (create/restore/tab indexing) clear the
	// tombstone and bring the topic back in the same projects-file transaction.
	if err := prependTopicInProjectsFile("", topicID, false); err != nil {
		t.Fatalf("single prepend: %v", err)
	}
	f = loadProjectsFile()
	if !containsDesktopString(f.GlobalTopics, topicID) {
		t.Fatalf("globalTopics = %#v, single prepend should restore %q", f.GlobalTopics, topicID)
	}
	if containsDesktopString(f.DeletedTopics, topicID) {
		t.Fatalf("deletedTopics = %#v, single prepend should clear the tombstone", f.DeletedTopics)
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
	meta, err := agent.EnsureBranchMeta(pinned)
	if err != nil {
		t.Fatal(err)
	}
	meta.Model = "missing-provider/missing-model"
	if err := agent.SaveBranchMetaPreserveUpdated(pinned, meta); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "tab_stale_model")
	tab.SessionPath = pinned
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

func TestPinnedTabSessionPathRejectsExistingAbsolutePathOutsideDir(t *testing.T) {
	isolateDesktopUserDirs(t)

	dirA := t.TempDir()
	dirB := t.TempDir()
	pathA := writeLegacySession(t, dirA, "same-name.jsonl", "project A", time.Now())
	_ = writeLegacySession(t, dirB, filepath.Base(pathA), "project B", time.Now())

	if got, ok := pinnedTabSessionPath(dirB, pathA); ok {
		t.Fatalf("pinnedTabSessionPath mapped existing absolute path outside dir to %q", got)
	}
}

func TestLoadPinnedTabSessionSkipsCleanupPending(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeLegacySession(t, dir, "pending-pinned.jsonl", "pending pinned", time.Now())
	if err := agent.MarkCleanupPending(path, "delete"); err != nil {
		t.Fatal(err)
	}

	if loaded, pinnedPath, ok, err := loadPinnedTabSession(dir, path); err != nil || ok || loaded != nil || pinnedPath != "" {
		t.Fatalf("loadPinnedTabSession cleanup-pending = loaded:%v path:%q ok:%v, want skipped", loaded, pinnedPath, ok)
	}
}

func TestLoadPinnedTabSessionPreservesLoadError(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := writeLegacySession(t, dir, "unsafe-pinned.jsonl", "checkpoint", time.Now())
	events := `{"schema_version":99,"type":"replace","messages":[{"role":"user","content":"newer"}]}` + "\n"
	if err := os.WriteFile(agent.SessionEventLogPath(path), []byte(events), 0o600); err != nil {
		t.Fatalf("write future event log: %v", err)
	}

	loaded, pinnedPath, ok, err := loadPinnedTabSession(dir, path)
	if err == nil || !strings.Contains(err.Error(), "uses schema 99") {
		t.Fatalf("loadPinnedTabSession error = %v, want future-schema refusal", err)
	}
	if loaded != nil || !ok || filepath.Clean(pinnedPath) != filepath.Clean(path) {
		t.Fatalf("load result = loaded:%v path:%q ok:%v, want pinned path retained with hard error", loaded, pinnedPath, ok)
	}
	if got, readErr := os.ReadFile(agent.SessionEventLogPath(path)); readErr != nil || string(got) != events {
		t.Fatalf("event log changed after refusal: bytes=%q err=%v", got, readErr)
	}
}

func TestBuildTabControllerSkipsCleanupPendingPinnedSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	pending := writeLegacySession(t, dir, "pending-startup.jsonl", "pending startup", time.Now())
	if err := agent.MarkCleanupPending(pending, "delete"); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "tab_pending")
	tab.SessionPath = pending
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	app.buildTabController(tab)
	if tab.Ctrl == nil {
		t.Fatalf("tab controller was not built: %s", tab.StartupErr)
	}
	defer tab.Ctrl.Close()

	if got := filepath.Clean(tab.Ctrl.SessionPath()); got == filepath.Clean(pending) {
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
	topicID := "topic_without_title"
	if err := saveProjectsFile(desktopProjectFile{Projects: []desktopProject{{
		Root:   projectRoot,
		Topics: []string{topicID},
	}}}); err != nil {
		t.Fatalf("save projects: %v", err)
	}

	app := NewApp()
	tab := app.createTabEntryWithID("project", projectRoot, topicID, "tab1")
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

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
	topic, err := NewApp().CreateTopic("project", projectRoot, "")
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	if got := topic.Title; got != defaultTopicTitle {
		t.Fatalf("topic title = %q, want %q", got, defaultTopicTitle)
	}
	if got := loadTopicTitle(projectRoot, topic.ID); got != defaultTopicTitle {
		t.Fatalf("stored title = %q, want %q", got, defaultTopicTitle)
	}
	if got := loadTopicTitleSource(projectRoot, topic.ID); got != topicTitleSourceAuto {
		t.Fatalf("title source = %q, want auto", got)
	}
	if got := loadTopicCreatedAt(projectRoot, topic.ID); got < before || got > after {
		t.Fatalf("createdAt = %d, want between %d and %d", got, before, after)
	}
	nodes := mustListProjectTree(t, NewApp())
	if len(nodes) != 1 || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want one project with one topic", nodes)
	}
	if got := nodes[0].Children[0].CreatedAt; got != topic.CreatedAt {
		t.Fatalf("project tree createdAt = %d, want %d", got, topic.CreatedAt)
	}
}

func TestListProjectTreeFallsBackToTopicIDCreatedAt(t *testing.T) {
	isolateDesktopUserDirs(t)

	const topicID = "legacy_20260606-114914_2276f13fd87c"
	if err := setTopicTitleWithSource("", topicID, "你好，你是谁", topicTitleSourceManual); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	if err := prependTopicInProjectsFile("", topicID, false); err != nil {
		t.Fatalf("prepend topic: %v", err)
	}

	nodes := mustListProjectTree(t, NewApp())
	if len(nodes) != 1 || nodes[0].Kind != "global_folder" || len(nodes[0].Children) != 1 {
		t.Fatalf("project tree = %#v, want Global with one topic", nodes)
	}
	expected := time.Date(2026, 6, 6, 11, 49, 14, 0, time.UTC).UnixMilli()
	if got := nodes[0].Children[0].CreatedAt; got != expected {
		t.Fatalf("project tree createdAt = %d, want %d", got, expected)
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
	if got := loadTopicTitle(projectRoot, topic.ID); got != "手动标题" {
		t.Fatalf("stored title = %q, want 手动标题", got)
	}
	if got := loadTopicTitleSource(projectRoot, topic.ID); got != topicTitleSourceManual {
		t.Fatalf("title source = %q, want manual", got)
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

func TestRenameCanonicalTopicSurvivesDeletedLegacyTitleIndex(t *testing.T) {
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
	if err := saveTopicTitles(projectRoot, map[string]string{}); err != nil {
		t.Fatalf("clear topic titles: %v", err)
	}
	if err := saveTopicTitleSources(projectRoot, map[string]string{}); err != nil {
		t.Fatalf("clear topic title sources: %v", err)
	}

	if err := app.RenameTopic(topic.ID, "恢复标题"); err != nil {
		t.Fatalf("rename topic after deleting title index: %v", err)
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

func TestRenameTopicRecreatesDeletedProjectTitleIndexFromSessionMeta(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_missing_index"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "旧标题"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	writeTopicSession(t, dir, "missing-index.jsonl", topicID, "旧标题", projectRoot)
	if err := saveTopicTitles(projectRoot, map[string]string{}); err != nil {
		t.Fatalf("clear topic titles: %v", err)
	}
	if err := saveTopicTitleSources(projectRoot, map[string]string{}); err != nil {
		t.Fatalf("clear topic title sources: %v", err)
	}

	if err := NewApp().RenameTopic(topicID, "恢复标题"); err != nil {
		t.Fatalf("rename topic from session meta after deleting title index: %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "恢复标题" {
		t.Fatalf("restored topic title = %q, want 恢复标题", got)
	}
	nodes := mustListProjectTree(t, NewApp())
	if len(nodes) != 1 || len(nodes[0].Children) != 1 || nodes[0].Children[0].TopicID != topicID {
		t.Fatalf("project tree should contain restored topic, got %#v", nodes)
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
	dir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	writeTopicSessionWithPrompt(t, dir, "manual-default.jsonl", topic.ID, defaultTopicTitle, projectRoot, "first prompt should not replace manual default", time.Now())

	meta, err := app.OpenProjectTab(projectRoot, topic.ID)
	if err != nil {
		t.Fatalf("open project tab: %v", err)
	}
	waitForTabReady(t, app, meta.ID)
	if got := meta.TopicTitle; got != defaultTopicTitle {
		t.Fatalf("opened topic title = %q, want %q", got, defaultTopicTitle)
	}
	if got := loadTopicTitle(projectRoot, topic.ID); got != defaultTopicTitle {
		t.Fatalf("stored topic title = %q, want %q", got, defaultTopicTitle)
	}
	if got := loadTopicTitleSource(projectRoot, topic.ID); got != topicTitleSourceManual {
		t.Fatalf("title source = %q, want manual", got)
	}
}

func TestEnsureTopicIndexedPreservesGlobalAutoTitleSource(t *testing.T) {
	isolateDesktopUserDirs(t)

	topicID := "topic_global_auto"
	if err := setTopicTitleWithSource("", topicID, defaultTopicTitle, topicTitleSourceAuto); err != nil {
		t.Fatalf("set global topic title: %v", err)
	}
	source := loadTopicTitleSource(topicTitleRoot("global", globalTabWorkspaceRoot()), topicID)
	if err := ensureTopicIndexed("global", globalTabWorkspaceRoot(), topicID, defaultTopicTitle, source); err != nil {
		t.Fatalf("ensure global topic indexed: %v", err)
	}

	if got := loadTopicTitleSource("", topicID); got != topicTitleSourceAuto {
		t.Fatalf("global title source = %q, want %q", got, topicTitleSourceAuto)
	}
}

func TestAutoTitleTopicFromFirstUserMessage(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topic, err := NewApp().CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := saveAutoTitleCurrentSession(sessionPath, []provider.Message{{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "讲讲这个代码库的架构"}}); err != nil {
		t.Fatalf("write session: %v", err)
	}

	title, updated := autoTitleTopicFromSession(projectRoot, topic.ID, sessionPath)
	if !updated {
		t.Fatal("auto title should update")
	}
	if title != "讲讲这个代码库的架构" {
		t.Fatalf("generated title = %q", title)
	}
	if got := loadTopicTitle(projectRoot, topic.ID); got != title {
		t.Fatalf("stored title = %q, want %q", got, title)
	}
	if got := loadTopicTitleSource(projectRoot, topic.ID); got != topicTitleSourceAuto {
		t.Fatalf("title source = %q, want auto", got)
	}
}

func TestAutoTitleTopicStripsReasoningLanguagePrefix(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topic, err := NewApp().CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	prompt := control.New(control.Options{ReasoningLanguage: "zh"}).Compose("讲讲这个代码库的架构")
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := saveAutoTitleCurrentSession(sessionPath, []provider.Message{{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: prompt, RawContent: "讲讲这个代码库的架构"}}); err != nil {
		t.Fatalf("write session: %v", err)
	}

	title, updated := autoTitleTopicFromSession(projectRoot, topic.ID, sessionPath)
	if !updated {
		t.Fatal("auto title should update")
	}
	if title != "讲讲这个代码库的架构" {
		t.Fatalf("generated title = %q", title)
	}
}

func TestAutoTitleTopicRefreshesOnThirdUserTurn(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topic, err := NewApp().CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	firstTurn := strings.Join([]string{
		`{"role":"user","content":"帮我看看"}`,
		`{"role":"assistant","content":"可以"}`,
	}, "\n") + "\n"
	if err := saveAutoTitleCurrentJSON(sessionPath, firstTurn); err != nil {
		t.Fatalf("write first session: %v", err)
	}

	title, updated := autoTitleTopicFromSession(projectRoot, topic.ID, sessionPath)
	if !updated || title != "帮我看看" {
		t.Fatalf("first auto title = %q updated=%v, want 帮我看看/true", title, updated)
	}

	thirdTurn := strings.Join([]string{
		`{"role":"user","content":"帮我看看"}`,
		`{"role":"assistant","content":"可以"}`,
		`{"role":"user","content":"继续"}`,
		`{"role":"assistant","content":"继续分析"}`,
		`{"role":"user","content":"实现自动更新会话标题"}`,
		`{"role":"assistant","content":"已实现"}`,
	}, "\n") + "\n"
	if err := saveAutoTitleCurrentJSON(sessionPath, thirdTurn); err != nil {
		t.Fatalf("write third session: %v", err)
	}

	title, updated = autoTitleTopicFromSession(projectRoot, topic.ID, sessionPath)
	if !updated || title != "实现自动更新会话标题" {
		t.Fatalf("third-turn auto title = %q updated=%v, want 实现自动更新会话标题/true", title, updated)
	}
	if got := loadTopicTitle(projectRoot, topic.ID); got != "实现自动更新会话标题" {
		t.Fatalf("stored title = %q, want 实现自动更新会话标题", got)
	}
	meta := loadTopicAutoTitleMeta(projectRoot)[topic.ID]
	if meta.Stage != 3 {
		t.Fatalf("auto title stage = %d, want 3", meta.Stage)
	}
}

func TestAutoTitleDoesNotOverrideManualTopicTitle(t *testing.T) {
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
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := saveAutoTitleCurrentSession(sessionPath, []provider.Message{{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "讲讲这个代码库的架构"}}); err != nil {
		t.Fatalf("write session: %v", err)
	}

	if title, updated := autoTitleTopicFromSession(projectRoot, topic.ID, sessionPath); updated || title != "" {
		t.Fatalf("manual title should not auto-update, title=%q updated=%v", title, updated)
	}
	if got := loadTopicTitle(projectRoot, topic.ID); got != "手动标题" {
		t.Fatalf("stored title = %q, want 手动标题", got)
	}
}

func TestAutoTitleDoesNotOverrideManualSessionTitle(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topic, err := NewApp().CreateTopic("project", projectRoot, "")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := saveAutoTitleCurrentSession(sessionPath, []provider.Message{{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "讲讲这个代码库的架构"}}); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(sessionPath, agent.BranchMeta{CustomTitle: "手动会话标题"}); err != nil {
		t.Fatalf("save branch meta: %v", err)
	}

	if title, updated := autoTitleTopicFromSession(projectRoot, topic.ID, sessionPath); updated || title != "" {
		t.Fatalf("manual session title should not auto-update, title=%q updated=%v", title, updated)
	}
	if got := loadTopicTitle(projectRoot, topic.ID); got != defaultTopicTitle {
		t.Fatalf("stored title = %q, want default title", got)
	}
}

func TestRenameTopicBlankKeepsManualTitleSource(t *testing.T) {
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
	if got := loadTopicTitle(projectRoot, topic.ID); got != defaultTopicTitle {
		t.Fatalf("stored title = %q, want %q", got, defaultTopicTitle)
	}
	if got := loadTopicTitleSource(projectRoot, topic.ID); got != topicTitleSourceManual {
		t.Fatalf("title source = %q, want manual", got)
	}
}

func TestTrashTopicRejectsRunningSessionRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_running_trash"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, "Running trash"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "running-trash.jsonl", topicID, "Running trash", projectRoot)
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: sessionPath, Label: "test", WorkspaceRoot: projectRoot})
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
	trashPath := filepath.Join(dir, sessionTrashDir, "running-trash.jsonl", "running-trash.jsonl")
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("rejected archive created a trash entry, stat err = %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "Running trash" {
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
	if err := setTopicTitle(projectRoot, topicID, "Detached running trash"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := writeTopicSession(t, dir, "detached-running-trash.jsonl", topicID, "Detached running trash", projectRoot)
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := control.New(control.Options{Runner: runner, SessionDir: dir, SessionPath: sessionPath, Label: "test", WorkspaceRoot: projectRoot})
	defer ctrl.Close()
	defer close(runner.release)
	detachedKey := sessionRuntimeKey(sessionPath)
	detached := &WorkspaceTab{
		ID:            detachedRuntimeTabID(detachedKey),
		Scope:         "project",
		WorkspaceRoot: projectRoot,
		TopicID:       topicID,
		TopicTitle:    "Detached running trash",
		SessionPath:   sessionPath,
		Ctrl:          ctrl,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
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
	trashPath := filepath.Join(dir, sessionTrashDir, "detached-running-trash.jsonl", "detached-running-trash.jsonl")
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("rejected archive created a trash entry, stat err = %v", err)
	}
	if got := loadTopicTitle(projectRoot, topicID); got != "Detached running trash" {
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
	if err := setTopicTitle(projectRoot, topicID, "Pending trash"); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"pending": {
				ID:            "pending",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    "Pending trash",
				Ctrl:          &runtimeStatusSessionController{status: control.RuntimeStatus{PendingPrompt: true}},
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
	if got := loadTopicTitle(projectRoot, topicID); got != "Pending trash" {
		t.Fatalf("rejected archive topic title = %q, want Pending trash", got)
	}
}

func TestTransientFallbackDiscardedWhenSingleSurfaceNavigatesAway(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	dir := desktopSessionDir(projectRoot)
	transientPath, err := createEmptySessionFile(dir, "test")
	if err != nil {
		t.Fatalf("create transient session: %v", err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(transientPath, agent.BranchMeta{
		Scope:         "project",
		WorkspaceRoot: projectRoot,
	}); err != nil {
		t.Fatalf("save transient meta: %v", err)
	}

	targetTopicID := "topic_target"
	if err := setTopicTitle(projectRoot, targetTopicID, "Target"); err != nil {
		t.Fatalf("set target topic title: %v", err)
	}
	targetPath := writeTopicSession(t, dir, "target.jsonl", targetTopicID, "Target", projectRoot)
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"transient": {
				ID:            "transient",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicTitle:    defaultTopicTitle,
				SessionPath:   transientPath,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
			"target": {
				ID:            "target",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       targetTopicID,
				TopicTitle:    "Target",
				SessionPath:   targetPath,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
		},
		tabOrder:    []string{"transient", "target"},
		activeTabID: "transient",
	}

	if _, err := app.keepOnlyVisibleTab("target"); err != nil {
		t.Fatalf("keepOnlyVisibleTab: %v", err)
	}
	if _, ok := app.tabs["transient"]; ok {
		t.Fatal("transient tab should be removed after single-surface navigation")
	}
	if _, err := os.Stat(transientPath); !os.IsNotExist(err) {
		t.Fatalf("transient session artifact should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(agent.BranchMetaPath(transientPath)); !os.IsNotExist(err) {
		t.Fatalf("transient session meta should be removed, stat err = %v", err)
	}
	f := loadProjectsFile()
	if len(f.Projects) != 1 || containsDesktopString(f.Projects[0].Topics, "") {
		t.Fatalf("transient blank should not be indexed in project topics: %#v", f.Projects)
	}
}

func TestCloseTabDiscardsUnusedTransientBlankSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	dir := desktopSessionDir(projectRoot)
	transientPath, err := createEmptySessionFile(dir, "test")
	if err != nil {
		t.Fatalf("create transient session: %v", err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(transientPath, agent.BranchMeta{
		Scope:         "project",
		WorkspaceRoot: projectRoot,
	}); err != nil {
		t.Fatalf("save transient meta: %v", err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"transient": {
				ID:            "transient",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicTitle:    defaultTopicTitle,
				SessionPath:   transientPath,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
			"other": {
				ID:          "other",
				Scope:       "global",
				TopicID:     "topic_other",
				TopicTitle:  "Other",
				Ready:       true,
				disabledMCP: map[string]ServerView{},
			},
		},
		tabOrder:    []string{"transient", "other"},
		activeTabID: "transient",
	}

	if err := app.CloseTab("transient"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}
	if _, ok := app.tabs["transient"]; ok {
		t.Fatal("transient tab should be closed")
	}
	if _, err := os.Stat(transientPath); !os.IsNotExist(err) {
		t.Fatalf("transient session artifact should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(agent.BranchMetaPath(transientPath)); !os.IsNotExist(err) {
		t.Fatalf("transient session meta should be removed, stat err = %v", err)
	}
}

func TestCloseTabKeepsIndexedBlankSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	topicID := "topic_indexed_blank"
	if err := addProject(projectRoot, ""); err != nil {
		t.Fatalf("add project: %v", err)
	}
	if err := setTopicTitle(projectRoot, topicID, defaultTopicTitle); err != nil {
		t.Fatalf("set topic title: %v", err)
	}
	dir := desktopSessionDir(projectRoot)
	indexedPath, err := createEmptySessionFile(dir, "test")
	if err != nil {
		t.Fatalf("create indexed blank session: %v", err)
	}
	app := &App{
		tabs: map[string]*WorkspaceTab{
			"indexed": {
				ID:            "indexed",
				Scope:         "project",
				WorkspaceRoot: projectRoot,
				TopicID:       topicID,
				TopicTitle:    defaultTopicTitle,
				SessionPath:   indexedPath,
				Ready:         true,
				disabledMCP:   map[string]ServerView{},
			},
			"other": {
				ID:          "other",
				Scope:       "global",
				TopicID:     "topic_other",
				TopicTitle:  "Other",
				Ready:       true,
				disabledMCP: map[string]ServerView{},
			},
		},
		tabOrder:    []string{"indexed", "other"},
		activeTabID: "indexed",
	}

	if err := app.CloseTab("indexed"); err != nil {
		t.Fatalf("CloseTab: %v", err)
	}
	if _, err := os.Stat(indexedPath); err != nil {
		t.Fatalf("indexed blank session should be preserved, stat err = %v", err)
	}
}

func TestFindTopicSessionIndexRefreshesWhenMetaChanges(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	topicID := "topic_cache_refresh"
	now := time.Now().UTC()
	first := writeTopicSessionWithPrompt(t, dir, "first.jsonl", topicID, "First", "", "first prompt", now.Add(-time.Hour))

	if got := findTopicSession(dir, topicID); got != first {
		t.Fatalf("first lookup = %q, want %q", got, first)
	}

	second := writeTopicSessionWithPrompt(t, dir, "second.jsonl", topicID, "Second", "", "second prompt", now)
	if got := findTopicSession(dir, topicID); got != second {
		t.Fatalf("lookup after new session = %q, want newer %q", got, second)
	}

	meta, ok, err := agent.LoadBranchMeta(second)
	if err != nil || !ok {
		t.Fatalf("load second meta: ok=%v err=%v", ok, err)
	}
	meta.TopicID = "topic_cache_other"
	meta.UpdatedAt = now.Add(time.Hour)
	if err := agent.SaveBranchMetaPreserveUpdated(second, meta); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(agent.BranchMetaPath(second), future, future); err != nil {
		t.Fatal(err)
	}

	if got := findTopicSession(dir, topicID); got != first {
		t.Fatalf("lookup after retopic = %q, want remaining %q", got, first)
	}
	if got := findTopicSession(dir, "topic_cache_other"); got != second {
		t.Fatalf("lookup for retopic session = %q, want %q", got, second)
	}
}

func TestFindTopicSessionSkipsCleanupPending(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	topicID := "topic_skip_pending"
	now := time.Now().UTC()
	normal := writeTopicSessionWithPrompt(t, dir, "normal.jsonl", topicID, "Normal", "", "normal prompt", now)
	pending := writeTopicSessionWithPrompt(t, dir, "pending.jsonl", topicID, "Pending", "", "pending prompt", now.Add(time.Hour))

	if got := findTopicSession(dir, topicID); got != pending {
		t.Fatalf("pre-marker lookup = %q, want newest pending %q", got, pending)
	}
	if err := agent.MarkCleanupPending(pending, "delete"); err != nil {
		t.Fatal(err)
	}
	if got := findTopicSession(dir, topicID); got != normal {
		t.Fatalf("lookup with cleanup-pending newest = %q, want normal %q", got, normal)
	}
	if err := agent.MarkCleanupPending(normal, "delete"); err != nil {
		t.Fatal(err)
	}
	if got := findTopicSession(dir, topicID); got != "" {
		t.Fatalf("lookup with only cleanup-pending sessions = %q, want empty", got)
	}
}

func TestOpenProjectTabSkipsCleanupPendingTopicSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	app := NewApp()
	topic, err := app.CreateTopic("project", projectRoot, "Pending topic")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	dir := desktopSessionDir(projectRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pending := writeTopicSessionWithPrompt(t, dir, "pending-topic.jsonl", topic.ID, "Pending topic", projectRoot, "pending topic prompt", time.Now())
	if err := agent.MarkCleanupPending(pending, "delete"); err != nil {
		t.Fatal(err)
	}
	if got := findTopicSession(dir, topic.ID); got != "" {
		t.Fatalf("topic lookup with only cleanup-pending session = %q, want empty", got)
	}
	if got, _ := app.findTopicSessionForTarget("project", projectRoot, topic.ID); got != "" {
		t.Fatalf("target topic lookup with only cleanup-pending session = %q, want empty", got)
	}

	meta, err := app.OpenProjectTab(projectRoot, topic.ID)
	if err != nil {
		t.Fatalf("open project tab: %v", err)
	}
	tab := waitForTabReady(t, app, meta.ID)
	if got := filepath.Clean(tab.Ctrl.SessionPath()); got == filepath.Clean(pending) {
		t.Fatalf("opened cleanup-pending topic session path %q", got)
	}
	for _, msg := range tab.Ctrl.History() {
		if msg.Content == "pending topic prompt" {
			t.Fatalf("opened cleanup-pending topic history at path %q: %+v", tab.Ctrl.SessionPath(), tab.Ctrl.History())
		}
	}
}

func saveAutoTitleCurrentSession(path string, messages []provider.Message) error {
	s := agent.NewSession("")
	for _, m := range messages {
		s.Add(m)
	}
	return s.Save(path)
}
func saveAutoTitleCurrentJSON(path, body string) error {
	var messages []provider.Message
	dec := json.NewDecoder(strings.NewReader(body))
	for dec.More() {
		var m provider.Message
		if err := dec.Decode(&m); err != nil {
			return err
		}
		m.Origin = provider.MessageOriginUser
		messages = append(messages, m)
	}
	return saveAutoTitleCurrentSession(path, messages)
}

func TestUpdateTopicSessionTitlesUsesTopicIndex(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	topicID := "topic_title_index"
	now := time.Now().UTC()
	valid := writeTopicSessionWithPrompt(t, dir, "valid.jsonl", topicID, "Old", "", "hello", now)
	unpreviewable := filepath.Join(dir, "unpreviewable.jsonl")
	if err := os.WriteFile(unpreviewable, []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := agent.SaveBranchMetaPreserveUpdated(unpreviewable, agent.BranchMeta{
		CreatedAt:  now.Add(-time.Minute),
		UpdatedAt:  now,
		Scope:      "global",
		TopicID:    topicID,
		TopicTitle: "Old",
	}); err != nil {
		t.Fatal(err)
	}

	NewApp().updateTopicSessionTitles(topicID, "Renamed")

	for _, path := range []string{valid, unpreviewable} {
		meta, ok, err := agent.LoadBranchMeta(path)
		if err != nil || !ok {
			t.Fatalf("load meta for %s: ok=%v err=%v", path, ok, err)
		}
		if meta.TopicTitle != "Renamed" {
			t.Fatalf("topic title for %s = %q, want Renamed", path, meta.TopicTitle)
		}
	}
}

func TestEnsureTopicIndexedConcurrentRunsHaveNoLostProjectUpdates(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectRoot := t.TempDir()
	const n = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {

		wg.Go(func() {
			<-start
			topicID := fmt.Sprintf("topic_recovered_%02d", i)
			if err := ensureTopicIndexed("project", projectRoot, topicID, fmt.Sprintf("Recovered %02d", i), topicTitleSourceManual); err != nil {
				t.Errorf("ensure topic indexed: %v", err)
			}
		})
	}
	close(start)
	wg.Wait()

	nodes := mustListProjectTree(t, NewApp())
	if len(nodes) != 1 {
		t.Fatalf("project tree len = %d, want 1: %#v", len(nodes), nodes)
	}
	got := map[string]bool{}
	for _, child := range nodes[0].Children {
		got[child.TopicID] = true
	}
	for i := range n {
		topicID := fmt.Sprintf("topic_recovered_%02d", i)
		if !got[topicID] {
			t.Fatalf("concurrent topic index recovery lost %q; children=%#v", topicID, nodes[0].Children)
		}
		if title := loadTopicTitle(projectRoot, topicID); title == "" {
			t.Fatalf("title index missing %q", topicID)
		}
	}
}
