package serve

import (
	"errors"

	"deepzora/internal/control"
	"deepzora/internal/provider"
)

func validateSubmitAction(format, action string) error {
	if action != "" && action != control.ProtocolRecoveryAction {
		return errors.New(`unsupported action (supported: "protocol_recovery")`)
	}
	if action != "" && format != "" {
		return errors.New("format is unavailable for recovery actions")
	}
	return nil
}

func submitWithAction(ctrl control.SessionAPI, input, format, action string, recoveryID ...string) {
	if action == control.ProtocolRecoveryAction {
		id := ""
		if len(recoveryID) > 0 {
			id = recoveryID[0]
		}
		if recovery, ok := ctrl.(interface{ SubmitProtocolRecovery(string, string) }); ok {
			recovery.SubmitProtocolRecovery(id, input)
		}
		return
	}

	ctrl.SubmitHTTPFormat(input, format)
}

func finalReadinessHistoryMessage(m provider.Message) ([]historyMessage, bool) {
	if m.LocalOnly && len(m.ProtocolRecovery) > 0 {
		if r, ok := provider.DecodeProtocolRecovery(m.ProtocolRecovery); ok && r.State == "pending" {
			return []historyMessage{{Role: "protocol_recovery", ProtocolRecovery: &provider.ProtocolRecoveryAction{ID: r.ID}}}, true
		}
		return nil, true
	}
	return nil, false
}
