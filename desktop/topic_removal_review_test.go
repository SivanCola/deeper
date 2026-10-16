package main

import (
	"testing"

	"deepzora/desktop/internal/workspacestate"
)

func TestTopicRemovalCanonicalConfirmationFencedAtArchiveAdmission(t *testing.T) {
	for _, phase := range []string{"topic-sessions-before-archive", "before-archive-commit"} {
		t.Run(phase, func(t *testing.T) { testTopicRemovalCanonicalConfirmation(t, phase) })
	}
}

func testTopicRemovalCanonicalConfirmation(t *testing.T, checkpoint string) {
	a, _, _ := topicRemovalFixture(t, "global", "")
	tab, err := a.ensureBlankTab("global", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.CloseTab(tab.ID); err != nil {
			t.Error(err)
		}
	})
	request := inspectedTopicRemoval(t, a, tab.TopicID)
	a.lifecycleCheckpointHook = func(phase string) {
		if phase != checkpoint {
			return
		}
		title := "Edited after confirmation"
		if err := a.workspaceRegistry().UpdatePresentation(t.Context(), []string{tab.SessionID}, &title, nil); err != nil {
			t.Fatal(err)
		}
	}
	result, err := a.RemoveTopic(request)
	a.lifecycleCheckpointHook = nil
	if err != nil || result.Committed || result.ErrorCode != "state_conflict" {
		t.Fatalf("archived changed confirmation: %+v %v", result, err)
	}
	state, err := a.workspaceRegistry().Load(t.Context())
	if err != nil || state.SessionStates[tab.SessionID].Lifecycle != workspacestate.Active {
		t.Fatalf("changed session not active: %v", err)
	}
	// The archive child is durable by this point. Generic startup replay must
	// enforce the same confirmation fence instead of bypassing the topic RPC.
	if err := a.recoverDesktopSessionOperations(t.Context()); err == nil {
		t.Fatal("replay accepted stale confirmation")
	}
	state, err = a.workspaceRegistry().Load(t.Context())
	if err != nil || state.SessionStates[tab.SessionID].Lifecycle != workspacestate.Active {
		t.Fatalf("replay archived the changed session: %v", err)
	}
}
