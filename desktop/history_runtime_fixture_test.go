package main

import (
	"context"
	"encoding/json"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/transcript"
)

func bindHistoryRuntime(t *testing.T, app *App, tab *WorkspaceTab, runner agent.Runner, executor *agent.Agent) *control.Controller {
	t.Helper()
	app.ctx = t.Context()
	service := app.desktopSessionService(t.TempDir())
	t.Cleanup(app.closeSessionServices)
	ctrl := control.New(control.Options{Runner: runner, Executor: executor, SessionService: service, SessionCreateService: service, Sink: tab.sink})
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed() })
	ref, workspace, err := app.bindFreshDesktopSessionWithIDs(t.Context(), tab.Scope, tab.WorkspaceRoot, ctrl, "history-"+tab.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	tab.Ctrl, tab.SessionID = ctrl, ref.SessionID
	tab.SessionWorkspace.ID = workspace
	installNoopRuntimeEvents(app, tab.sink)
	return ctrl
}

func currentTranscriptMessages(t *testing.T, ctrl *control.Controller) []HistoryMessage {
	t.Helper()
	response, err := ctrl.TranscriptFollow(t.Context(), transcript.FollowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.TranscriptFollow(context.Background(), transcript.FollowRequest{Subscription: response.Subscription, Close: true})
	snapshot := response.Snapshot
	if snapshot == nil {
		t.Fatal("current Follow returned no snapshot")
	}
	rows := make([]HistoryMessage, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		rows = append(rows, record.Message)
	}
	return rows
}

func currentHistoryWindowForTab(t *testing.T, app *App, tabID string) session.HistoryWindowPage {
	t.Helper()
	handle, err := app.BeginSessionHistoryReadForTab(tabID)
	if err != nil {
		t.Fatal(err)
	}
	defer app.ReleaseSessionHistoryRead(handle.ID)
	page := currentBoundWindow(t, app, handle.ID, session.HistoryWindowRequest{Anchor: "newest", Limit: 128})
	if page.Status != "ready" {
		t.Fatalf("current history status=%q", page.Status)
	}
	return page
}

func currentHistoryMessagesForTab(t *testing.T, app *App, tabID string) []HistoryMessage {
	t.Helper()
	page := currentHistoryWindowForTab(t, app, tabID)
	var messages []provider.Message
	for _, row := range page.Messages {
		if row.ContentRef != nil {
			t.Fatal("small-message fixture unexpectedly requires content streaming")
		}
		var message provider.Message
		if err := json.Unmarshal(row.Inline, &message); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	return transcript.History(messages, transcript.HistoryOptions{})
}

func currentHistoryUser(i int, text string) provider.Message {
	return provider.Message{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: text, CreatedAt: 1_700_000_000_000 + int64(i)}
}

func currentHistoryAssistant(i int, text string) provider.Message {
	return provider.Message{Role: provider.RoleAssistant, Content: text, CreatedAt: 1_700_000_000_000 + int64(i)}
}
