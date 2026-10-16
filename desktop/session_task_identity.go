package main

import (
	"deepzora/internal/control"
)

// Task ownership uses the currently bound immutable session identity.
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
	return ""
}
