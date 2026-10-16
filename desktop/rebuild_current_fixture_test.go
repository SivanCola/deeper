package main

import (
	"fmt"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func newCurrentRebuildController(t *testing.T, app *App, opts control.Options) *control.Controller {
	t.Helper()
	return newNamedCurrentRebuildController(t, app, "rebuild-current", opts)
}

func newNamedCurrentRebuildController(t *testing.T, app *App, id string, opts control.Options) *control.Controller {
	t.Helper()
	firstService := len(app.sessionServices) == 0
	service := app.desktopSessionService("")
	if firstService {
		t.Cleanup(app.closeSessionServices)
	}
	root := opts.WorkspaceRoot
	if root == "" {
		root = globalTabWorkspaceRoot()
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id, CWD: root, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Executor != nil {
		for i, message := range opts.Executor.Session().Snapshot() {
			message.ID = fmt.Sprintf("initial-%d", i)
			if message.Role == provider.RoleUser {
				message.Origin = provider.MessageOriginUser
			}
			appendSessionTestMessage(t, runtime, message.ID, message)
		}
	}
	opts.SessionService, opts.SessionRuntime, opts.ExclusiveSession = service, runtime, true
	opts.SessionCreateService = service
	ctrl := control.New(opts)
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed() })
	return ctrl
}

func setCurrentRebuildTabIdentity(t *testing.T, app *App, tab *WorkspaceTab, ctrl *control.Controller) {
	t.Helper()
	ref, ok := ctrl.SessionRef()
	if !ok {
		t.Fatal("missing current rebuild identity")
	}
	tab.SessionID = ref.SessionID
	workspace, err := app.attachDesktopSession(t.Context(), tab.Scope, tab.WorkspaceRoot, ref)
	if err != nil {
		t.Fatal(err)
	}
	tab.SessionWorkspace.ID = workspace
}
