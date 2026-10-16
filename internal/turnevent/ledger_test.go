package turnevent

import (
	"deepzora/internal/event"
	"testing"
)

func TestLedgerOrdersMonotonicEventsAndExactlyOneTerminal(t *testing.T) {
	l := NewMemory("session")
	l.SetRoutingMetadata("epoch-1", "submission-1")
	turnID, err := l.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	l.SetTranscriptSnapshot(7, "digest-1")
	started, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress)
	if err != nil || !ok {
		t.Fatalf("append started: ok=%v err=%v", ok, err)
	}
	done, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted)
	if err != nil || !ok {
		t.Fatalf("append done: ok=%v err=%v", ok, err)
	}
	if started.TurnID != turnID || done.TurnID != turnID || started.Sequence != 1 || done.Sequence != 2 {
		t.Fatalf("stamps = started(%q,%d) done(%q,%d), want turn %q seq 1,2", started.TurnID, started.Sequence, done.TurnID, done.Sequence, turnID)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnFailed); err != nil || ok {
		t.Fatalf("second terminal: ok=%v err=%v, want compare-and-append rejection", ok, err)
	}
	recs, err := l.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(recs) != 2 || recs[0].Sequence != 1 || recs[1].Sequence != 2 {
		t.Fatalf("records = %#v, want two monotonic events", recs)
	}
	if recs[1].RuntimeEpoch != "epoch-1" || recs[1].SubmissionID != "submission-1" || recs[1].TranscriptRevision != 7 || recs[1].TranscriptDigest != "digest-1" {
		t.Fatalf("terminal metadata = %+v, want routing and transcript identity", recs[1])
	}
	if latest, replayAfter := l.ProjectionCursor(); latest != 2 || replayAfter != 2 {
		t.Fatalf("terminal cursor = (%d,%d), want (2,2)", latest, replayAfter)
	}
}

func TestLedgerProjectionCursorReplaysOnlyActiveTurn(t *testing.T) {
	l := NewMemory("session")
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin first: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("complete first: ok=%v err=%v", ok, err)
	}
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin second: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged}, event.TurnQueued); err != nil || !ok {
		t.Fatalf("queue second: ok=%v err=%v", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("start second: ok=%v err=%v", ok, err)
	}
	if latest, replayAfter := l.ProjectionCursor(); latest != 3 || replayAfter != 1 {
		t.Fatalf("active cursor = (%d,%d), want (3,1)", latest, replayAfter)
	}
}

func TestLedgerSubmissionReceiptSurvivesCompletionAndIsOneShot(t *testing.T) {
	l := NewMemory("session")
	l.SetRoutingMetadata("epoch-1", "submission-1")
	first, err := l.Begin()
	if err != nil {
		t.Fatalf("Begin first: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("complete first: ok=%v err=%v", ok, err)
	}
	if got := l.TurnIDForSubmission("submission-1"); got != first {
		t.Fatalf("receipt after completion = %q, want %q", got, first)
	}
	second, err := l.Begin()
	if err != nil {
		t.Fatalf("Begin automatic follow-up: %v", err)
	}
	if second == first {
		t.Fatal("follow-up reused the completed turn id")
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("start follow-up: ok=%v err=%v", ok, err)
	}
	records, err := l.EventsAfter(0)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	last := records[len(records)-1]
	if last.SubmissionID != "" || last.Event.SubmissionID != "" {
		t.Fatalf("automatic follow-up inherited the previous submission: %+v", last)
	}
	if last.RuntimeEpoch != "epoch-1" || last.Event.RuntimeEpoch != "epoch-1" {
		t.Fatalf("automatic follow-up lost its owning runtime epoch: %+v", last)
	}
	if got := l.TurnIDForSubmission("submission-1"); got != first {
		t.Fatalf("receipt after replacement = %q, want original %q", got, first)
	}
}

func TestLedgerRejectsStatusRegressionAndKeepsCancellationSticky(t *testing.T) {
	l := NewMemory("session")
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("start: ok=%v err=%v", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged}, event.TurnQueued); err == nil || ok {
		t.Fatalf("status regression: ok=%v err=%v, want rejection", ok, err)
	}
	if _, ok, err := l.Append(event.Event{Kind: event.TurnStatusChanged}, event.TurnCancelling); err != nil || !ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	stamped, ok, err := l.Append(event.Event{Kind: event.PromptAnswered}, event.TurnInProgress)
	if err != nil || !ok || stamped.Status != event.TurnCancelling || l.CurrentStatus() != event.TurnCancelling {
		t.Fatalf("late prompt answer = (%+v,%v,%v), current=%q, want sticky cancelling", stamped, ok, err, l.CurrentStatus())
	}
}

func TestLedgerReplayPaginationAndOutOfRangeCursor(t *testing.T) {
	l := NewMemory("session")
	if _, err := l.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for i := range replayMaxEvents + 25 {
		if _, ok, err := l.Append(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "tool", Name: "bash", Output: "tick"}}, event.TurnInProgress); err != nil || !ok {
			t.Fatalf("Append %d: ok=%v err=%v", i, ok, err)
		}
	}
	first, err := l.Replay(0)
	if err != nil || len(first.Events) != replayMaxEvents || !first.HasMore || first.NextAfterSequence != replayMaxEvents {
		t.Fatalf("first page = events:%d next:%d more:%v err=%v", len(first.Events), first.NextAfterSequence, first.HasMore, err)
	}
	second, err := l.Replay(first.NextAfterSequence)
	if err != nil || len(second.Events) != 25 || second.HasMore {
		t.Fatalf("second page = events:%d more:%v err=%v", len(second.Events), second.HasMore, err)
	}
	outOfRange, err := l.Replay(second.LatestSequence + 1)
	if err != nil || !outOfRange.ResetRequired {
		t.Fatalf("out-of-range replay = %+v err=%v", outOfRange, err)
	}
}

func TestLedgerReplayEmptyArray(t *testing.T) {
	l := NewMemory("session")
	records, err := l.EventsAfter(99)
	if err != nil || records == nil || len(records) != 0 {
		t.Fatalf("empty replay = %#v, %v", records, err)
	}
}
