package main

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/sessioncatalog"
	"deepzora/internal/sqliteuri"
)

func TestReconcileWaitEndsWhenCatalogStopsBeforeDiscoveryStarts(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app.ctx = ctx
	catalog, err := sessioncatalog.Open(t.Context(), sessioncatalog.Options{InMemory: true, MetadataOnly: true, StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	settled, accepted := catalog.ScheduleReconcile(sessioncatalog.DirectoryTarget{Path: t.TempDir(), Scope: "global"})
	if !accepted {
		t.Fatal("paused catalog rejected reconcile")
	}
	finished := make(chan struct{})
	go func() {
		app.waitSessionCatalogReconcile(catalog, settled)
		close(finished)
	}()
	if err := catalog.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-settled:
		t.Fatal("unvisited discovery must not be reported as settled")
	default:
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile waiter stayed blocked after its catalog stopped")
	}
}

func TestStopSessionCatalogWaitsForEarlierTimedOutClose(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	catalog, err := sessioncatalog.Open(t.Context(), sessioncatalog.Options{Path: path, DisableRepair: true})
	if err != nil {
		t.Fatal(err)
	}
	app.sessionCatalog.Store(catalog)
	t.Cleanup(func() {
		if !app.stopSessionCatalog(sessionCatalogTestDeadline) {
			t.Error("session catalog did not stop before directory cleanup")
		}
	})

	// A reader holding a WAL snapshot makes the closing TRUNCATE checkpoint wait
	// out its busy timeout, so the close outlives a short stop deadline.
	dsn, err := sqliteuri.Disk(path, url.Values{"mode": {"ro"}})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	tx, err := reader.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	var revision uint64
	if err := tx.QueryRow(`SELECT revision FROM catalog_state WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}

	if app.stopSessionCatalog(time.Millisecond) {
		t.Fatal("stop finished while a reader pinned the WAL checkpoint")
	}
	if !app.stopSessionCatalog(sessionCatalogTestDeadline) {
		t.Fatal("a later stop did not wait out the earlier close")
	}
	if state := catalog.Status().State; state != sessioncatalog.StateClosed {
		t.Fatalf("stop reported the catalog stopped while it was %q", state)
	}
}
