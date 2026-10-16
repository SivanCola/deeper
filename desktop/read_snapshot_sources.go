package main

import "deepzora/desktop/internal/workspacestate"

func projectionReadVersions(state workspacestate.State, supplied []*workspacestate.ReadVersions) *workspacestate.ReadVersions {
	if len(supplied) > 0 && supplied[0] != nil {
		return supplied[0]
	}
	// Callers constructing their own projection cannot borrow a different
	// registry publication solely because its numeric generation matches.
	return workspacestate.NewReadVersions(state)
}

func (a *App) workspaceReadFence(versions *workspacestate.ReadVersions, workspace workspacestate.Workspace, nodes []ProjectNode) func(*workspacestate.ReadSnapshot) error {
	states := map[string]string{}
	for _, node := range nodes {
		if node.Session != nil {
			states[node.Session.SessionID] = versions.Session(node.Session.SessionID)
		}
	}
	workspace.Organization = nil
	workspace.SessionIDs = nil
	return func(current *workspacestate.ReadSnapshot) error {
		owner, ok := current.WorkspaceMetadata(workspace.ID)
		if !ok || owner.Root != workspace.Root || owner.Visible != workspace.Visible {
			return snapshotStale("lifecycle_changed")
		}
		for id, expected := range states {
			member := current.Session(id)
			if member.Workspace.ID != workspace.ID || member.OwnershipConflict || current.ReadVersions().Session(id) != expected {
				return snapshotStale("lifecycle_changed")
			}
		}
		return nil
	}
}
