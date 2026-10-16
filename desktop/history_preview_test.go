package main

import (
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"testing"
)

func TestColdHistoryPreservesReasoningAndUserTimeWithoutResuming(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	service := app.desktopSessionService("")
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "history-preview"})
	if err != nil {
		t.Fatal(err)
	}
	const timestamp = int64(1718000000000)
	appendSessionTestMessage(t, runtime, "question", provider.Message{
		ID: "question", Role: provider.RoleUser, Origin: provider.MessageOriginUser,
		Content: "show history", CreatedAt: timestamp,
	})
	appendSessionTestMessage(t, runtime, "answer", provider.Message{
		ID: "answer", Role: provider.RoleAssistant, Content: "answer", ReasoningContent: "saved reasoning",
	})
	ref := runtime.Ref()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	tab := &WorkspaceTab{ID: "preview", SessionID: ref.SessionID, Ready: false}
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	rows := currentHistoryMessagesForTab(t, app, tab.ID)
	if len(rows) != 2 || rows[0].CreatedAt != timestamp || rows[1].Reasoning != "saved reasoning" {
		t.Fatalf("cold history lost current fields: %+v", rows)
	}
	if tab.Ctrl != nil || tab.Ready {
		t.Fatal("history read started the tab controller")
	}
	if _, live := service.Runtime(ref); live {
		t.Fatal("history read acquired execution ownership")
	}
}
