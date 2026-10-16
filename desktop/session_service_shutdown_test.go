package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"deepzora/internal/session"
)

func TestShutdownReleasesCachedSessionOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	app := NewApp()
	service := app.desktopSessionService(dir)
	if service == nil {
		t.Fatal("missing session service")
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "shutdown-cache"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.Bind(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.shutdown(context.Background())
	if _, ok := service.Runtime(runtime.Ref()); ok {
		t.Fatal("shutdown retained the idle runtime")
	}
	reopened, err := session.NewService("local", session.NewFilesystemPersistence(app.desktopSessions.root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.CloseAll(context.Background()) })
	reader, err := reopened.EnsureExecution(t.Context(), runtime.Ref())
	if err != nil {
		t.Fatalf("shutdown retained the writer lease: %v", err)
	}
	if err := reader.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CloseAll(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownPreservesCurrentIdentityAndDurableHistory(t *testing.T) {
	app, tab, ctrl, _ := currentClearFixture(t, workspaceContextProvider{}, nil)
	if err := ctrl.RunTurn(t.Context(), "keep this current conversation"); err != nil {
		t.Fatal(err)
	}
	ref, _ := ctrl.SessionRef()
	before, err := ctrl.SessionService().Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture installs its tab directly; normal session publication queues
	// this layout write before shutdown drains the pending persistence.
	app.queueCurrentTabLayout()
	app.shutdown(context.Background())
	saved := loadTabsFile()
	if len(saved.Tabs) != 1 || saved.Tabs[0].ID != tab.ID || saved.Tabs[0].SessionID != ref.SessionID {
		t.Fatalf("shutdown persisted a different session binding: %+v", saved.Tabs)
	}
	service, err := session.NewService(ref.HostID, session.NewFilesystemPersistence(app.desktopSessions.root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	runtime, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatalf("current session could not be reopened after shutdown: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Release(context.Background()) })
	after, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil || after.DurableSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) {
		t.Fatalf("shutdown lost current history: %v", err)
	}
}
