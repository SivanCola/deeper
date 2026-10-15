package main

import (
	"context"
	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/sessioncatalog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installSessionCatalogForTest(t *testing.T, app *App, path, scope, workspaceRoot string) {
	t.Helper()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatalf("open in-memory session catalog: %v", err)
	}
	target := sessioncatalog.DirectoryTarget{Path: path, Scope: scope, WorkspaceRoot: workspaceRoot}
	if err := catalog.ReconcileDirectory(context.Background(), target); err != nil {
		_ = catalog.Close(context.Background())
		t.Fatalf("reconcile session catalog %q: %v", target.Path, err)
	}
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() {
		app.stopSessionCatalog(time.Second)
	})
}

func reconcileSessionCatalogForTest(t *testing.T, app *App, path, scope, workspaceRoot string) {
	t.Helper()
	catalog := app.sessionCatalog.Load()
	if catalog == nil {
		t.Fatal("session catalog is not installed")
	}
	target := sessioncatalog.DirectoryTarget{Path: path, Scope: scope, WorkspaceRoot: workspaceRoot}
	if err := catalog.ReconcileDirectory(context.Background(), target); err != nil {
		t.Fatalf("reconcile session catalog %q: %v", path, err)
	}
}

func TestProjectTreeSnapshotReturnsProjectShellWithoutMigratingSessions(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Large Project"); err != nil {
		t.Fatal(err)
	}
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(sessionDir, "legacy.jsonl")
	if err := os.WriteFile(legacyPath, []byte(`{"role":"user","content":"legacy"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot := mustProjectTreeSnapshot(t, NewApp())
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].Root != root {
		t.Fatalf("snapshot = %#v, want project shell %q", snapshot, root)
	}
	if snapshot.Projects[0].Children == nil {
		t.Fatal("project shell children encoded as null, want []")
	}
	if _, err := os.Stat(legacyPath + ".meta"); !os.IsNotExist(err) {
		t.Fatalf("snapshot migrated session metadata: %v", err)
	}
}

func TestProjectTreeSnapshotIncludesPinnedTopicsForCollapsedFolders(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Pinned Project"); err != nil {
		t.Fatal(err)
	}
	if err := setTopicTitle(root, "ordinary", "Ordinary chat"); err != nil {
		t.Fatal(err)
	}
	if err := setTopicTitle(root, "pinned", "Pinned chat"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.SetTopicPinned("pinned", true); err != nil {
		t.Fatal(err)
	}

	snapshot := mustProjectTreeSnapshot(t, app)
	if len(snapshot.Projects) != 1 {
		t.Fatalf("project shells = %#v, want one project", snapshot.Projects)
	}
	children := snapshot.Projects[0].Children
	if len(children) != 1 || children[0].TopicID != "pinned" || !children[0].Pinned {
		t.Fatalf("collapsed project children = %#v, want only pinned topic shell", children)
	}
	if children[0].Label != "Pinned chat" {
		t.Fatalf("pinned topic label = %q, want %q", children[0].Label, "Pinned chat")
	}
}

func TestCompatibilityProjectTreeDoesNotMigrateLegacySession(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Project"); err != nil {
		t.Fatal(err)
	}
	sessionDir := desktopSessionDir(root)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(sessionDir, "legacy.jsonl")
	if err := os.WriteFile(legacyPath, []byte(`{"role":"user","content":"legacy"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_ = mustListProjectTree(t, NewApp())
	if _, err := os.Stat(legacyPath + ".meta"); !os.IsNotExist(err) {
		t.Fatalf("ListProjectTree migrated legacy session: %v", err)
	}
}

func TestProjectTreeShellSurvivesCatalogRevisionRace(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Shell Race"); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	// Catalog not open yet: revision stays 0 while the shell still returns projects.
	snapshot := mustProjectTreeSnapshot(t, app)
	if snapshot.Revision != 0 {
		t.Fatalf("revision = %d, want 0 while catalog is opening", snapshot.Revision)
	}
	if len(snapshot.Projects) == 0 {
		t.Fatal("project shell empty while catalog opening")
	}
	found := false
	for _, project := range snapshot.Projects {
		if project.Root == root || project.Label == "Shell Race" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("snapshot projects = %#v, want Shell Race", snapshot.Projects)
	}
}

func TestListProjectTopicsSurfacesLiveTabWhileCatalogLags(t *testing.T) {
	app, root, refs := canonicalOrganizationFixture(t, "live")
	app.tabs["tab"] = &WorkspaceTab{ID: "tab", Scope: "project", WorkspaceRoot: root, SessionID: refs["live"].SessionID, TopicID: "live", Ready: true}
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root})
	if err != nil || len(page.Items) != 1 || page.Items[0].Session == nil || *page.Items[0].Session != refs["live"] {
		t.Fatalf("current live row = %+v %v", page, err)
	}
	if page.Items[0].CreatedAt <= 0 {
		t.Fatal("live row has no creation time")
	}
}

func TestListProjectTopicsDoesNotDuplicateIndexedLiveTab(t *testing.T) {
	app, root, refs := canonicalOrganizationFixture(t, "indexed")
	app.tabs["tab"] = &WorkspaceTab{ID: "tab", Scope: "project", WorkspaceRoot: root, SessionID: refs["indexed"].SessionID, TopicID: "indexed", Ready: true}
	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root})
	if err != nil || len(page.Items) != 1 || page.Items[0].Session == nil || *page.Items[0].Session != refs["indexed"] {
		t.Fatalf("current live row = %+v %v", page, err)
	}
	if page.Items[0].CreatedAt <= 0 {
		t.Fatal("live row has no creation time")
	}
}

func TestListProjectTopicsHonorsConversationSortMode(t *testing.T) {
	app, root, refs := canonicalOrganizationFixture(t, "older", "newer")
	time.Sleep(time.Millisecond)
	rt, ok := app.desktopSessionService("").Runtime(refs["older"])
	if !ok {
		t.Fatal("missing current runtime")
	}
	appendSessionTestMessage(t, rt, "latest", provider.Message{ID: "latest", Role: provider.RoleUser, Content: "latest activity"})
	for _, tc := range []struct{ mode, want string }{{"created", "newer"}, {"updated", "older"}} {
		page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, SortMode: tc.mode})
		if err != nil || len(page.Items) != 2 || page.Items[0].Session.SessionID != tc.want {
			t.Fatalf("%s = %+v %v", tc.mode, page, err)
		}
	}
}

// The catalog goroutine outlives every request, so an unbounded metadata sync
// there can hold the catalog's single-writer mutex forever and freeze the whole
// sidebar. Guard the call shape, not just today's behaviour.
func TestSessionCatalogGoroutineOnlySyncsMetadataWithATimeout(t *testing.T) {
	source, err := os.ReadFile("session_catalog.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	start := strings.Index(body, "func (a *App) startSessionCatalog(")
	if start < 0 {
		t.Fatal("startSessionCatalog not found")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("end of startSessionCatalog not found")
	}
	for line := range strings.SplitSeq(body[start:start+1+end], "\n") {
		if !strings.Contains(line, "syncSessionCatalogMetadata(") {
			continue
		}
		if !strings.Contains(line, "syncSessionCatalogMetadataBounded(") {
			t.Fatalf("unbounded metadata sync in startSessionCatalog: %s", strings.TrimSpace(line))
		}
	}
}

func TestListProjectTopicsKeepsMetadataWhileFirstCatalogScanIsPending(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	if err := addProject(root, "Upgraded App"); err != nil {
		t.Fatal(err)
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		for i := range f.Projects {
			if sameProjectRoot(f.Projects[i].Root, root) {
				f.Projects[i].Topics = []string{"topic-keep"}
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := saveTopicTitles(root, map[string]string{"topic-keep": "Previous chat"}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.sessionCatalog.CompareAndSwap(catalog, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = catalog.Close(ctx)
	})
	app.sessionCatalog.Store(catalog)

	page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].TopicID != "topic-keep" || page.Items[0].Label != "Previous chat" {
		t.Fatalf("topics while v6 catalog is still empty = %#v, want the desktop-projects conversation", page.Items)
	}
}

func TestProjectTreeSnapshotIndexingWaitsForFirstDirectoryScan(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.sessionCatalog.CompareAndSwap(catalog, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = catalog.Close(ctx)
	})
	app.sessionCatalog.Store(catalog)
	snapshot := mustProjectTreeSnapshot(t, app)
	if snapshot.IndexingDone {
		t.Fatal("indexingDone must stay false until the first directory scan finishes")
	}
}

func TestContinuePathForOpenFollowsCoveringLeafFromParent(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopSessionDir(globalWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	q := provider.Message{Role: provider.RoleUser, Content: "question"}
	a := provider.Message{Role: provider.RoleAssistant, Content: "answer"}
	next := provider.Message{Role: provider.RoleUser, Content: "next"}
	done := provider.Message{Role: provider.RoleAssistant, Content: "done"}
	save := func(path, topic string, messages ...provider.Message) {
		t.Helper()
		session := agent.NewSession("sys")
		for _, message := range messages {
			session.Add(message)
		}
		if err := session.Save(path); err != nil {
			t.Fatal(err)
		}
		if err := agent.SaveBranchMetaPreserveUpdated(path, agent.BranchMeta{
			ID: agent.BranchID(path), Scope: "global", TopicID: topic, TopicTitle: "Upgraded",
		}); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, "root.jsonl")
	leaf := filepath.Join(dir, "leaf.jsonl")
	save(root, "conversation", q, a)
	save(leaf, "legacy-leaf-topic", q, a, next, done)
	if err := agent.SaveBranchMetaPreserveUpdated(leaf, agent.BranchMeta{
		ID: "leaf", Scope: "global", TopicID: "legacy-leaf-topic",
		Recovered: true, ParentID: "root", RecoveryDepth: 1,
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	installSessionCatalogForTest(t, app, dir, "global", "")
	if got := app.continuePathForOpen(root); got != leaf {
		t.Fatalf("continue parent = %q, want covering leaf %q", got, leaf)
	}
	if got := app.continuePathForOpen(leaf); got != "" {
		t.Fatalf("continue leaf = %q, want keep", got)
	}
}

func TestMetadataCatalogAvailabilityUsesDiscoveryStateForAbsentRoots(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	catalog, err := sessioncatalog.Open(t.Context(), sessioncatalog.Options{InMemory: true, MetadataOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() { app.stopSessionCatalog(time.Second) })
	legacy := config.SessionDir()
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(legacy, "retained.jsonl")
	if err := os.WriteFile(path, []byte("body must not be read\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := app.catalogWorkspaceAvailability(catalog, "global", "")
	if before.complete || before.pending == 0 {
		t.Fatalf("unknown directories declared complete: %+v", before)
	}
	for _, target := range app.sessionCatalogTargets() {
		if err := catalog.ReconcileDirectory(t.Context(), target); err != nil {
			t.Fatal(err)
		}
	}
	ready := app.catalogWorkspaceAvailability(catalog, "global", "")
	if !ready.complete || ready.failed != 0 || ready.pending != 0 {
		t.Fatalf("optional absent roots did not settle: %+v", ready)
	}
	if err := os.Rename(legacy, legacy+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ReconcileDirectory(t.Context(), sessioncatalog.DirectoryTarget{Path: legacy, Scope: "global"}); !os.IsNotExist(err) {
		t.Fatalf("known missing root accepted: %v", err)
	}
	offline := app.catalogWorkspaceAvailability(catalog, "global", "")
	if offline.complete || !offline.usable || offline.failed != 1 {
		t.Fatalf("unavailable history was hidden from completeness: %+v", offline)
	}
	row, found, err := catalog.GetSession(t.Context(), path)
	if err != nil || !found || row.MissingSince != 0 {
		t.Fatalf("unavailable root lost retained row: %+v %v", row, err)
	}
}

func TestListProjectTopicsPaginatesMetadataWhileCatalogIsPartiallyAvailable(t *testing.T) {
	isolateDesktopUserDirs(t)
	legacy := config.SessionDir()
	global := desktopSessionDir(globalWorkspaceRoot())
	for _, dir := range []string{legacy, global} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		f.GlobalTopics = []string{"topic-a", "topic-b", "topic-c"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := saveTopicTitles("", map[string]string{
		"topic-a": "A", "topic-b": "B", "topic-c": "C",
	}); err != nil {
		t.Fatal(err)
	}
	if err := saveTopicCreatedAts("", map[string]int64{
		"topic-a": 100, "topic-b": 200, "topic-c": 300,
	}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	catalog, err := sessioncatalog.Open(context.Background(), sessioncatalog.Options{InMemory: true, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		app.sessionCatalog.CompareAndSwap(catalog, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = catalog.Close(ctx)
	})
	app.sessionCatalog.Store(catalog)
	if err := catalog.ReconcileDirectory(context.Background(), sessioncatalog.DirectoryTarget{Path: legacy, Scope: "global"}); err != nil {
		t.Fatal(err)
	}

	first, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].TopicID != "topic-c" || first.Items[1].TopicID != "topic-b" {
		t.Fatalf("first page topics = %#v", first.Items)
	}
	if first.NextCursor == "" {
		t.Fatal("first page omitted metadata continuation cursor")
	}
	second, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].TopicID != "topic-a" {
		t.Fatalf("second page topics = %#v", second.Items)
	}
	if second.NextCursor != "" {
		t.Fatalf("second page next cursor = %q, want empty", second.NextCursor)
	}
}

type retargetRuntimeController struct {
	stubSessionAPI
	status control.RuntimeStatus
	path   string
}

func (c *retargetRuntimeController) RuntimeStatus() control.RuntimeStatus { return c.status }
func (c *retargetRuntimeController) SessionPath() string                  { return c.path }
func (c *retargetRuntimeController) PlanMode() bool                       { return false }
func (c *retargetRuntimeController) AutoApproveTools() bool               { return false }
func (c *retargetRuntimeController) Goal() string                         { return "" }
func (c *retargetRuntimeController) GoalStatus() string                   { return "" }
func (c *retargetRuntimeController) ToolApprovalMode() string             { return control.ToolApprovalReadOnly }

func (c *retargetRuntimeController) RuntimeStateSnapshot() event.RuntimeStateSnapshot {
	return runtimeSnapshotForTest(c.RuntimeStatus())
}
