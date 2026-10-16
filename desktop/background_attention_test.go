package main

import (
	"context"
	"testing"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/event"
)

func TestDetachedAskPublishesGlobalPendingInteraction(t *testing.T) {
	isolateDesktopUserDirs(t)
	a := NewApp()
	a.ctx = context.Background()
	pending := make(chan RuntimeSessionState, 1)
	a.runtimeEvents.emit = func(_ context.Context, name string, payload ...any) {
		if name != "runtime-state:changed" {
			return
		}
		for _, session := range payload[0].(RuntimeStateProjection).Sessions {
			if !session.Open && len(session.State.Interactions) > 0 {
				select {
				case pending <- session:
				default:
				}
			}
		}
	}
	sink := &tabEventSink{tabID: "A", app: a, ctx: a.ctx}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	ctrl := newCurrentRebuildController(t, a, control.Options{Runner: runner, Sink: sink})
	ref, _ := ctrl.SessionRef()
	path := sessionRoute(ref.SessionID)
	_, runtime, _ := ctrl.SessionBinding()
	ctrl.SetTurnEventRoutingMetadata(runtime.StateSnapshot().Epoch, "")
	defer ctrl.Close()
	tab := &WorkspaceTab{ID: "A", Scope: "global", TopicID: "topic-a", TopicTitle: "Conversation A", SessionID: ref.SessionID, Ctrl: ctrl, sink: sink, Ready: true}
	a.tabs[tab.ID] = tab
	a.activeTabID = tab.ID
	ctrl.Submit("fixture turn")
	<-runner.started
	if !a.detachRuntimeForReplacement(tab) {
		t.Fatal("could not detach running A")
	}
	a.mu.Lock()
	delete(a.tabs, "A")
	a.tabs["B"] = &WorkspaceTab{ID: "B"}
	a.activeTabID = "B"
	a.mu.Unlock()
	if sink.context() != nil {
		t.Fatal("detached transcript must stay disconnected")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ctrl.Ask(ctx, []event.AskQuestion{{ID: "choice", Prompt: "Choose"}})
	}()
	defer func() {
		cancel()
		<-done
		close(runner.release)
		waitNotRunning(t, ctrl)
		a.mu.RLock()
		detached := a.detachedSessions[sessionRuntimeKey(path)]
		a.mu.RUnlock()
		a.quiesceTabAutosave(detached)
	}()
	select {
	case session := <-pending:
		prompt := session.State.Interactions[0]
		if session.TabID == "B" || session.SessionID != ref.SessionID || prompt.Kind != "ask" || prompt.RequestID == "" || prompt.TurnID == "" || prompt.RuntimeEpoch == "" {
			t.Fatalf("wrong background prompt identity: %+v", session)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background Ask was not pushed without reopening A")
	}
	a.mu.RLock()
	active := a.activeTabID
	a.mu.RUnlock()
	if active != "B" {
		t.Fatalf("background Ask changed active tab to %q", active)
	}
}
