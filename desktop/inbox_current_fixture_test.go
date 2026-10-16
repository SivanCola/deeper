package main

import (
	"deepzora/internal/control"
	"testing"
)

func currentInboxTestApp(t *testing.T, options control.Options) (*App, *control.Controller, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	ctrl := newCurrentRebuildController(t, app, options)
	app.setTestCtrl(ctrl, "")
	ref, ok := ctrl.SessionRef()
	if !ok {
		t.Fatal("current inbox fixture has no identity")
	}
	app.tabs["test"].SessionID = ref.SessionID
	return app, ctrl, sessionRoute(ref.SessionID)
}
