package control

import (
	"context"
	"testing"

	"deepzora/internal/session"
)

func newOwnedTestController(t testing.TB, options Options) *Controller {
	t.Helper()
	controller := New(options)
	t.Cleanup(func() {
		controller.Close()
		// Close only starts teardown; finalizeControllerClose joins the workers
		// that may still write under SessionDir. Waiting keeps a late Flush from
		// racing t.TempDir removal, so it is unconditional.
		<-controller.closeFinalized
		if options.SessionService == nil {
			return
		}
		// Close an idle retained runtime when this is the final owner. A runtime
		// still bound by another controller belongs to that controller's cleanup.
		_ = options.SessionService.CloseAll(context.Background())
	})
	return controller
}

// Current persistence fixtures seed the supplied executor directly into the
// current event log. They never import a transcript path or sidecar.
func newCurrentOwnedTestController(t testing.TB, options Options) *Controller {
	t.Helper()
	service, err := session.NewService("control-test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(context.Background(), session.CreateOptions{SessionID: "controller-session", CWD: options.WorkspaceRoot, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown current fixture: %v", err)
		}
	})
	options.SessionService, options.SessionCreateService = service, service
	options.SessionRuntime, options.ExclusiveSession = runtime, true
	return newOwnedTestController(t, options)
}

func reopenCurrentOwnedTestController(t testing.TB, previous *Controller, options Options) *Controller {
	t.Helper()
	service := previous.SessionService()
	ref, ok := previous.SessionRef()
	if !ok {
		t.Fatal("cannot reopen an unbound fixture")
	}
	previous.Close()
	<-previous.closeFinalized
	if err := service.Close(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	binding, err := service.Open(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(context.Background())
	options.SessionService, options.SessionCreateService = service, service
	options.SessionRuntime, options.ExclusiveSession = binding.Runtime(), true
	return newOwnedTestController(t, options)
}
