package control

import (
	"testing"
	"testing/synctest"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
	"deepzora/internal/transcript"
)

func TestTranscriptRetainsProtocolRecoveryAfterProviderFailure(t *testing.T) {
	// The deadline guards a missing terminal event, not host filesystem speed.
	// Keep the real asynchronous controller and persistence assertions while
	// advancing its clock only when the test's goroutines are durably blocked.
	synctest.Test(t, testTranscriptRetainsProtocolRecoveryAfterProviderFailure)
}

func testTranscriptRetainsProtocolRecoveryAfterProviderFailure(t *testing.T) {
	p := &manualProtocolProvider{MockProvider: testutil.NewMock("strict", testutil.ErrorTurn(&provider.APIError{Status: 400, Body: `{"model":"deepseek"}`}))}
	session := agent.NewSession("system")
	session.Add(provider.Message{Role: provider.RoleAssistant, Content: "earlier", ReasoningContent: "proof"})
	a := agent.New(p, tool.NewRegistry(), session, agent.Options{}, event.Discard)
	done := make(chan event.Event, 1)
	c := newCurrentOwnedTestController(t, Options{Runner: a, Executor: a, Sink: event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			done <- e
		}
	})})
	defer c.Close()
	c.Send("next")
	select {
	case e := <-done:
		if e.Err == nil || e.ProtocolRecovery == nil {
			t.Fatal("expected recoverable provider failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not settle")
	}
	action := a.PendingProtocolRecovery()
	if action == nil {
		t.Fatal("missing recovery token")
	}
	check := func(rows []transcript.Message) {
		t.Helper()
		for _, row := range rows {
			if row.ProtocolRecovery != nil && row.ProtocolRecovery.ID == action.ID && row.Pending {
				return
			}
		}
		t.Fatalf("authoritative display lost pending recovery %q: %+v", action.ID, rows)
	}
	snap, err := c.TranscriptSnapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var rows []transcript.Message
	for _, r := range snap.Records {
		rows = append(rows, r.Message)
	}
	check(rows)
	ref, ok := c.SessionRef()
	if !ok {
		t.Fatal("missing current session identity")
	}
	durable, err := c.SessionService().Query().History(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	check(transcript.History(durable, transcript.HistoryOptions{}))
}
