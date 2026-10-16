package control

import (
	"fmt"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func TestCurrentRewindUsesCheckpointTurnIdentityAfterReplacement(t *testing.T) {
	service, _, previous := newForkTargetsHarness(t, "rewind-current", testutil.Turn{Text: "first answer"})
	if err := previous.RunTurn(t.Context(), "first prompt"); err != nil {
		t.Fatal(err)
	}
	ref, _ := previous.SessionRef()
	preview, err := previous.PrepareRewind(0, RewindConversation)
	if err != nil {
		t.Fatal(err)
	}
	_, runtime, _ := previous.SessionBinding()
	exec := agent.New(testutil.NewMock("replacement", testutil.Turn{Text: "second answer"}), tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, event.Discard)
	next := newOwnedTestController(t, Options{Runner: exec, Executor: exec, Sink: event.Discard,
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	if store := previous.checkpoints.storeRef(); store != next.checkpoints.storeRef() ||
		preview.WorkspaceToken != fmt.Sprint(store.Barrier().Generation()) {
		t.Fatal("preparing replacement changed the current checkpoint owner or preview")
	}
	if err := ActivateControllerReplacement(previous, next); err != nil {
		t.Fatal(err)
	}
	if err := next.checkpoints.storeRef().ValidatePlanSessionRevision(preview.PlanID, preview.SessionRevision); err == nil {
		t.Fatal("replacement preserved the old preview's approval even with a matching revision")
	}
	if _, err := next.checkpoints.storeRef().CommitRewind(preview.PlanID, nil, nil); err == nil {
		t.Fatal("replacement accepted the previous controller's rewind preview")
	}
	previous.Close()
	<-previous.Closed()
	if err := next.RunTurn(t.Context(), "second prompt"); err != nil {
		t.Fatal(err)
	}
	checkpoints := next.Checkpoints()
	if len(checkpoints) != 2 || checkpoints[0].Turn != 0 || checkpoints[1].Turn != 1 {
		t.Fatalf("replacement checkpoints: %+v", checkpoints)
	}
	before, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	request, err := next.CheckpointForkRequest(1)
	if err != nil || request.Source != ref || request.TurnID != before.Projection.Turns[1].TurnID || !request.BeforeTurn || request.BoundarySequence != before.Projection.Turns[1].StartSequence-1 {
		t.Fatalf("replacement checkpoint request = %+v, %v", request, err)
	}
	createdID, err := next.CreateForkSession(request, "checkpoint child")
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Query().Snapshot(t.Context(), session.SessionRef{HostID: ref.HostID, SessionID: createdID})
	if err != nil || len(created.Projection.Turns) != 1 || created.Projection.Turns[0].TurnID != before.Projection.Turns[0].TurnID {
		t.Fatalf("checkpoint child prefix = %+v, %v", created.Projection.Turns, err)
	}
	plan, err := next.PrepareRewind(checkpoints[1].Turn, RewindConversation)
	if err != nil {
		t.Fatal(err)
	}
	result, err := next.CommitRewind(plan.PlanID)
	if err != nil || !result.OK || !result.ConversationForked {
		t.Fatalf("rewind: %+v, %v", result, err)
	}
	child := session.SessionRef{HostID: ref.HostID, SessionID: result.Branch}
	snapshot, err := service.Query().Snapshot(t.Context(), child)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projection.Turns) != 1 || snapshot.Projection.Turns[0].TurnID != before.Projection.Turns[0].TurnID {
		t.Fatal("replacement checkpoint rewound the first turn instead of the second turn")
	}
	if strings.Contains(requestMessagesText(snapshot.Projection.Messages), "second prompt") {
		t.Fatal("rewind retained target turn")
	}
	if got, _ := next.SessionRef(); got != ref {
		t.Fatal("detached rewind switched parent")
	}
	after, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil || after.EventSequence != before.EventSequence {
		t.Fatalf("rewind altered parent: %v", err)
	}
}
