// Activation: whether a fact's body rides the session-context background
// snapshot (pinned) or stays retrieval-only (relevant).
package memory

import "strings"

// Activation controls how a fact reaches the model, orthogonal to Scope:
// scope says where a fact may be used, activation says whether its body rides
// the current session-context snapshot (pinned) or is retrieval-only (relevant).
type Activation string

const (
	ActivationRelevant Activation = "relevant" // retrieval-only: index + recall
	ActivationPinned   Activation = "pinned"   // body loads into session-context
)

// NormalizeActivation validates a persisted or requested activation. Empty and
// unknown values return "" (unset); new facts default to relevant.
func NormalizeActivation(s string) Activation {
	switch Activation(strings.ToLower(strings.TrimSpace(s))) {
	case ActivationRelevant:
		return ActivationRelevant
	case ActivationPinned:
		return ActivationPinned
	}
	return ""
}

// ResolveActivation requires an explicit pinned choice. Unset facts are relevant.
func ResolveActivation(m Memory) Activation {
	if a := NormalizeActivation(string(m.Activation)); a != "" {
		return a
	}
	return ActivationRelevant
}
