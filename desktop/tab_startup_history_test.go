package main

import (
	"testing"

	"deepzora/internal/session"
)

func TestTerminalStartupFailureRemainsInProjectHistoryAfterRestart(t *testing.T) {
	app, root, refs := canonicalOrganizationFixture(t, "failed-startup")
	ref := refs["failed-startup"]
	wid, err := app.resolveDesktopWorkspaceID(t.Context(), "project", root)
	if err != nil {
		t.Fatal(err)
	}
	failed := &WorkspaceTab{ID: "failed", Scope: "project", WorkspaceRoot: root, SessionID: ref.SessionID, Ready: true, disabledMCP: map[string]ServerView{}}
	failed.SessionWorkspace.ID = wid
	app.tabs[failed.ID] = failed
	app.tabOrder = []string{failed.ID}
	app.activeTabID = failed.ID
	app.mu.Lock()
	app.newSessionRuntimeLocked(failed, sessionRuntimeKey(failed.currentSessionIdentity()))
	leaseHeld, save := app.markTabStartupFailureLocked(failed, session.ErrSessionNotRunning, suppressStartupRestore)
	app.mu.Unlock()
	if leaseHeld || save == nil {
		t.Fatal("terminal startup failure was not persisted")
	}
	app.writeTabsSaveRequest(save)
	if got := loadTabsFile(); len(got.Tabs) != 0 || got.ActiveTab != "" {
		t.Fatalf("failed runtime restored: %+v", got)
	}
	sessionRoot := app.desktopSessions.root
	app.closeSessionServices()
	restarted := NewApp()
	restarted.desktopSessions.root = sessionRoot
	restarted.desktopSessions.workspaceState = app.desktopSessions.workspaceState
	t.Cleanup(restarted.closeSessionServices)
	if _, err := restarted.desktopSessionService(sessionRoot).Query().Stat(t.Context(), ref); err != nil {
		t.Fatalf("durable history lost: %v", err)
	}
	page, err := restarted.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: root, Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].Session == nil || page.Items[0].Session.SessionID != ref.SessionID {
		t.Fatalf("restart history: %+v %v", page, err)
	}
}
