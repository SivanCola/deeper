package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartupReservationReadinessPrecedesRestoredShell(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	app.tabsRestored = make(chan struct{})
	var scans atomic.Int32
	app.projectTreeCatalogRefreshHook = func() { scans.Add(1) }
	app.startDesktopSessionRecovery(ctx)
	select {
	case <-app.desktopRecoveryDone:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("reservation recovery waited for the shell that depends on it")
	}
	if scans.Load() != 0 {
		t.Fatal("current reservation recovery scheduled a historical root scan")
	}
	app.markTabsRestored()
	app.closeSessionServices()
}
