package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/session"
)

// sidebarLabelForSession returns the label the project tree paints for ref.
func sidebarLabelForSession(t *testing.T, app *App, root string, ref session.SessionRef) string {
	t.Helper()
	page, err := app.unifiedProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil {
		t.Fatalf("sidebar page: %v", err)
	}
	for _, node := range page.Items {
		if node.Session != nil && node.Session.SessionID == ref.SessionID {
			return node.Label
		}
	}
	t.Fatalf("session %q missing from the sidebar page", ref.SessionID)
	return ""
}

func canonicalTitleFixture(t *testing.T) (*App, *WorkspaceTab, *session.Runtime, session.SessionRef) {
	t.Helper()
	app, tab, target, _, _ := canonicalWorkspaceOpenFixture(t)
	ref := session.SessionRef{HostID: localDesktopHostID, SessionID: tab.SessionID}
	// An automatic label is a projection; neither current store persists it as
	// an explicitly chosen title.
	if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, "topic-A", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenSession(ref); err != nil {
		t.Fatal(err)
	}
	return app, tab, target, ref
}

// An untitled session used to show its first user turn in the sidebar and the
// presentation default in the topicbar. Both surfaces share one projection.
func TestUntitledCanonicalSessionSurfacesAgree(t *testing.T) {
	app, tab, _, ref := canonicalTitleFixture(t)
	topicbar := app.tabMeta(tab, true).TopicTitle
	sidebar := sidebarLabelForSession(t, app, tab.WorkspaceRoot, ref)
	if topicbar != sidebar {
		t.Fatalf("topicbar %q != sidebar %q", topicbar, sidebar)
	}
	if topicbar != "session-A" {
		t.Fatalf("untitled session must show its first user turn, got %q", topicbar)
	}
}

// A canonical rename writes only the session log. Every tab bind, including
// the restart rebuild that starts from the legacy topic map, must re-derive
// the name from that log, and the presentation row must mirror it.
func TestCanonicalRenameSurvivesSwitchAndRebuild(t *testing.T) {
	app, tab, target, ref := canonicalTitleFixture(t)
	rootA := tab.WorkspaceRoot
	const want = "AI 生成的标题"
	if err := app.RenameCanonicalSession(ref, want); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if tab.TopicTitle != want || tab.topicTitleSource != topicTitleSourceManual {
		t.Fatalf("live tab after rename = %q/%q", tab.TopicTitle, tab.topicTitleSource)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Presentation[ref.SessionID].Title; got != want {
		t.Fatalf("presentation title = %q, want write-through %q", got, want)
	}

	if _, err := app.OpenSession(target.Ref()); err != nil {
		t.Fatalf("switch away: %v", err)
	}
	if _, err := app.OpenSession(ref); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	if got := app.tabMeta(tab, true).TopicTitle; got != want {
		t.Fatalf("topicbar after switching back = %q, want %q", got, want)
	}
	if got := sidebarLabelForSession(t, app, rootA, ref); got != want {
		t.Fatalf("sidebar after rename = %q, want %q", got, want)
	}

	// Restart: restoreOrBuildTabs recreates every local tab through
	// createTabEntryWithID and then builds its controller. Park the visible
	// surface on B first so A is cold, exactly like a fresh process.
	if _, err := app.OpenSession(target.Ref()); err != nil {
		t.Fatalf("park on B: %v", err)
	}
	const restoredID = "restored-tab"
	restored := app.createTabEntryWithID("project", rootA, "topic-A", restoredID)
	if restored.TopicTitle == want {
		t.Fatal("fixture must start from the legacy topic map, not the log")
	}
	restored.SessionID = ref.SessionID
	restored.sink = &tabEventSink{tabID: restoredID, app: app}
	app.mu.Lock()
	app.tabs[restoredID] = restored
	app.tabOrder = append(app.tabOrder, restoredID)
	app.activeTabID = restoredID
	app.mu.Unlock()
	app.startTabControllerBuild(restored)
	built := waitForTabReady(t, app, restoredID)
	if got := app.tabMeta(built, true).TopicTitle; got != want {
		t.Fatalf("topicbar after restart rebuild = %q, want %q", got, want)
	}
}

// Clearing a committed title must not leave the topicbar blank while the
// sidebar shows the preview: the log owns the name, the shared chain closes it.
func TestClearedCanonicalTitleFallsBackToPreviewEverywhere(t *testing.T) {
	app, tab, _, ref := canonicalTitleFixture(t)
	if err := app.RenameCanonicalSession(ref, "临时标题"); err != nil {
		t.Fatal(err)
	}
	if err := app.RenameCanonicalSession(ref, ""); err != nil {
		t.Fatal(err)
	}
	topicbar := app.tabMeta(tab, true).TopicTitle
	sidebar := sidebarLabelForSession(t, app, tab.WorkspaceRoot, ref)
	if topicbar != "session-A" || sidebar != "session-A" {
		t.Fatalf("cleared title: topicbar %q sidebar %q, want the first user turn", topicbar, sidebar)
	}
	state, err := app.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Presentation[ref.SessionID].Title; got != "" {
		t.Fatalf("presentation title after clear = %q, want empty", got)
	}
}

func TestRenameSessionRejectsHistoricalFilePath(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	dir := t.TempDir()
	path := writeTopicSessionWithPrompt(t, dir, "historical-title.jsonl", "historical-topic", "Original", "", "prompt", time.Now())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RenameSession(path, "Unsupported"); err == nil {
		t.Fatal("historical file path accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("historical source changed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".titles.json")); !os.IsNotExist(err) {
		t.Fatal("historical title map was written")
	}
}
