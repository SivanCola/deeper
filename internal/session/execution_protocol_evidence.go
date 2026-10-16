package session

import (
	"encoding/json"

	"deepzora/internal/provider"
)

// Only the latest local protocol incident can authorize a manual retry. Keep
// its exact receipt independently of the provider workset, which strips local
// fields. This bounded projection retains neither message bodies nor past
// incidents; the event log remains their authoritative history.
type protocolRecoveryEvidence struct {
	MessageID string
	Record    json.RawMessage
}

func noteProtocolRecovery(projection *Projection, message provider.Message) {
	if message.LocalOnly && len(message.ProtocolRecovery) > 0 {
		projection.ProtocolRecovery = &protocolRecoveryEvidence{MessageID: message.ID, Record: message.ProtocolRecovery}
	} else if current := projection.ProtocolRecovery; current != nil && current.MessageID == message.ID {
		projection.ProtocolRecovery = nil
	}
}

func cloneProtocolRecovery(record *protocolRecoveryEvidence) *protocolRecoveryEvidence {
	if record == nil {
		return nil
	}
	return &protocolRecoveryEvidence{MessageID: record.MessageID, Record: cloneRaw(record.Record)}
}

func restoreProtocolRecovery(projection *Projection) {
	if record := projection.ProtocolRecovery; record != nil {
		projection.ModelMessages = append(projection.ModelMessages, provider.Message{
			ID: record.MessageID, Role: provider.RoleTool, LocalOnly: true,
			ProtocolRecovery: cloneRaw(record.Record),
		})
	}
}
