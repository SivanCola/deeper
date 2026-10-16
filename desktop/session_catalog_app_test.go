package main

import (
	"deepzora/internal/provider"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	if _, err := app.CreateTopic("project", root, "Ordinary chat"); err != nil {
		t.Fatal(err)
	}
	pinned, err := app.CreateTopic("project", root, "Pinned chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SetTopicPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}

	snapshot := mustProjectTreeSnapshot(t, app)
	if len(snapshot.Projects) != 1 {
		t.Fatalf("project shells = %#v, want one project", snapshot.Projects)
	}
	children := snapshot.Projects[0].Children
	if len(children) != 1 || children[0].TopicID != pinned.ID || !children[0].Pinned {
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
