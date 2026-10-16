package subagentpersist

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func (t *transcript) commit(ctx context.Context, batch session.Batch) (session.Commit, error) {
	if t.closed {
		return session.Commit{}, session.ErrStaleExecution
	}
	prepared, err := t.runtime.Session().PrepareBatchContext(ctx, batch.OperationID, batch)
	if err != nil {
		return session.Commit{}, err
	}
	return t.runtime.CommitPreparedForExecution(t.generation, prepared)
}

func (t *transcript) RecordSessionMessages(ctx context.Context, reason string, messages []provider.Message) error {
	return t.recordMessages(ctx, "message/complete", reason, messages)
}
func (t *transcript) RecordSessionMessageUpsert(ctx context.Context, reason string, message provider.Message) error {
	return t.recordMessages(ctx, "message/upsert", reason, []provider.Message{message})
}
func (t *transcript) recordMessages(ctx context.Context, kind, reason string, messages []provider.Message) error {
	if len(messages) == 0 {
		return ctx.Err()
	}
	events := make([]session.Event, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.ID) == "" {
			return errors.New("subagent message requires a stable id")
		}
		payload, err := json.Marshal(map[string]any{"message": message})
		if err != nil {
			return err
		}
		events = append(events, session.Event{Kind: kind, Payload: payload})
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err = t.commit(ctx, session.Batch{OperationID: fmt.Sprintf("%s:%s:%x", kind, reason, digest), Events: events})
	return err
}

func (t *transcript) RecordSessionModelContext(ctx context.Context, request agent.SessionModelContextCommit) (agent.SessionModelContextCommitResult, error) {
	var result agent.SessionModelContextCommitResult
	if strings.TrimSpace(request.OperationID) == "" {
		return result, errors.New("subagent context commit requires an operation id")
	}
	messages := provider.ModelMessages(request.Messages)
	if messages == nil {
		messages = []provider.Message{}
	}
	payload, err := json.Marshal(map[string]any{"messages": messages, "reason": request.Reason})
	if err != nil {
		return result, err
	}
	events := []session.Event{{Kind: "model/context-replace", Payload: payload}}
	if request.Maintenance != nil {
		metadata, err := session.ContextMaintenanceEvent(*request.Maintenance)
		if err != nil {
			return result, err
		}
		events = append(events, metadata)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	commit, err := t.commit(ctx, session.Batch{OperationID: "model-context-maintenance:" + request.OperationID, Events: events})
	if err != nil {
		return result, err
	}
	result.Accepted = true
	receipt, err := t.runtime.Session().Flush(ctx)
	if err != nil {
		return result, err
	}
	if receipt.DurableSequence < commit.LastSequence() {
		return result, errors.New("subagent context commit was not made durable")
	}
	result.Durable = true
	return result, nil
}
