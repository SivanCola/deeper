package control

import (
	"fmt"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
)

func TestReadPauseKeepsItsTerminalOutcome(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &agent.IncompleteReadError{Reason: "page budget"})
	if turnOutcome(err) != event.TurnOutcomeIncompleteRead {
		t.Fatal("read pause lost its outcome")
	}
}
