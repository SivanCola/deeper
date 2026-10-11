package main

import (
	"testing"

	"deepzora/desktop/internal/workspacestate"
)

// Archiving the only visible session hands the surface to the empty welcome.
// A replacement blank session would be registered as a real sidebar row that
// the user can archive again, so the workspace could never become empty.
func TestArchiveLastVisibleSessionLeavesNoReplacementBlankSession(t *testing.T) {
	a, ref := lifecycleFixture(t)
	a.tabs = map[string]*WorkspaceTab{
		"only": {ID: "only", Scope: "global", WorkspaceRoot: globalWorkspaceRoot(), TopicID: "canonical-" + ref.SessionID,
			SessionID: ref.SessionID, Ready: true, disabledMCP: map[string]ServerView{}},
	}
	a.tabOrder = []string{"only"}
	a.activeTabID = "only"

	archived, err := a.ArchiveSessionTarget(SessionSelector{Ref: &ref})
	if err != nil || !archived.Committed {
		t.Fatalf("ArchiveSessionTarget = %+v, %v", archived, err)
	}
	assertNoVisibleRuntime(t, a)
	state, err := a.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.SessionStates[ref.SessionID].Lifecycle; got != workspacestate.Archived {
		t.Fatalf("archived lifecycle = %q", got)
	}
	for id, sessionState := range state.SessionStates {
		if sessionState.Lifecycle == workspacestate.Active {
			t.Fatalf("archive registered replacement session %q", id)
		}
	}
	page, err := a.ListWorkspaceSessions(workspacestate.GlobalWorkspaceID, "", "", 50, false)
	if err != nil || len(page.Sessions) != 0 {
		t.Fatalf("workspace still lists sessions after archiving its only one: %+v, %v", page.Sessions, err)
	}
}

func assertNoVisibleRuntime(t *testing.T, a *App) {
	t.Helper()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.tabs) != 0 || len(a.tabOrder) != 0 || a.activeTabID != "" {
		ids := make([]string, 0, len(a.tabs))
		for id, tab := range a.tabs {
			ids = append(ids, id+"/topic="+tab.TopicID+"/session="+tab.SessionID)
		}
		t.Fatalf("removing the last visible session opened a replacement runtime: tabs=%v order=%v active=%q", ids, a.tabOrder, a.activeTabID)
	}
}
