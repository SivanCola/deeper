package control

import (
	"reflect"
	"testing"

	"deepzora/internal/agent/testutil"
	"deepzora/internal/session"
)

func TestForkAtTurnCreatesIndependentSession(t *testing.T) {
	service, _, c := newForkTargetsHarness(t, "fork-parent", testutil.Turn{Text: "root answer"}, testutil.Turn{Text: "second answer"})
	for _, prompt := range []string{"root prompt", "second prompt"} {
		if err := c.RunTurn(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	parent, _ := c.SessionRef()
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	childID, err := c.ForkNamed(1, "named fork")
	if err != nil {
		t.Fatal(err)
	}
	child, ok := c.SessionRef()
	if !ok || child.SessionID != childID || child == parent {
		t.Fatalf("fork identity = %+v", child)
	}
	snapshot, err := service.Query().Snapshot(t.Context(), child)
	if err != nil || len(snapshot.Projection.Turns) != 1 || snapshot.Projection.Turns[0].TurnID != before.Projection.Turns[0].TurnID {
		t.Fatalf("child prefix: %+v, %v", snapshot.Projection.Turns, err)
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatalf("fork changed parent: %v", err)
	}
	info, err := service.Query().Stat(t.Context(), child)
	if err != nil || info.ParentSessionID != parent.SessionID || info.Title != "named fork" {
		t.Fatalf("fork metadata = %+v, %v", info, err)
	}
}

func TestCommitRewindInPlaceSwitchesToIndependentCurrentSession(t *testing.T) {
	service, _, c := newForkTargetsHarness(t, "rewind-parent", testutil.Turn{Text: "root answer"}, testutil.Turn{Text: "second answer"})
	for _, prompt := range []string{"root prompt", "second prompt"} {
		if err := c.RunTurn(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
	}
	parent, _ := c.SessionRef()
	before, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := c.Checkpoints()
	if len(checkpoints) != 2 {
		t.Fatalf("checkpoints = %+v", checkpoints)
	}
	plan, err := c.PrepareRewind(checkpoints[1].Turn, RewindConversation)
	if err != nil || !plan.CanConversation {
		t.Fatalf("preview = %+v, %v", plan, err)
	}
	result, err := c.CommitRewindInPlace(plan.PlanID)
	if err != nil || !result.OK || !result.ConversationForked {
		t.Fatalf("commit = %+v, %v", result, err)
	}
	child := session.SessionRef{HostID: parent.HostID, SessionID: result.Branch}
	if got, _ := c.SessionRef(); got != child || child == parent {
		t.Fatalf("rewind binding = %+v", got)
	}
	snapshot, err := service.Query().Snapshot(t.Context(), child)
	if err != nil || len(snapshot.Projection.Turns) != 1 || snapshot.Projection.Turns[0].TurnID != before.Projection.Turns[0].TurnID {
		t.Fatalf("rewind prefix = %+v, %v", snapshot.Projection.Turns, err)
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatalf("rewind changed parent: %v", err)
	}
}
