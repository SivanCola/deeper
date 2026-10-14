package main

import (
	"slices"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

// GetSessionSummary uses the same durable projection as the workspace list.
// A topic, live tab, or disposable catalog cannot invent a replacement row.
func (a *App) GetSessionSummary(ref session.SessionRef) (ProjectNode, error) {
	empty := ProjectNode{Children: []ProjectNode{}}
	if err := validateLocalSessionRef(ref); err != nil {
		return empty, err
	}
	state, err := a.workspaceRegistry().LoadProjection(a.bootContext())
	if err != nil {
		return empty, err
	}
	if state.SessionStates[ref.SessionID].Lifecycle != workspacestate.Active {
		return empty, session.ErrSessionNotFound
	}
	var workspace workspacestate.Workspace
	for _, candidate := range state.Workspaces {
		if slices.Contains(candidate.SessionIDs, ref.SessionID) {
			if workspace.ID != "" {
				return empty, errSessionWorkspaceConflict
			}
			workspace = candidate
		}
	}
	if workspace.ID == "" {
		return empty, session.ErrSessionNotFound
	}
	info, err := a.desktopSessionService("").Query().Stat(a.bootContext(), ref)
	if err != nil {
		return empty, err
	}
	if info.MetadataStatus == session.MetadataFailed {
		return empty, newSessionOperationError(sessionOperationDamaged, "The session metadata could not be read.")
	}
	if !sameDesktopPath(info.CWD, workspace.Root) && !(workspace.ID == workspacestate.GlobalWorkspaceID && isGlobalWorkspacePath(workspace, info.CWD)) {
		return empty, errSessionWorkspaceConflict
	}
	node := a.canonicalProjectSessionNode(state, workspace, info, ref.SessionID,
		slices.Index(workspace.SessionIDs, ref.SessionID), loadTopicCreatedAts(topicTitleRoot(canonicalWorkspaceScope(workspace), workspace.Root)))
	org := workspacestate.Organization{}
	if workspace.Organization != nil {
		org = *workspace.Organization
	}
	nodes := filterWorkspaceSessionNodes(ProjectTopicPageRequest{Scope: canonicalWorkspaceScope(workspace), WorkspaceRoot: workspace.Root},
		org, state, workspace.ID, []ProjectNode{node})
	if len(nodes) == 0 {
		return empty, session.ErrSessionNotFound
	}
	_, nodes[0].Open = a.desktopSessionService("").Runtime(ref)
	return nodes[0], nil
}
