package main

import (
	"testing"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

func currentTopicInfo(t *testing.T, app *App, topicID string) (session.SessionRef, session.SessionInfo) {
	t.Helper()
	target, err := app.resolveSessionTarget(SessionSelector{TopicID: topicID})
	if err != nil || target.SessionRef.SessionID == "" {
		t.Fatalf("resolve current topic: %+v, %v", target, err)
	}
	info, err := app.desktopSessionService("").Query().Stat(t.Context(), target.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	return target.SessionRef, info
}

func TestCreateTopicPersistsCurrentColdSession(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		t.Run(scope, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			app := NewApp()
			app.ctx = t.Context()
			installNoopRuntimeEvents(app)
			t.Cleanup(app.closeSessionServices)
			root := ""
			if scope == "project" {
				root = t.TempDir()
			}
			topic, err := app.CreateTopic(scope, root, "Named empty session")
			if err != nil {
				t.Fatal(err)
			}
			ref, info := currentTopicInfo(t, app, topic.ID)
			if info.Title != topic.Title || info.CreatedAt.UnixMilli() != topic.CreatedAt || info.Origin != session.SessionOriginNew {
				t.Fatalf("current session metadata: %+v, topic: %+v", info, topic)
			}
			if _, open := app.desktopSessionService("").Runtime(ref); open {
				t.Fatal("creation left a runtime open")
			}
			if len(app.tabs) != 0 {
				t.Fatal("cold creation opened a tab")
			}
			app.closeSessionServices()
			reopened := NewApp()
			reopened.ctx = t.Context()
			installNoopRuntimeEvents(reopened)
			t.Cleanup(reopened.closeSessionServices)
			after, _ := currentTopicInfo(t, reopened, topic.ID)
			if after != ref {
				t.Fatalf("restart changed identity: %+v -> %+v", ref, after)
			}
			if err := reopened.TrashTopic(topic.ID); err != nil {
				t.Fatal(err)
			}
			state, err := reopened.workspaceRegistry().Load(t.Context())
			if err != nil || state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Archived {
				t.Fatalf("archive did not update current lifecycle: %+v, %v", state.SessionStates[ref.SessionID], err)
			}
		})
	}
}

func TestColdTopicRecoveryPreservesManualDefaultTitle(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	t.Cleanup(app.closeSessionServices)
	interruptTopicRemoval(t, app, "cold-topic-before-title", func() {
		_, _ = app.CreateTopic("global", "", defaultTopicTitle)
	})
	app.closeSessionServices()
	restarted := NewApp()
	restarted.ctx = t.Context()
	restarted.setDesktopLocale("en-US")
	installNoopRuntimeEvents(restarted)
	t.Cleanup(restarted.closeSessionServices)
	if err := restarted.recoverDesktopPendingCreates(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := restarted.ListProjectTopics(ProjectTopicPageRequest{Scope: "global", Limit: 50})
	if err != nil || len(page.Items) != 1 || page.Items[0].Session == nil || page.Items[0].Label != defaultTopicTitle {
		t.Fatalf("current create recovery lost manual name: %+v, %v", page, err)
	}
	state, err := restarted.workspaceRegistry().Load(t.Context())
	if err != nil || len(state.PendingCreates) != 0 {
		t.Fatalf("current create journal did not settle: %+v, %v", state.PendingCreates, err)
	}
}
