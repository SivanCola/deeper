package cli

import (
	"context"
	"deepzora/internal/agent"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"testing"
)

func newCurrentTestController(t *testing.T, options control.Options) *control.Controller {
	t.Helper()
	if options.SessionDir == "" {
		options.SessionDir = t.TempDir()
	}
	service, err := session.NewService("local", session.NewFilesystemPersistence(session.RootForSessionDir(options.SessionDir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	options.SessionService, options.ExclusiveSession = service, true
	if options.Executor == nil {
		options.Executor = agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	}
	return newOwnedTestController(t, options)
}

func newOwnedTestController(t testing.TB, options control.Options) *control.Controller {
	t.Helper()
	controller := control.New(options)
	ownTestController(t, controller)
	return controller
}

func newBoundCurrentTestController(t *testing.T, options control.Options) *control.Controller {
	t.Helper()
	service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown fixture: %v", err)
		}
	})
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "cli-current", CWD: options.WorkspaceRoot, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	options.SessionService, options.SessionCreateService = service, service
	options.SessionRuntime, options.ExclusiveSession = runtime, true
	return newOwnedTestController(t, options)
}

func ownTestController(t testing.TB, controller *control.Controller) {
	t.Helper()
	t.Cleanup(func() {
		controller.Close()
		// Close requests teardown; Closed joins the final persistence work and
		// releases stores before t.TempDir removes their files (notably Windows).
		<-controller.Closed()
	})
}
