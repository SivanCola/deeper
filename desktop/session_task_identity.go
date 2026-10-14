package main

import (
	"deepzora/internal/agent"
	"deepzora/internal/control"
)

// Canonical controllers use SessionRef. Current internal Agent controllers
// use their DAG branch identity; an unbound canonical controller has no fallback.
func controllerTaskSessionID(ctrl control.SessionAPI) string {
	if ctrl == nil {
		return ""
	}
	if lifecycle, ok := ctrl.(control.IdentityLifecycle); ok && lifecycle.UsesExclusiveSession() {
		if ref, bound := lifecycle.SessionRef(); bound {
			return ref.SessionID
		}
		return ""
	}
	return agent.BranchID(ctrl.SessionPath())
}
