package control

import (
	"log/slog"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

// This boundary only scopes late worker output. Durable turn authority lives
// in the current event log, never in a transcript sidecar.
type turnCompletionBoundary struct {
	PreserveUser bool
	StartedAt    time.Time
}

func captureTurnCompletionBoundary(preserveUser bool) turnCompletionBoundary {
	return turnCompletionBoundary{PreserveUser: preserveUser, StartedAt: time.Now().UTC()}
}

func (c *Controller) finishInFlightTurn(startMessages int, boundary turnCompletionBoundary) {
	if ledger := c.turnEventLedger(); ledger != nil && ledger.CurrentStatus() == event.TurnRecoveryRequired {
		c.stripRecoverySealedTurn(startMessages, boundary)
		return
	}
	if _, err := c.snapshotActivityIfChanged(startMessages); err != nil {
		slog.Warn("controller: persist completed turn", "err", err)
	}
}

func (c *Controller) stripRecoverySealedTurn(startMessages int, boundary turnCompletionBoundary) {
	if c == nil || c.executor == nil {
		return
	}
	msgs := c.executor.Session().Snapshot()
	start := startMessages
	if resolved, ok := resolveInterruptedTurnStart(msgs, startMessages, boundary.PreserveUser, boundary.StartedAt, provider.Message{}); ok {
		start = resolved
	}
	start = max(0, min(start, len(msgs)))
	next := append([]provider.Message(nil), msgs[:start]...)
	if boundary.PreserveUser || boundary.StartedAt.IsZero() {
		for _, message := range msgs[start:] {
			if !agent.IsUserAuthoredTurnMessage(message) {
				continue
			}
			message.Content = StripComposePrefixes(message.Content)
			next = append(next, message)
			break
		}
	}
	c.replaceSessionAfterCancel(next)
}
