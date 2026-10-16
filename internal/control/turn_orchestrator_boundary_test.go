package control

import (
	"deepzora/internal/agent/testutil"
	"testing"
)

func TestTurnOrchestratorCheckpointBoundaryPrecedesUserMessage(t *testing.T) {
	service, _, c := newForkTargetsHarness(t, "checkpoint-boundary", testutil.Turn{Text: "answer"})
	if err := c.RunTurn(t.Context(), "write the test"); err != nil {
		t.Fatal(err)
	}
	parent, _ := c.SessionRef()
	if !c.CheckpointHasBoundary(0) {
		t.Fatal("checkpoint boundary is unavailable")
	}
	snapshot, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Projection.Messages) != 3 || snapshot.Projection.Messages[1].Content != "write the test" {
		t.Fatalf("current transcript = %+v", snapshot.Projection.Messages)
	}
	request, err := c.CheckpointForkRequest(0)
	if err != nil || request.TurnID != snapshot.Projection.Turns[0].TurnID || request.BoundarySequence >= snapshot.Projection.Turns[0].StartSequence {
		t.Fatalf("checkpoint cut = %+v, %v", request, err)
	}
	if err := c.executeRewindCommand(0, RewindConversation); err != nil {
		t.Fatal(err)
	}
	child, _ := c.SessionRef()
	if child == parent {
		t.Fatal("rewind did not publish a current child identity")
	}
	if got := c.History(); len(got) != 1 {
		t.Fatalf("rewind retained user message: %+v", got)
	}
	after, err := service.Query().Snapshot(t.Context(), parent)
	if err != nil || after.EventSequence != snapshot.EventSequence || len(after.Projection.Messages) != 3 {
		t.Fatalf("parent changed: %v", err)
	}
}
