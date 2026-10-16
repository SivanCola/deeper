package main

import (
	"context"
	"deepzora/internal/history"
	"testing"
	"time"
)

const sessionCatalogTestDeadline = 30 * time.Second

func waitForInitialCatalogReconcile(t *testing.T, app *App) bool {
	t.Helper()
	// Own cleanup before admission can fail. Publication/readiness assertions
	// must not leave a late query observer running into temporary-dir cleanup.
	t.Cleanup(func() {
		if !app.stopSessionCatalog(sessionCatalogTestDeadline) {
			t.Error("session catalog fixture did not stop cleanly")
		}
		ctx, cancel := context.WithTimeout(context.Background(), sessionCatalogTestDeadline)
		defer cancel()
		if err := history.CloseSharedCatalog(ctx); err != nil {
			t.Error(err)
		}
	})
	app.catalogLifecycleMu.Lock()
	alreadyStarted := app.catalogInitialReconcileDone != nil
	app.catalogLifecycleMu.Unlock()
	app.startSessionCatalog()
	app.catalogLifecycleMu.Lock()
	done := app.catalogInitialReconcileDone
	app.catalogLifecycleMu.Unlock()
	if done == nil {
		t.Fatal("session catalog initial reconcile was not armed")
	}
	select {
	case <-done:
	case <-time.After(sessionCatalogTestDeadline):
		t.Fatal("session catalog initial reconcile did not finish")
	}
	app.catalogLifecycleMu.Lock()
	monitor := app.catalogDone
	app.catalogLifecycleMu.Unlock()
	select {
	case <-monitor:
		t.Fatal("session catalog exited before publication")
	default:
	}
	return alreadyStarted
}
