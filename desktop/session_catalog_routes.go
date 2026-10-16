package main

import (
	"context"
	"sort"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/config"
	"deepzora/internal/session"
	"deepzora/internal/tool/sessiontool"
)

func sessionRoute(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return remoteSessionIDRoutePrefix + id
}

func parseSessionRoute(route string) (string, bool) {
	locator := classifySessionLocator(route)
	if locator.kind != sessionLocatorCanonical {
		return "", false
	}
	return locator.ref.SessionID, true
}

func sameSessionRoute(left, right string) bool {
	leftID, leftOK := parseSessionRoute(left)
	rightID, rightOK := parseSessionRoute(right)
	return leftOK && rightOK && leftID == rightID
}

// sessionToolScope uses the sidebar's current membership, including moves and
// archive changes, rather than widening a tab's tools to the shared host store.
func (a *App) sessionToolScope(dir string) sessiontool.Scope {
	return func(ctx context.Context) (map[string]bool, error) {
		return a.sessionIDsForDirectory(ctx, dir)
	}
}

func (a *App) sessionIDsForDirectory(ctx context.Context, dir string) (map[string]bool, error) {
	state, err := a.workspaceRegistry().Load(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, workspace := range state.Workspaces {
		matches := sameDesktopPath(dir, desktopSessionDir(workspace.Root))
		if workspace.ID == workspacestate.GlobalWorkspaceID {
			matches = matches || sameDesktopPath(dir, config.SessionDir())
		}
		if matches {
			for _, id := range workspace.SessionIDs {
				if state.SessionStates[id].Lifecycle == workspacestate.Active {
					allowed[id] = true
				}
			}
		}
	}
	return allowed, nil
}

func (a *App) listCanonicalSessionsFromDir(dir, active string) []SessionMeta {
	service := a.desktopSessionService(dir)
	if service == nil {
		return []SessionMeta{}
	}
	activeID, _ := parseSessionRoute(active)
	allowed, err := a.sessionIDsForDirectory(a.bootContext(), dir)
	if err != nil {
		return []SessionMeta{{Error: "workspace_registry_unavailable", TurnsState: "corrupt"}}
	}
	query := service.Query()
	result := make([]SessionMeta, 0)
	cursor := ""
	for {
		page, err := query.List(context.Background(), cursor, 100)
		if err != nil {
			return result
		}
		for _, info := range page.Sessions {
			if !allowed[info.SessionID] {
				continue
			}
			delete(allowed, info.SessionID)
			meta := SessionMeta{
				Path: sessionRoute(info.SessionID), SessionID: info.SessionID,
				HostID: service.HostID(), Codec: info.Codec, Error: info.Error,
				Title: info.Title, Turns: info.Turns, TurnsState: "valid",
				CreatedAt: info.CreatedAt.UnixMilli(), LastActivityAt: info.UpdatedAt.UnixMilli(),
				Current: info.SessionID == activeID,
			}
			if info.Error != "" {
				meta.TurnsState = "corrupt"
			} else {
				meta.Preview = info.Preview
			}
			a.mu.RLock()
			_, meta.Open = a.runtimeBySessionKey[sessionRuntimeKey(meta.Path)]
			a.mu.RUnlock()
			result = append(result, meta)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	for id := range allowed {
		result = append(result, SessionMeta{Path: sessionRoute(id), SessionID: id, HostID: localDesktopHostID, Error: "session_content_unavailable", TurnsState: "corrupt"})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].LastActivityAt > result[j].LastActivityAt })
	return result
}

func sessionRefForRoute(service *session.Service, route string) (session.SessionRef, bool) {
	id, ok := parseSessionRoute(route)
	if !ok || service == nil {
		return session.SessionRef{}, false
	}
	return session.SessionRef{HostID: service.HostID(), SessionID: id}, true
}
