package eventwire

import "deepzora/internal/event"

// Approval is the JSON form of an event.Approval.
type Approval struct {
	ID                 string               `json:"id"`
	Tool               string               `json:"tool"`
	Subject            string               `json:"subject" externalizable:"true"`
	Reason             string               `json:"reason,omitempty" externalizable:"true"`
	Fresh              bool                 `json:"fresh,omitempty"`
	Kind               string               `json:"kind,omitempty"` // tool | plan | write_access
	WriteAccess        *WriteAccessApproval `json:"write_access,omitempty"`
	TurnID             string               `json:"turnId,omitempty"`
	Generation         uint64               `json:"generation,omitempty"`
	PermissionRevision uint64               `json:"permissionRevision,omitempty"`
}

type WriteAccessApproval struct {
	Directories              []string `json:"directories"`
	DisplayDirectories       []string `json:"display_directories"`
	Justification            string   `json:"justification,omitempty"`
	BroadHomeAccess          bool     `json:"broad_home_access,omitempty"`
	OrdinaryPermissionNeeded bool     `json:"ordinary_permission_needed,omitempty"`
	PersistAllowed           bool     `json:"persist_allowed,omitempty"`
}

func toWireApproval(a event.Approval) *Approval {
	w := &Approval{ID: a.ID, Tool: a.Tool, Subject: a.Subject, Reason: a.Reason, Fresh: a.Fresh, Kind: a.Kind, TurnID: a.TurnID, Generation: a.Generation, PermissionRevision: a.PermissionRevision}
	if wa := event.NormalizeWriteAccessApproval(a.WriteAccess); wa != nil {
		w.WriteAccess = &WriteAccessApproval{
			Directories: append([]string{}, wa.Directories...), DisplayDirectories: append([]string{}, wa.DisplayDirectories...),
			Justification: wa.Justification, BroadHomeAccess: wa.BroadHomeAccess,
			OrdinaryPermissionNeeded: wa.OrdinaryPermissionNeeded, PersistAllowed: wa.PersistAllowed,
		}
	}
	return w
}
