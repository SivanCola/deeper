package control

import (
	"testing"

	"deepzora/internal/agent/testutil"
	"deepzora/internal/event"
)

func TestTurnDoneIdentifiesCurrentDurableTurn(t *testing.T) {
	done := make(chan event.Event, 4)
	c, service, runtime := exclusiveTestController(t, event.FuncSink(func(e event.Event) {
		if e.Kind == event.TurnDone {
			done <- e
		}
	}), testutil.Turn{Text: "first answer"}, testutil.Turn{Text: "second answer"})
	previous := ""
	for _, input := range []string{"first", "second"} {
		if err := c.RunTurn(t.Context(), input); err != nil {
			t.Fatal(err)
		}
		terminal := waitTurnDoneEvent(t, done)
		if err := c.Snapshot(); err != nil {
			t.Fatal(err)
		}
		snapshot, err := service.Query().Snapshot(t.Context(), runtime.Ref())
		if err != nil || len(snapshot.Projection.Turns) == 0 {
			t.Fatalf("durable turn missing: %+v, %v", snapshot, err)
		}
		turn := snapshot.Projection.Turns[len(snapshot.Projection.Turns)-1]
		if terminal.TurnID == "" || terminal.TurnID == previous || terminal.TurnID != turn.TurnID || turn.EndSequence == 0 || terminal.Status != event.TurnCompleted {
			t.Fatalf("terminal does not identify its current committed turn: %+v, %+v", terminal, turn)
		}
		previous = terminal.TurnID
	}
}
