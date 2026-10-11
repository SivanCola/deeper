package main

import (
	"testing"

	"deepzora/internal/session"
)

func TestCanonicalPinnedShellUsesRegistryAndArchiveLifecycle(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	pinDesktopSessionRoot(t, app)
	t.Cleanup(app.closeSessionServices)
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "global", "")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{SessionID: "pin-fixture", CWD: globalWorkspaceRoot(), Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, runtime.Ref().SessionID, ""); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "fixture-tab", Scope: "global", SessionID: runtime.Ref().SessionID, SessionWorkspace: desktopTabWorkspace{ID: workspaceID}}
	app.tabs[tab.ID] = tab
	if err := app.ensureTabTopicIndexedForUserTurn(tab); err != nil {
		t.Fatal(err)
	}
	if err := app.RenameCanonicalSession(runtime.Ref(), "Canonical title"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetTopicPinned(tab.TopicID, true); err != nil {
		t.Fatal(err)
	}
	assertPins := func(want int) {
		t.Helper()
		snapshot := mustProjectTreeSnapshot(t, app)
		pins := []ProjectNode{}
		for _, project := range snapshot.Projects {
			pins = append(pins, project.Children...)
		}
		if len(pins) != want {
			t.Fatalf("pins = %+v, want %d", pins, want)
		}
		if want == 1 && (pins[0].Label != "Canonical title" || pins[0].Session == nil || pins[0].Session.SessionID != runtime.Ref().SessionID || !pins[0].Pinned) {
			t.Fatalf("stale pinned shell: %+v", pins[0])
		}
	}
	assertPins(1)
	if err := app.workspaceRegistry().ArchiveSession(t.Context(), runtime.Ref().SessionID); err != nil {
		t.Fatal(err)
	}
	assertPins(0)
	if err := app.workspaceRegistry().RestoreSession(t.Context(), runtime.Ref().SessionID); err != nil {
		t.Fatal(err)
	}
	assertPins(1)
}
