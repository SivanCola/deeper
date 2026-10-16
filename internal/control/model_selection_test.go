package control

import (
	"context"
	"errors"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestControllerModelSelectionPersistsAndBranches(t *testing.T) {
	dir := t.TempDir()
	service, err := session.NewService("test", session.NewFilesystemPersistence(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	p := &recordingProvider{streams: [][]provider.Chunk{{{Type: provider.ChunkText, Text: "saved answer"}, {Type: provider.ChunkDone}}}}
	exec := agent.New(p, nil, agent.NewSession("fixture system"), agent.Options{}, event.Discard)
	ctrl := newOwnedTestController(t, Options{Runner: exec, Executor: exec, Sink: event.Discard, SessionDir: dir,
		SessionService: service, ExclusiveSession: true, ModelRef: "go/model", ModelIdentity: "accepted"})
	parent, err := ctrl.BindFreshSession(t.Context(), "original")
	if err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(t.Context(), "keep this"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Snapshot(); err != nil {
		t.Fatal(err)
	}
	branch, err := ctrl.Branch("copy")
	if err != nil {
		t.Fatal(err)
	}
	child, bound := ctrl.SessionRef()
	if !bound || child.SessionID != branch || child == parent {
		t.Fatal("branch did not publish a new current identity")
	}
	ctrl.Close()
	<-ctrl.Closed()
	for _, ref := range []session.SessionRef{parent, child} {
		if err := service.Close(t.Context(), ref); err != nil {
			t.Fatal(err)
		}
		state, err := service.Query().Snapshot(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}
		if state.Projection.ModelRef != "go/model" || state.Projection.ModelIdentity != "accepted" {
			t.Fatalf("session %s lost model selection: %q / %q", ref.SessionID, state.Projection.ModelRef, state.Projection.ModelIdentity)
		}
		if len(state.Projection.Turns) != 1 || len(state.Projection.Messages) < 2 {
			t.Fatal("branch lost the completed conversation")
		}
	}
}

func TestControllerRejectsHistoricalBranchBeforeTransition(t *testing.T) {
	blocked := errors.New("SAVED_MODEL_UNAVAILABLE")
	validated := false
	exec := agent.New(nil, nil, agent.NewSession("fixture"), agent.Options{}, event.Discard)
	ctrl, service := bindCurrentBranchController(t, Options{Executor: exec, ResolveSessionModel: func(model, identity string) (string, error) {
		if model != "go/model" || identity != "old" {
			t.Fatal("branch did not pass the saved selection to the validator")
		}
		validated = true
		return "", blocked
	}})
	before, _ := ctrl.SessionRef()
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := sessionConfigEvent("go/model", "old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Session().AppendBatch(t.Context(), "saved-selection", []session.Event{config}); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.SwitchBranch(target.Ref().SessionID); !errors.Is(err, blocked) {
		t.Fatalf("branch was not rejected: %v", err)
	}
	if !validated {
		t.Fatal("saved model selection was not validated")
	}
	if got, _ := ctrl.SessionRef(); got != before {
		t.Fatal("failed validation changed the active session")
	}
	if _, live := service.Runtime(target.Ref()); live {
		t.Fatal("model validation acquired the target writer")
	}
}
