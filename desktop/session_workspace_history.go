package main

import (
	"sort"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

// workspaceHistorySessions reads membership from the same registry as the
// sidebar. Archived sessions and sessions owned by another workspace are not
// candidates for prompt recall or memory suggestions.
func (a *App) workspaceHistorySessions(scope, root string) ([]session.SessionInfo, error) {
	state, err := a.workspaceRegistry().LoadProjection(a.bootContext())
	if err != nil {
		return nil, err
	}
	id := desktopWorkspaceOwnerID(state, scope, root)
	var ids []string
	for _, sid := range state.Workspaces[id].SessionIDs {
		if state.SessionStates[sid].Lifecycle == workspacestate.Active {
			ids = append(ids, sid)
		}
	}
	infos, err := listWorkspaceSessionInfo(a.bootContext(), a.desktopSessionService("").Query(), ids)
	if err != nil {
		return nil, err
	}
	out := make([]session.SessionInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].SessionID > out[j].SessionID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}
