package agent

import (
	"context"
	"fmt"
	"slices"

	"deepzora/internal/provider"
)

// Caller holds toolMetadataMu across the edit and this commit. Parallel tool
// starts must not publish an older whole-message copy over a newer call record.
func (a *Agent) recordToolMetadata(ctx context.Context, callID, reason string) error {
	recorder, ok := a.svc.sessionCheckpointer.(SessionMessageMutationRecorder)
	if !ok {
		return nil
	}
	var message provider.Message
	found := false
	s := a.sess.conversation
	s.mu.Lock()
	for _, candidate := range slices.Backward(s.Messages) {
		for _, call := range candidate.ToolCalls {
			if call.ID == callID {
				message, found = candidate, true
				break
			}
		}
		if found {
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return fmt.Errorf("tool metadata has no message: %s", callID)
	}
	return recorder.RecordSessionMessageUpsert(ctx, reason, message)
}

func (a *Agent) updateToolRecoveryRecord(ctx context.Context, callID string, record provider.ToolCallRecord) (bool, error) {
	a.sess.toolMetadataMu.Lock()
	defer a.sess.toolMetadataMu.Unlock()
	if !a.sess.conversation.setToolRecoveryRecord(callID, record) {
		return false, nil
	}
	return true, a.recordToolMetadata(ctx, callID, "tool-recovery")
}
