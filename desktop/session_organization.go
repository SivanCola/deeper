package main

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

type SessionOrganizationWorkspace struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	HostID        string `json:"hostId,omitempty"`
}
type SessionOrganizationSnapshot struct {
	Revision           uint64         `json:"revision"`
	Applied            bool           `json:"applied"`
	ManualOrderEnabled bool           `json:"manualOrderEnabled"`
	Order              []string       `json:"order"`
	Groups             []desktopGroup `json:"groups"`
}
type SessionOrganizationMutation struct {
	Kind     string           `json:"kind"`
	Target   *SessionSelector `json:"target,omitempty"`
	Anchor   *SessionSelector `json:"anchor,omitempty"`
	Position string           `json:"position,omitempty"`
	GroupID  string           `json:"groupId,omitempty"`
	Title    string           `json:"title,omitempty"`
	// SortMode is the activity order the sidebar shows while no manual order exists.
	SortMode string `json:"sortMode,omitempty"`
}

func organizationSnapshot(o workspacestate.Organization, applied bool) SessionOrganizationSnapshot {
	groups := []desktopGroup{}
	for _, g := range o.Groups {
		groups = append(groups, desktopGroup{ID: g.ID, Title: g.Title, SessionKeys: append([]string{}, g.Members...)})
	}
	return SessionOrganizationSnapshot{Revision: o.Revision, Applied: applied, ManualOrderEnabled: o.ManualOrderEnabled, Order: append([]string{}, o.Order...), Groups: groups}
}

// Keep organization membership aligned with the current workspace registry.
func (a *App) ensureSessionOrganization(scope, root string) (string, workspacestate.Organization, error) {
	return a.ensureSessionOrganizationSources(scope, root, false)
}

func (a *App) ensureSessionOrganizationSources(scope, root string, mutation bool) (string, workspacestate.Organization, error) {
	scope, root, err := normalizeOrganizationTarget(scope, root)
	if err != nil {
		return "", workspacestate.Organization{}, err
	}
	id, err := a.resolveDesktopWorkspaceID(a.bootContext(), scope, root)
	if err != nil {
		return "", workspacestate.Organization{}, err
	}
	state, err := a.workspaceRegistry().LoadProjection(a.bootContext())
	if err != nil {
		return "", workspacestate.Organization{}, err
	}
	w, exists := state.Workspaces[id]
	if !exists {
		id, err = a.ensureDesktopWorkspace(a.bootContext(), scope, root)
		if err != nil {
			return "", workspacestate.Organization{}, err
		}
		state, err = a.workspaceRegistry().LoadProjection(a.bootContext())
		if err != nil {
			return "", workspacestate.Organization{}, err
		}
		w = state.Workspaces[id]
	}
	update := func(o *workspacestate.Organization) error {
		if o.Imported == nil {
			o.Imported = map[string]bool{}
		}
		for _, sid := range w.SessionIDs {
			ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sid}
			key := projectNodeSessionKey(ProjectNode{Session: &ref})
			o.Imported[key] = true
			if !slices.Contains(o.Order, key) {
				o.Order = append(o.Order, key)
			}
		}
		return nil
	}
	if w.Organization != nil {
		next := w.Organization.Clone()
		_ = update(&next)
		if reflect.DeepEqual(next, *w.Organization) {
			return id, next, nil
		}
	}
	o, _, err := a.workspaceRegistry().UpdateOrganization(a.bootContext(), id, nil, update)
	return id, o, err
}

func projectShellPreferences(projects desktopProjectFile, scope, root string) desktopProject {
	if scope == "project" {
		// Most callers use the persisted spelling; avoid filesystem comparisons
		// unless this request actually uses an alias.
		for _, project := range projects.Projects {
			if project.Root == root {
				return project
			}
		}
		if i := projectIndexByRoot(projects.Projects, root); i >= 0 {
			return projects.Projects[i]
		}
		return desktopProject{}
	}
	return desktopProject{Groups: projects.GlobalGroups, SessionOrder: projects.GlobalSessionOrder,
		Topics: projects.GlobalTopics, ManualSessionOrder: projects.GlobalManualSessionOrder, ManualTopicOrder: projects.GlobalManualTopicOrder}
}

func workspaceSourceAliases(state workspacestate.State, workspaceID string) map[string][]string {
	result := map[string][]string{}
	// A topic aliases its initial placeholder only while it has one owner.
	// Build this once per workspace, not once per row of a large history page.
	owners := map[string]string{}
	for _, id := range state.Workspaces[workspaceID].SessionIDs {
		if topic := state.Presentation[id].TopicID; topic != "" {
			if _, seen := owners[topic]; seen {
				owners[topic] = ""
			} else {
				owners[topic] = id
			}
		}
	}
	for topic, id := range owners {
		if id != "" {
			result[id] = append(result[id], "topic\x00"+topic)
		}
	}

	for id, aliases := range result {
		slices.Sort(aliases)
		result[id] = slices.Compact(aliases)
	}
	return result
}

func sourceAliases(state workspacestate.State, workspaceID, sessionID string) []string {
	return append([]string{}, workspaceSourceAliases(state, workspaceID)[sessionID]...)
}

func (a *App) GetSessionOrganization(workspace SessionOrganizationWorkspace) (SessionOrganizationSnapshot, error) {
	if workspace.HostID != "" && workspace.HostID != localDesktopHostID {
		return a.remoteSessionOrganization(workspace, nil, nil)
	}
	_, o, err := a.ensureSessionOrganization(workspace.Scope, workspace.WorkspaceRoot)
	return organizationSnapshot(o, err == nil), err
}

func (a *App) UpdateSessionOrganization(workspace SessionOrganizationWorkspace, expectedRevision uint64, mutation SessionOrganizationMutation) (SessionOrganizationSnapshot, error) {
	if workspace.HostID != "" && workspace.HostID != localDesktopHostID {
		return a.remoteSessionOrganization(workspace, &expectedRevision, &mutation)
	}
	// Group edits address either a group or one explicit source. Only moving
	// in a manual order needs the complete relative order of other sources.
	id, current, err := a.ensureSessionOrganizationSources(workspace.Scope, workspace.WorkspaceRoot, mutation.Kind == "move")
	if err != nil {
		return SessionOrganizationSnapshot{}, err
	}
	var shown []string
	if mutation.Kind == "move" && !current.ManualOrderEnabled {
		if shown, err = a.shownSessionOrder(workspace.Scope, workspace.WorkspaceRoot, mutation.SortMode); err != nil {
			return SessionOrganizationSnapshot{}, err
		}
	}
	resolved := []SessionTarget{}
	resolve := func(selector *SessionSelector) (string, error) {
		if selector == nil {
			return "", newSessionOperationError("target_not_found", "Select a session.")
		}
		target, e := a.resolveSessionTarget(*selector)
		if e != nil {
			return "", e
		}
		targetWorkspaceID, e := a.resolveDesktopWorkspaceID(a.bootContext(), target.Scope, target.WorkspaceRoot)
		if e != nil {
			return "", e
		}
		if targetWorkspaceID != id {
			return "", newSessionOperationError("target_changed", "The session moved to another workspace.")
		}
		var ref *session.SessionRef
		if target.SessionRef.SessionID != "" {
			ref = &target.SessionRef
		} else {
			return "", workspacestate.ErrMutationConflict
		}
		resolved = append(resolved, target)
		return projectNodeSessionKey(ProjectNode{Session: ref, SessionPath: target.SessionPath}), nil
	}
	key, anchor := "", ""
	if mutation.Kind == "move" || mutation.Kind == "set-group" {
		key, err = resolve(mutation.Target)
		if err != nil {
			return SessionOrganizationSnapshot{}, err
		}
	}
	if mutation.Kind == "move" {
		anchor, err = resolve(mutation.Anchor)
		if err != nil {
			return SessionOrganizationSnapshot{}, err
		}
	}
	o, applied, err := a.workspaceRegistry().UpdateOrganizationWithState(a.bootContext(), id, &expectedRevision, func(state *workspacestate.State, o *workspacestate.Organization) error {
		if mutation.Kind == "move" && !o.ManualOrderEnabled {
			seedManualOrder(o, shown)
		}
		return applyResolvedOrganizationMutation(state, id, o, resolved, mutation, key, anchor)
	})
	if err == nil && applied {
		a.emitProjectTreeMetadataChanged()
	}
	return organizationSnapshot(o, applied), err
}

// shownSessionOrder lists a workspace in the activity order the sidebar is
// showing, so the first manual move starts from what the user was looking at.
func (a *App) shownSessionOrder(scope, root, sortMode string) ([]string, error) {
	keys := []string{}
	req := ProjectTopicPageRequest{Scope: scope, WorkspaceRoot: root, Limit: 200, SortMode: sortMode}
	for {
		page, err := a.unifiedProjectTopics(req)
		a.ReleaseReadSnapshot(page.SnapshotID)
		if err != nil {
			return nil, err
		}
		for _, node := range page.Items {
			keys = append(keys, projectNodeSessionKey(node))
		}
		if page.NextCursor == "" {
			return keys, nil
		}
		if page.NextCursor == req.Cursor {
			return nil, fmt.Errorf("session order cursor did not advance")
		}
		req.Cursor = page.NextCursor
	}
}

// seedManualOrder replaces the attach order, which is oldest first and never
// what the sidebar showed, with the shown order. Unlisted keys keep their place
// after the listed ones.
func seedManualOrder(o *workspacestate.Organization, shown []string) {
	rank := make(map[string]int, len(shown))
	for i, key := range shown {
		if _, ok := rank[key]; !ok {
			rank[key] = i
		}
	}
	slices.SortStableFunc(o.Order, func(left, right string) int {
		l, lok := rank[left]
		r, rok := rank[right]
		switch {
		case lok && rok:
			return l - r
		case lok:
			return -1
		case rok:
			return 1
		}
		return 0
	})
}

// Run inside the registry transaction: resolution precedes the write lock, so
// source adoption and lifecycle changes must be checked again at commit time.
func applyResolvedOrganizationMutation(state *workspacestate.State, workspaceID string, o *workspacestate.Organization, resolved []SessionTarget, mutation SessionOrganizationMutation, key, anchor string) error {
	for _, target := range resolved {
		if target.SessionRef.SessionID == "" {
			return workspacestate.ErrMutationConflict
		}
		current := state.SessionStates[target.SessionRef.SessionID]
		if current.Lifecycle != workspacestate.Active || current.Generation != target.LifecycleGeneration || !slices.Contains(state.Workspaces[workspaceID].SessionIDs, target.SessionRef.SessionID) {
			return workspacestate.ErrMutationConflict
		}
	}
	if mutation.Kind == "set-group" && !o.Imported[key] {
		// Imported also records explicit ungrouping so later preference
		// discovery cannot restore an older assignment.
		o.Imported[key] = true
		if !slices.Contains(o.Order, key) {
			o.Order = append(o.Order, key)
		}
	}
	return applyOrganizationMutation(o, mutation, key, anchor)
}

// replaceSessionOrganizationGroups retains old RPC signatures while moving their
// persistence into the same transaction as ordering and lifecycle mutations.
func (a *App) replaceSessionOrganizationGroups(ctx context.Context, scope, root string, revision *uint64, groups []desktopGroup) (ProjectGroupsSnapshot, error) {
	id, _, err := a.ensureSessionOrganizationSources(scope, root, true)
	if err != nil {
		return ProjectGroupsSnapshot{}, err
	}
	if err = validateSessionGroups(groups); err != nil {
		return ProjectGroupsSnapshot{}, err
	}
	state, err := a.workspaceRegistry().LoadProjection(ctx)
	if err != nil {
		return ProjectGroupsSnapshot{}, err
	}
	nodes := []ProjectNode{}
	for _, sid := range state.Workspaces[id].SessionIDs {
		if state.SessionStates[sid].Lifecycle != workspacestate.Active {
			continue
		}
		ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sid}
		nodes = append(nodes, ProjectNode{Session: &ref, TopicID: state.Presentation[sid].TopicID})
	}
	o, applied, err := a.workspaceRegistry().UpdateOrganization(ctx, id, revision, func(o *workspacestate.Organization) error {
		next := []workspacestate.OrganizationGroup{}
		for _, g := range groups {
			members := []string{}
			for _, node := range nodes {
				key := projectNodeSessionKey(node)
				if o.Imported[key] && desktopGroupContainsNode(g, node) && !slices.Contains(members, key) {
					members = append(members, key)
				}
			}
			for _, key := range g.SessionKeys {
				if !o.Imported[key] {
					return workspacestate.ErrMutationConflict
				}
				if !slices.Contains(members, key) {
					members = append(members, key)
				}
			}
			group := workspacestate.OrganizationGroup{ID: g.ID, Title: g.Title, Members: members}
			for _, existing := range o.Groups {
				if existing.ID == g.ID {
					group = existing
					group.Title, group.Members = g.Title, members
					break
				}
			}
			next = append(next, group)
		}
		o.Groups = next
		return nil
	})
	if err == nil && applied {
		a.emitProjectTreeMetadataChanged()
	}
	s := organizationSnapshot(o, applied)
	return ProjectGroupsSnapshot{Groups: s.Groups, Revision: s.Revision, Applied: applied}, err
}

func applyOrganizationMutation(o *workspacestate.Organization, mutation SessionOrganizationMutation, key, anchor string) error {
	switch mutation.Kind {
	case "reset-order":
		// Keep the recorded order and groups; time sorting ignores ranks while disabled.
		o.ManualOrderEnabled = false
	case "move":
		if key == anchor {
			return nil
		}
		if !o.Imported[key] || !o.Imported[anchor] {
			return workspacestate.ErrMutationConflict
		}
		if mutation.Position != "before" && mutation.Position != "after" {
			return fmt.Errorf("invalid position")
		}
		o.Order = slices.DeleteFunc(o.Order, func(v string) bool { return v == key })
		i := slices.Index(o.Order, anchor)
		if i < 0 {
			return workspacestate.ErrMutationConflict
		}
		if mutation.Position == "after" {
			i++
		}
		o.Order = slices.Insert(o.Order, i, key)
		o.ManualOrderEnabled = true
	case "set-group":
		if !o.Imported[key] {
			return workspacestate.ErrMutationConflict
		}
		found := mutation.GroupID == ""
		for _, g := range o.Groups {
			found = found || g.ID == mutation.GroupID
		}
		if !found {
			return workspacestate.ErrMutationConflict
		}
		for i := range o.Groups {
			o.Groups[i].Members = slices.DeleteFunc(o.Groups[i].Members, func(v string) bool { return v == key })
			if o.Groups[i].ID == mutation.GroupID {
				o.Groups[i].Members = append(o.Groups[i].Members, key)
			}
		}
	case "create-group":
		if len(o.Groups) >= maxSessionGroups {
			return fmt.Errorf("group limit exceeded")
		}
		if err := validateSessionGroups([]desktopGroup{{ID: mutation.GroupID, Title: mutation.Title}}); err != nil {
			return err
		}
		if strings.TrimSpace(mutation.GroupID) == "" || strings.TrimSpace(mutation.Title) == "" {
			return fmt.Errorf("group id and title required")
		}
		for _, g := range o.Groups {
			if g.ID == mutation.GroupID {
				return workspacestate.ErrMutationConflict
			}
		}
		o.Groups = append(o.Groups, workspacestate.OrganizationGroup{ID: mutation.GroupID, Title: strings.TrimSpace(mutation.Title), Members: []string{}})
	case "rename-group", "delete-group":
		if mutation.Kind == "rename-group" {
			if err := validateSessionGroups([]desktopGroup{{ID: mutation.GroupID, Title: mutation.Title}}); err != nil {
				return err
			}
		}
		index := slices.IndexFunc(o.Groups, func(g workspacestate.OrganizationGroup) bool { return g.ID == mutation.GroupID })
		if index < 0 {
			return workspacestate.ErrMutationConflict
		}
		if mutation.Kind == "delete-group" {
			o.Groups = slices.Delete(o.Groups, index, index+1)
		} else {
			if strings.TrimSpace(mutation.Title) == "" {
				return fmt.Errorf("title required")
			}
			o.Groups[index].Title = strings.TrimSpace(mutation.Title)
		}
	default:
		return fmt.Errorf("unsupported organization mutation")
	}
	return nil
}
