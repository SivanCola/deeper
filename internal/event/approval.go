package event

import "encoding/json"

// Approval identifies a pending tool-call approval for an ApprovalRequest
// event. ID correlates the request with the controller's Approve(ID, …) reply.
type Approval struct {
	ID      string
	Tool    string
	Subject string
	Reason  string // optional annotation explaining why approval is needed
	// RawInput is the exact structured tool input. ACP permission clients use it
	// together with locations/reason instead of parsing a human title.
	RawInput json.RawMessage
	Fresh    bool // current human decision required; do not offer remembered grants
	// Kind classifies the approval surface: "tool" (default), "plan", or
	// "write_access". Empty means ordinary permission.
	Kind        string
	WriteAccess *WriteAccessApproval
	TurnID      string
	// Generation and PermissionRevision bind a decision to the exact runtime
	// permission snapshot that emitted it. New clients echo both values when
	// resolving; older clients remain fenced by turn/runtime identity.
	Generation         uint64
	PermissionRevision uint64
}

// ApprovalKindWriteAccess is the Approval.Kind value for directory expansion.
const ApprovalKindWriteAccess = "write_access"

// WriteAccessApproval is the backward-compatible structured payload for
// extending writable roots. Slices are never nil on the wire.
type WriteAccessApproval struct {
	Directories              []string `json:"directories"`
	DisplayDirectories       []string `json:"display_directories"`
	Justification            string   `json:"justification,omitempty"`
	BroadHomeAccess          bool     `json:"broad_home_access,omitempty"`
	OrdinaryPermissionNeeded bool     `json:"ordinary_permission_needed,omitempty"`
	PersistAllowed           bool     `json:"persist_allowed,omitempty"`
}

// NormalizeWriteAccessApproval makes list fields non-nil for desktop/JSON.
func NormalizeWriteAccessApproval(w *WriteAccessApproval) *WriteAccessApproval {
	if w == nil {
		return nil
	}
	if w.Directories == nil {
		w.Directories = []string{}
	}
	if w.DisplayDirectories == nil {
		w.DisplayDirectories = []string{}
	}
	return w
}
