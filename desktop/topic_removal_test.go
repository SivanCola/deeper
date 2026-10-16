package main

import (
	"deepzora/desktop/internal/workspacestate"
	"testing"
)

func topicRemovalFixture(t *testing.T, scope, title string) (*App, TopicMeta, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	a := NewApp()
	a.ctx = t.Context()
	installNoopRuntimeEvents(a)
	t.Cleanup(a.closeSessionServices)
	root := ""
	if scope == "project" {
		root = t.TempDir()
	}
	topic, err := a.CreateTopic(scope, root, title)
	if err != nil {
		t.Fatal(err)
	}
	return a, topic, root
}

func inspectedTopicRemoval(t *testing.T, a *App, topicID string) TopicRemovalRequest {
	t.Helper()
	view, err := a.InspectTopicRemoval(TopicRemovalTarget{TopicID: topicID})
	if err != nil || !view.Allowed {
		t.Fatalf("inspect: %+v %v", view, err)
	}
	return TopicRemovalRequest{OperationID: "remove-" + topicID, Target: view.Target, ExpectedToken: view.Token}
}

func TestTopicRemovalClassificationAndRestore(t *testing.T) {
	for _, scope := range []string{"global", "project"} {
		for _, title := range []string{"", "My plan", defaultTopicTitle} {
			t.Run(scope+"/"+title, func(t *testing.T) {
				a, topic, root := topicRemovalFixture(t, scope, title)
				ref, _ := currentTopicInfo(t, a, topic.ID)
				if err := a.SetTopicPinned(topic.ID, true); err != nil {
					t.Fatal(err)
				}
				req := inspectedTopicRemoval(t, a, topic.ID)
				out, err := a.RemoveTopic(req)
				if err != nil || !out.Committed || out.Disposition != "archive_sessions" {
					t.Fatalf("remove: %+v %v", out, err)
				}
				page, err := a.ListTrashEntries("", "", 50)
				if err != nil || len(page.Items) != 1 || page.Items[0].Ref == nil || *page.Items[0].Ref != ref || !page.Items[0].CanRestore || !page.Items[0].CanPreview || page.Items[0].Title != topic.Title {
					t.Fatalf("current trash: %+v %v", page, err)
				}
				row := page.Items[0]
				restored, err := a.ApplySessionLifecycle(SessionLifecycleRequest{OperationID: "restore", Action: "restore", ExpectedGeneration: page.Generation, Targets: []SessionLifecycleTarget{{Ref: row.Ref, WorkspaceID: row.WorkspaceID}}})
				if err != nil || !restored.Committed {
					t.Fatalf("restore: %+v %v", restored, err)
				}
				visible, err := a.ListProjectTopics(ProjectTopicPageRequest{Scope: scope, WorkspaceRoot: root, Limit: 50})
				if err != nil || len(visible.Items) != 1 || visible.Items[0].Session == nil || *visible.Items[0].Session != ref || visible.Items[0].Label != topic.Title || !visible.Items[0].Pinned {
					t.Fatalf("restored session: %+v %v", visible, err)
				}
				if out, err := a.RemoveTopic(req); err != nil || !out.Committed {
					t.Fatalf("duplicate removal receipt: %+v %v", out, err)
				}
				state, err := a.workspaceRegistry().Load(t.Context())
				if err != nil || state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Active {
					t.Fatalf("duplicate removal archived restored session: %+v %v", state.SessionStates[ref.SessionID], err)
				}
			})
		}
	}
}

func TestTopicRemovalRejectsChangedConfirmation(t *testing.T) {
	a, topic, _ := topicRemovalFixture(t, "global", "")
	req := inspectedTopicRemoval(t, a, topic.ID)
	if err := a.RenameTopic(topic.ID, "Keep this"); err != nil {
		t.Fatal(err)
	}
	out, err := a.RemoveTopic(req)
	if err != nil || out.Committed || out.ErrorCode != "state_conflict" {
		t.Fatalf("stale confirmation: %+v %v", out, err)
	}
	_, info := currentTopicInfo(t, a, topic.ID)
	if info.Title != "Keep this" {
		t.Fatal("changed session title was removed")
	}
}

func interruptTopicRemoval(t *testing.T, a *App, phase string, work func()) {
	t.Helper()
	a.lifecycleCheckpointHook = func(at string) {
		if at == phase {
			panic("simulated process exit")
		}
	}
	defer func() {
		a.lifecycleCheckpointHook = nil
		if recover() != "simulated process exit" {
			t.Fatal("checkpoint was not reached")
		}
	}()
	work()
}

func TestTopicRemovalRecoversInterruptedWrites(t *testing.T) {
	for _, phase := range []string{"topic-sessions-before-archive", "before-archive-commit"} {
		t.Run(phase, func(t *testing.T) {
			a, topic, _ := topicRemovalFixture(t, "global", "Keep metadata")
			req := inspectedTopicRemoval(t, a, topic.ID)
			interruptTopicRemoval(t, a, phase, func() { _, _ = a.RemoveTopic(req) })
			restarted := NewApp()
			restarted.ctx = t.Context()
			installNoopRuntimeEvents(restarted)
			t.Cleanup(restarted.closeSessionServices)
			state, err := restarted.workspaceRegistry().Load(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.reconcileTopicRemovals(state); err != nil {
				t.Fatal(err)
			}
			page, err := restarted.ListTrashEntries("", "", 50)
			if err != nil || len(page.Items) != 1 || !page.Items[0].CanRestore {
				t.Fatalf("recovery: %+v %v", page, err)
			}
		})
	}
}

func TestTopicRemovalRecoveryDoesNotEraseNewerMetadata(t *testing.T) {
	a, topic, _ := topicRemovalFixture(t, "global", "Keep metadata")
	req := inspectedTopicRemoval(t, a, topic.ID)
	interruptTopicRemoval(t, a, "topic-sessions-before-archive", func() { _, _ = a.RemoveTopic(req) })
	if err := a.RenameTopic(topic.ID, "Newer title"); err != nil {
		t.Fatal(err)
	}
	state, err := a.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.reconcileTopicRemovals(state); err == nil {
		t.Fatal("stale removal resumed after rename")
	}
	_, info := currentTopicInfo(t, a, topic.ID)
	if info.Title != "Newer title" {
		t.Fatal("replay erased newer title")
	}
}

func TestTopicRemovalRejectsAmbiguousCurrentOwner(t *testing.T) {
	a, topic, _ := topicRemovalFixture(t, "global", "Original")
	root := t.TempDir()
	ref, _ := createCurrentRegistrySession(t, a, root, "other-owner", true)
	if err := a.workspaceRegistry().EnsureSessionTopic(t.Context(), ref.SessionID, topic.ID, "Other owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.InspectTopicRemoval(TopicRemovalTarget{TopicID: topic.ID}); err == nil {
		t.Fatal("ambiguous current ownership accepted")
	}
	if _, err := a.desktopSessionService("").Query().Stat(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
}
