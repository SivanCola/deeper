package subagentpersist

import (
	"context"
	"crypto/sha256"
	"fmt"

	"deepzora/internal/agent"
	"deepzora/internal/session"
)

func (t *transcript) SessionContextMaintenance() *agent.SessionContextMaintenance {
	return t.runtime.Session().StateSnapshot().Projection.ContextMaintenance
}

func (t *transcript) RecordSessionContextMaintenance(ctx context.Context, state agent.SessionContextMaintenance) (agent.SessionModelContextCommitResult, error) {
	var result agent.SessionModelContextCommitResult
	event, err := session.ContextMaintenanceEvent(state)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(event.Payload)
	t.mu.Lock()
	defer t.mu.Unlock()
	commit, err := t.commit(ctx, session.Batch{OperationID: fmt.Sprintf("context-maintenance:%x", digest), Events: []session.Event{event}})
	if err != nil {
		return result, err
	}
	result.Accepted = true
	receipt, err := t.runtime.Session().Flush(ctx)
	if err != nil {
		return result, err
	}
	result.Durable = receipt.DurableSequence >= commit.LastSequence()
	if !result.Durable {
		return result, fmt.Errorf("context maintenance is not durable")
	}
	return result, nil
}
