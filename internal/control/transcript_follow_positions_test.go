package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/transcript"
	"deepzora/internal/turnevent"
)

func TestTranscriptFollowLocatesActiveUserOutsideDurableTail(t *testing.T) {
	c, service, runtime := newTranscriptBoundaryController(t, testutil.Turn{}, event.Discard)
	if err := runtime.PublishTranscriptFrame(turnevent.Envelope{
		SessionID: runtime.Ref().SessionID, RuntimeEpoch: runtime.StateSnapshot().Epoch,
		TurnID: "active-turn", Kind: "turn_status", Status: event.TurnInProgress,
		Event: eventwire.Event{Kind: "turn_status", Status: string(event.TurnInProgress)},
	}); err != nil {
		t.Fatal(err)
	}
	events := []session.Event{{Kind: "turn/start", Payload: json.RawMessage(`{}`)}}
	for i := range 140 {
		message := provider.Message{ID: fmt.Sprintf("output-%d", i), Role: provider.RoleAssistant, Content: "output"}
		if i == 0 {
			message.ID, message.Role, message.Content = "active-user", provider.RoleUser, "question"
			message.Origin = provider.MessageOriginUser
		}
		body, err := json.Marshal(map[string]any{"message": message})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, session.Event{Kind: "message/complete", Payload: body})
	}
	if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: "active-prefix", TurnID: "active-turn", Events: events}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.PublishTranscriptFrame(turnevent.Envelope{
		SessionID: runtime.Ref().SessionID, RuntimeEpoch: runtime.StateSnapshot().Epoch,
		TurnID: "active-turn", Kind: "text", Status: event.TurnInProgress,
		Event: eventwire.Event{Kind: "text", MessageID: "streaming", Text: "partial"},
	}); err != nil {
		t.Fatal(err)
	}
	response, err := c.TranscriptFollow(t.Context(), transcript.FollowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.TranscriptFollow(context.Background(), transcript.FollowRequest{Subscription: response.Subscription, Close: true})
	if response.History == nil || response.Snapshot == nil || len(response.History.Messages) != 32 {
		t.Fatalf("missing bounded current window: %+v", response)
	}
	for _, message := range response.History.Messages {
		if message.MessageID == "active-user" {
			t.Fatal("fixture user must precede the durable tail")
		}
	}
	position, found := response.RecordPositions["active-user"]
	if _, durable := response.RecordPositions["streaming"]; durable {
		t.Fatal("provisional streaming output acquired an invented durable position")
	}
	if message, visible := transcriptBoundaryMessage(*response.Snapshot, "streaming"); !visible || message.Content != "partial" {
		t.Fatal("current follow lost the provisional streaming prefix")
	}
	location, err := service.Query().LocateMessage(t.Context(), runtime.Ref(), "active-user", response.Snapshot.CoveredThroughSeq)
	if err != nil || !found || location.Status != "ready" || location.Position != position || location.SnapshotSequence != response.History.SnapshotSequence || location.Generation != response.History.Generation {
		t.Fatalf("active user position missing or belongs to another cut: positions=%v location=%+v err=%v", response.RecordPositions, location, err)
	}
	if err := runtime.PublishTranscriptFrame(turnevent.Envelope{
		SessionID: runtime.Ref().SessionID, RuntimeEpoch: runtime.StateSnapshot().Epoch,
		TurnID: "active-turn", Kind: "turn_done", Status: event.TurnCompleted,
		Event: eventwire.Event{Kind: "turn_done"},
	}); err != nil {
		t.Fatal(err)
	}
	settled, err := runtime.Transcript().Snapshot(transcript.PageRequest{})
	if err != nil || settled.TotalRecords > 96 || len(settled.ActiveRecords) != 0 {
		t.Fatalf("settlement retained the obsolete active prefix: total=%d active=%d err=%v", settled.TotalRecords, len(settled.ActiveRecords), err)
	}
}

func TestTranscriptFollowDoesNotAllocateAnUnboundSession(t *testing.T) {
	service, err := session.NewService("test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	executor := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newOwnedTestController(t, Options{Executor: executor, SessionService: service, ExclusiveSession: true})
	response, err := c.TranscriptFollow(t.Context(), transcript.FollowRequest{})
	if !errors.Is(err, ErrTranscriptProjectionUnavailable) || response.Subscription != "" || response.Snapshot != nil {
		t.Fatalf("unbound follow accepted: %+v, %v", response, err)
	}
	page, err := service.Query().List(t.Context(), "", 10)
	if err != nil || len(page.Sessions) != 0 {
		t.Fatalf("follow allocated a session: %+v, %v", page, err)
	}
}
