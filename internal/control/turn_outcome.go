package control

import (
	"errors"

	"deepzora/internal/agent"
	"deepzora/internal/event"
)

// turnOutcome maps a finished run's typed pause onto the TurnDone outcome
// string. Empty means an ordinary success or failure.
func turnOutcome(err error) string {
	var readErr *agent.IncompleteReadError
	if errors.As(err, &readErr) {
		return event.TurnOutcomeIncompleteRead
	}

	return ""
}
