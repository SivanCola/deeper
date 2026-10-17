package agent

import (
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

// admissionGatedDelegations are the delegation tools whose admission is audited.
// The model chooses delegation; this audit never blocks a call.
var admissionGatedDelegations = map[string]bool{
	"research": true,
	"explore":  true,
	"task":     true,
	"fleet":    true,
}

// observeDelegationAdmission records a shadow admission verdict for every
// gated delegation call in the batch without classifying prompt text.
func (a *Agent) observeDelegationAdmission(calls []provider.ToolCall) {
	for _, call := range calls {
		if !admissionGatedDelegations[call.Name] {
			continue
		}
		event.RecordDelegationAdmission(a.svc.sink, event.DelegationAdmissionAudit{
			Tool: call.Name, Verdict: "allow", Reason: "model_decides",
		})
	}
}
