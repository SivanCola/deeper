package main

import (
	"path/filepath"
	"testing"
)

func TestTopicOpenRejectsHistoricalPathsBeforePublishing(t *testing.T) {
	app := NewApp()
	for _, path := range []string{filepath.Join(t.TempDir(), "root.jsonl"), "root.jsonl", "session-id:../root"} {
		if _, err := app.openTopicTabWithActivation("global", "", "topic", path, true); err == nil {
			t.Fatalf("accepted old or malformed session identity %q", path)
		}
		if len(app.tabs) != 0 || app.activeTabID != "" {
			t.Fatal("rejected identity published a tab")
		}
	}
}

func TestTopicOpenKeepsCurrentIdentity(t *testing.T) {
	app, tab, _, _, _ := canonicalWorkspaceOpenFixture(t)
	installNoopRuntimeEvents(app)
	app.readyHook = func() {}
	ctrl, id := tab.Ctrl, tab.SessionID
	tab.ActivityStatus = topicStatusPaused
	meta, err := app.openTopicTabWithActivation(tab.Scope, tab.WorkspaceRoot, tab.TopicID, sessionRoute(id), true)
	if err != nil {
		t.Fatal(err)
	}
	if meta.SessionID != id || tab.SessionID != id || tab.Ctrl != ctrl {
		t.Fatal("topic selection redirected or replaced the selected current session")
	}
}
