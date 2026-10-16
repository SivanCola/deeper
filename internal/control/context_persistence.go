package control

import (
	"context"
	"crypto/sha256"
	"fmt"

	"deepzora/internal/agent"
	"deepzora/internal/session"
)

func (c *Controller) SessionContextMaintenance() *agent.SessionContextMaintenance {
	if store := c.sessionEventStore(); store != nil {
		return store.StateSnapshot().Projection.ContextMaintenance
	}
	return nil
}

func (c *Controller) RecordSessionContextMaintenance(ctx context.Context, state agent.SessionContextMaintenance) (agent.SessionModelContextCommitResult, error) {
	var result agent.SessionModelContextCommitResult
	store := c.sessionEventStore()
	if !c.sessionEventCommitAllowed() || store == nil {
		return result, session.ErrStaleExecution
	}
	event, err := session.ContextMaintenanceEvent(state)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(event.Payload)
	c.turnEvents.commitMu.Lock()
	if !c.messageCommitAllowedLocked(ctx, store) {
		c.turnEvents.commitMu.Unlock()
		return result, session.ErrStaleExecution
	}
	commit, err := c.appendSessionBatch(ctx, store, session.Batch{
		OperationID: fmt.Sprintf("context-maintenance:%x", digest), Events: []session.Event{event},
	})
	c.turnEvents.commitMu.Unlock()
	if err != nil {
		return result, err
	}
	result.Accepted = true
	receipt, err := store.Flush(ctx)
	if err != nil {
		return result, err
	}
	result.Durable = receipt.DurableSequence >= commit.LastSequence()
	if !result.Durable {
		return result, fmt.Errorf("context maintenance is not durable")
	}
	return result, nil
}
