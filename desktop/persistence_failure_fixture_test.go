package main

import (
	"os"
	"testing"
)

// Failure injection targets the Snapshot boundary while the controller owns a
// real current session, so navigation cannot bypass a failed durability fence.
func appWithSnapshotFailure(t *testing.T) (*App, *WorkspaceTab) {
	t.Helper()
	app, tab := appWithTab(t)
	tab.Ctrl = &snapshotErrorSessionController{SessionAPI: tab.Ctrl, err: os.ErrPermission}
	return app, tab
}
