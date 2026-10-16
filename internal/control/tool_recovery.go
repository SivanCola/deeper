package control

import (
	"crypto/sha256"
	"deepzora/internal/agent"
	"encoding/hex"
	"encoding/json"

	"deepzora/internal/provider"
)

type ToolRecoverySnapshot struct {
	Silent       bool                         `json:"silent"`
	Statistics   agent.ToolRecoveryStatistics `json:"statistics"`
	SessionPath  string                       `json:"sessionPath"`
	RuntimeEpoch string                       `json:"runtimeEpoch"`
	Revision     string                       `json:"revision"`
	Calls        []provider.ToolCallRecord    `json:"calls"`
}

func (c *Controller) ToolRecoverySnapshot() ToolRecoverySnapshot {
	view := ToolRecoverySnapshot{SessionPath: c.SessionRoute(), RuntimeEpoch: c.RuntimeStateSnapshot().RuntimeEpoch, Calls: []provider.ToolCallRecord{}}
	if c.executor != nil {
		view.Calls = c.executor.PendingToolRecovery()
		view.Statistics = c.executor.ToolRecoveryStatistics()
		view.Silent = c.executor.SilentToolRecovery()
	}
	// Raw parameters stay in the session. Frontends get immutable identities
	// and inspection facts, never an executable payload supplied by the UI.
	for i := range view.Calls {
		view.Calls[i].Arguments = nil
	}
	bytes, _ := json.Marshal(view)
	sum := sha256.Sum256(bytes)
	view.Revision = hex.EncodeToString(sum[:])
	return view
}
