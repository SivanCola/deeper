package control

import (
	"errors"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/tool"
	"deepzora/internal/transcript"
)

func TestCurrentTranscriptColdReopenKeepsMessagesOnce(t *testing.T) {
	provider := testutil.NewMock("test", testutil.Turn{Text: "first answer"})
	executor := agent.New(provider, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Runner: executor, Executor: executor, Sink: event.Discard})
	if err := c.RunTurn(t.Context(), "first question"); err != nil {
		t.Fatal(err)
	}
	before, err := c.TranscriptSnapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := c.SessionRef()
	service := c.SessionService()
	c.Close()
	<-c.closeFinalized
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	provider = testutil.NewMock("test", testutil.Turn{Text: "second answer"})
	executor = agent.New(provider, tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	reopened := newOwnedTestController(t, Options{Runner: executor, Executor: executor, Sink: event.Discard,
		SessionService: service, SessionCreateService: service, ExclusiveSession: true})
	if _, err := reopened.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	after, err := reopened.TranscriptSnapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Records) != 2 || len(after.Records) != len(before.Records) {
		t.Fatalf("cold reopen changed messages: before=%+v after=%+v", before.Records, after.Records)
	}
	for i, record := range before.Records {
		got := after.Records[i].Message
		if got.MessageID != record.Message.MessageID || got.Content != record.Message.Content || got.Role != record.Message.Role {
			t.Fatalf("cold reopen changed message %d: %+v", i, got)
		}
	}
	if err := reopened.RunTurn(t.Context(), "second question"); err != nil {
		t.Fatal(err)
	}
	latest, err := reopened.TranscriptSnapshot(transcript.PageRequest{})
	if err != nil || len(latest.Records) != 4 || latest.Records[3].Message.Content != "second answer" {
		t.Fatalf("reopened runtime lost or duplicated messages: %+v, %v", latest, err)
	}
}

func TestTranscriptFollowRequiresPersistentRuntime(t *testing.T) {
	c := newOwnedTestController(t, Options{Sink: event.Discard})
	t.Cleanup(c.Close)
	response, err := c.TranscriptFollow(t.Context(), transcript.FollowRequest{})
	if !errors.Is(err, ErrTranscriptProjectionUnavailable) || response.Subscription != "" || response.Snapshot != nil {
		t.Fatalf("unbound follow accepted: %+v, %v", response, err)
	}
}

func TestTranscriptProjectionCommitsBeforePublicationAndAllowsReentry(t *testing.T) {
	var c *Controller
	publications := 0
	c = newOwnedTestController(t, Options{Sink: event.FuncSink(func(e event.Event) {
		if e.Sequence == 0 {
			return
		}
		publications++
		snap, err := c.TranscriptSnapshot(transcript.PageRequest{})
		if err != nil {
			t.Errorf("snapshot during publish: %v", err)
			return
		}
		if snap.CoveredThroughSeq < e.Sequence {
			t.Errorf("published %d before snapshot coverage %d", e.Sequence, snap.CoveredThroughSeq)
		}
		if e.Kind == event.AskRequest {
			if err := c.emitTurnEventChecked(event.Event{Kind: event.PromptAnswered, ItemID: "prompt"}); err != nil {
				t.Error(err)
			}
		}
	})})
	t.Cleanup(c.Close)
	c.SetTurnEventRoutingMetadata("runtime", "submission")
	if _, err := c.turnEventLedger().Begin(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []event.Event{
		{Kind: event.TurnStarted},
		{Kind: event.UserMessage, MessageID: "u", Text: "question"},
		{Kind: event.Reasoning, MessageID: "a", Text: "think"},
		{Kind: event.AskRequest, ItemID: "prompt"},
	} {
		if err := c.emitTurnEventChecked(e); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := c.TranscriptSnapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if publications != 5 || snap.CoveredThroughSeq != 5 || len(snap.Records) != 2 || snap.Records[1].Message.Reasoning != "think" {
		t.Fatalf("publications=%d snapshot=%+v", publications, snap)
	}
	if len(snap.Runtime.PendingEvents) != 0 {
		t.Fatal("answered prompt survived snapshot")
	}
	if snap.Records[0].Message.SubmissionID != "submission" {
		t.Fatal("lost submission identity")
	}
	view, err := c.turnEventLedger().Replay(2)
	if err != nil {
		t.Fatal(err)
	}
	if view.LatestSequence != 5 || len(view.Events) != 3 {
		t.Fatalf("replay=%+v", view)
	}
}
