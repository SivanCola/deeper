package workspacestate

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Organization is owned by the registry transaction, including source adoption.
// Keys are host-qualified canonical or source identities, never topic IDs.
type Organization struct {
	Revision           uint64              `json:"revision"`
	ManualOrderEnabled bool                `json:"manualOrderEnabled"`
	Order              []string            `json:"order"`
	Groups             []OrganizationGroup `json:"groups"`
	Imported           map[string]bool     `json:"imported"`
	extra              map[string]json.RawMessage
}
type OrganizationGroup struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Members []string `json:"members"`
	extra   map[string]json.RawMessage
}

func (o *Organization) UnmarshalJSON(b []byte) error {
	type plain Organization
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*o = Organization(p)
	var err error
	o.extra, err = unknownFields(b, "revision", "manualOrderEnabled", "order", "groups", "migrationVersion", "imported")
	return err
}
func (o Organization) MarshalJSON() ([]byte, error) {
	type plain Organization
	b, e := json.Marshal(plain(o))
	if e != nil {
		return nil, e
	}
	return mergeUnknown(b, o.extra)
}
func (o *OrganizationGroup) UnmarshalJSON(b []byte) error {
	type plain OrganizationGroup
	var p plain
	if e := json.Unmarshal(b, &p); e != nil {
		return e
	}
	*o = OrganizationGroup(p)
	var e error
	o.extra, e = unknownFields(b, "id", "title", "members")
	return e
}
func (o OrganizationGroup) MarshalJSON() ([]byte, error) {
	type plain OrganizationGroup
	b, e := json.Marshal(plain(o))
	if e != nil {
		return nil, e
	}
	return mergeUnknown(b, o.extra)
}

func SessionKey(id string) string { return "ref\x00local\x00" + id }

func (s *Store) PrepareCreatePresentation(ctx context.Context, id string, p Presentation) error {
	return s.mutate(ctx, func(state *State) error {
		if _, attached := sessionOwner(*state, id); attached {
			return nil
		}
		pending, ok := state.PendingCreates[id]
		if !ok {
			return ErrSessionNotFound
		}
		if pending.Presentation == nil {
			pending.Presentation = &p
			state.PendingCreates[id] = pending
		}
		return nil
	})
}
func normalizeOrganization(o *Organization) {
	if o.Order == nil {
		o.Order = []string{}
	}
	if o.Groups == nil {
		o.Groups = []OrganizationGroup{}
	}
	if o.Imported == nil {
		o.Imported = map[string]bool{}
	}
	for i := range o.Groups {
		if o.Groups[i].Members == nil {
			o.Groups[i].Members = []string{}
		}
	}
}

// UpdateOrganization is a workspace-scoped CAS. A nil revision is reserved for
// compatibility/import callers already serialized by this store's file lock.
func (s *Store) UpdateOrganization(ctx context.Context, id string, revision *uint64, change func(*Organization) error) (Organization, bool, error) {
	return s.UpdateOrganizationWithState(ctx, id, revision, func(_ *State, o *Organization) error { return change(o) })
}

func (s *Store) UpdateOrganizationWithState(ctx context.Context, id string, revision *uint64, change func(*State, *Organization) error) (Organization, bool, error) {
	var result Organization
	applied := false
	err := s.mutate(ctx, func(state *State) error {
		w, ok := state.Workspaces[id]
		if !ok {
			return ErrWorkspaceNotFound
		}
		if w.Organization == nil {
			w.Organization = &Organization{}
		}
		o := w.Organization
		normalizeOrganization(o)
		if revision != nil && o.Revision != *revision {
			result = *o
			return nil
		}
		before, _ := json.Marshal(o)
		if err := change(state, o); err != nil {
			return err
		}
		normalizeOrganization(o)
		after, _ := json.Marshal(o)
		if !bytes.Equal(before, after) {
			o.Revision++
		}
		mirrorOrganizationOrder(&w)
		state.Workspaces[id] = w
		result = *o
		applied = true
		return nil
	})
	return result, applied, err
}

func mirrorOrganizationOrder(w *Workspace) {
	if w.Organization == nil {
		return
	}
	ids := []string{}
	members := make(map[string]bool, len(w.SessionIDs))
	seen := make(map[string]bool, len(w.SessionIDs))
	for _, sid := range w.SessionIDs {
		members[sid] = true
	}
	for _, key := range w.Organization.Order {
		if sid, ok := strings.CutPrefix(key, "ref\x00local\x00"); ok {
			if members[sid] && !seen[sid] {
				ids = append(ids, sid)
				seen[sid] = true
			}
		}
	}
	for _, sid := range w.SessionIDs {
		if !seen[sid] {
			ids = append(ids, sid)
			seen[sid] = true
		}
	}
	w.SessionIDs = ids
}

func attachOrganizationSession(w *Workspace, id, parentID string) {
	if w.Organization == nil {
		return
	}
	o := w.Organization
	normalizeOrganization(o)
	key := SessionKey(id)
	if slices.Contains(o.Order, key) {
		return
	}
	parent := SessionKey(parentID)
	index := slices.Index(o.Order, parent)
	if parentID != "" && index >= 0 {
		o.Order = slices.Insert(o.Order, index+1, key)
	} else if o.ManualOrderEnabled {
		// The sidebar pages a manual order from its head; a tail entry sits
		// behind every older session and is out of reach once history is long.
		o.Order = slices.Insert(o.Order, 0, key)
	} else {
		o.Order = append(o.Order, key)
	}
	for i := range o.Groups {
		if parentID != "" && slices.Contains(o.Groups[i].Members, parent) {
			o.Groups[i].Members = append(o.Groups[i].Members, key)
		}
	}
	o.Imported[key] = true
	o.Revision++
}
