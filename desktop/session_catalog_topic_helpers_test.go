package main

import (
	"testing"
	"time"
)

const sessionCatalogTestDeadline = 30 * time.Second

func waitForInitialCatalogReconcile(t *testing.T, app *App) bool {
	t.Helper()
	// Own cleanup before admission can fail. Publication/readiness assertions
	// must not leave a late SQLite opener running into temporary-dir cleanup.
	t.Cleanup(func() {
		if !app.stopSessionCatalog(sessionCatalogTestDeadline) {
			t.Error("session catalog fixture did not stop cleanly")
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
	if app.sessionCatalog.Load() == nil {
		t.Fatal("session catalog exited before publication")
	}
	return alreadyStarted
}
