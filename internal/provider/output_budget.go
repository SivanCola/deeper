package provider

// SharedWindowInputPolicy describes adapter-specific text replayed into a
// shared context window in addition to the fields common to all transports.
type SharedWindowInputPolicy struct {
	ReplaysOrdinaryReasoning bool
	ReplaysResponsesItems    bool
}

// SharedWindowInputPolicyProvider reports adapter-specific replay behavior so
// admission estimates can follow the actual wire without changing its bytes.
type SharedWindowInputPolicyProvider interface {
	SharedWindowInputPolicy() SharedWindowInputPolicy
}
