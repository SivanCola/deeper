package main

import (
	"deepzora/internal/control"
	"strings"
)

// nativeSessionSelection belongs to the durable source, including detached views.
type nativeSessionSelection struct {
	SessionHeadID string `json:"sessionHeadId,omitempty"`
}

func (a *App) controllerMatchesSessionPath(ctrl control.SessionAPI, path string) bool {
	if path == "" {
		return true
	}
	ref, ok := sessionRefForRoute(a.desktopSessionService(""), path)
	identity, bound := ctrl.(control.IdentityLifecycle)
	if !ok || !bound {
		return false
	}
	current, found := identity.SessionRef()
	return found && current == ref
}

func currentStartupSession(tab *WorkspaceTab, root, scope, route string) (string, string) {
	dir := desktopSessionDir(root)
	if scope == "global" {
		dir = desktopSessionDir(globalWorkspaceRoot())
	}
	if _, ok := parseSessionRoute(route); !ok && !strings.HasPrefix(route, embeddedBotSessionPrefix) {
		route = ""
	}
	prepareStartupPinnedContext(tab, route, route)
	return dir, route
}
