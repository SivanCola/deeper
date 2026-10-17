package main

import (
	"testing"
)

// The opaque-ID title cleanup exists because older builds persisted a
// canonical session ID as the tab title. A legacy row has no canonical
// identity and its name is the transcript basename, which the sidebar also
// shows as the label — discarding it there makes the tab read "app" while the
// sidebar reads "chat-2026-09" after every restart.
func TestRemoteRestorePreservesExplicitTitleAndDefaultsOnlyWhenEmpty(t *testing.T) {
	seedBridgeTestHost(t, "box")
	a := &App{}
	a.restoreRemoteTabShells(desktopTabsFile{
		RemoteTabs: []desktopRemoteTabEntry{
			{ID: "named", HostID: "box", Workspace: "~/app", TopicTitle: "Project notes", SessionID: "chat-id"},
			{ID: "canonical", HostID: "box", Workspace: "~/app2", TopicTitle: "abc123", SessionID: "abc123"},
			{ID: "untitled", HostID: "box", Workspace: "~/app3", SessionID: "other-id"},
		},
		RemoteTabOrder: []string{"named", "canonical", "untitled"},
	})

	a.remoteTabMu.Lock()
	defer a.remoteTabMu.Unlock()
	if got := a.remoteTabs["named"].topicTitle; got != "Project notes" {
		t.Fatalf("restored title = %q, want the persisted title", got)
	}
	if got := a.remoteTabs["canonical"].topicTitle; got != "abc123" {
		t.Fatalf("explicit title matching its ID was rewritten: %q", got)
	}
	if got := a.remoteTabs["untitled"].topicTitle; got != remoteWorkspaceName("~/app3") {
		t.Fatalf("empty restored title = %q, want the workspace fallback", got)
	}
}
