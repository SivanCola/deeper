package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func currentCatalogRepairFixture(t *testing.T) (*App, string, string) {
	t.Helper()
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	installNoopRuntimeEvents(app)
	app.desktopSessionService("").MetadataOnlyListings()
	t.Cleanup(func() { app.stopSessionCatalog(time.Second); app.closeSessionServices() })
	topic, err := app.CreateTopic("project", t.TempDir(), "Current repair target")
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := currentTopicInfo(t, app, topic.ID)
	root := app.desktopSessionService("").Query().StorageRoot()
	cache := filepath.Join(root, ".query-cache", ref.SessionID, "catalog-metadata.json")
	if err := os.WriteFile(cache, []byte("invalid disposable cache"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.desktopSessionService("").Query().Stat(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	status := app.GetSessionCatalogStatus()
	if status.Total != 1 || status.Indexed != 0 || status.RepairPending != 1 || !status.CanRebuild {
		t.Fatalf("current pending metadata: %+v", status)
	}
	return app, filepath.Join(root, ref.SessionID, "events.frames"), cache
}

func TestCurrentCatalogRebuildJoinsRequestsAndPreservesSource(t *testing.T) {
	app, source, _ := currentCatalogRepairFixture(t)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	started, resume, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	t.Cleanup(release)
	var refreshed atomic.Int32
	app.lifecycleCheckpointHook = func(phase string) {
		if phase == "current-catalog-before-refresh" {
			refreshed.Add(1)
			close(started)
			<-resume
		}
	}
	app.catalogRebuildJoinHook = func() { close(joined) }
	leader, follower := make(chan error, 1), make(chan error, 1)
	go func() { leader <- app.RebuildSessionCatalog() }()
	awaitRemovalTest(t, started)
	status := app.GetSessionCatalogStatus()
	if status.State != "rebuilding" || status.Total != 1 || status.CanRebuild {
		t.Fatalf("rebuilding current metadata: %+v", status)
	}
	go func() { follower <- app.RebuildSessionCatalog() }()
	awaitRemovalTest(t, joined)
	release()
	if err := awaitRemovalTest(t, leader); err != nil {
		t.Fatal(err)
	}
	if err := awaitRemovalTest(t, follower); err != nil {
		t.Fatal(err)
	}
	if refreshed.Load() != 1 {
		t.Fatalf("duplicate metadata refreshes: %d", refreshed.Load())
	}
	status = app.GetSessionCatalogStatus()
	if status.State != "ready" || status.Indexed != 1 || status.RepairPending != 0 || status.LastError != "" {
		t.Fatalf("current repair result: %+v", status)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repair rewrote source: %v", err)
	}
}

func TestCurrentCatalogStopCancelsRepairAndRetainsOutstandingJoin(t *testing.T) {
	app, _, _ := currentCatalogRepairFixture(t)
	started, resume := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(resume) })
	t.Cleanup(release)
	app.lifecycleCheckpointHook = func(phase string) {
		if phase == "current-catalog-before-refresh" {
			close(started)
			<-resume
		}
	}
	done := make(chan error, 1)
	go func() { done <- app.RebuildSessionCatalog() }()
	awaitRemovalTest(t, started)
	if app.stopSessionCatalog(10 * time.Millisecond) {
		t.Fatal("stop forgot the blocked repair")
	}
	release()
	if err := awaitRemovalTest(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled current repair: %v", err)
	}
	if !app.stopSessionCatalog(time.Second) {
		t.Fatal("later stop did not join completed repair")
	}
}

func TestCurrentCatalogRepairFailureCanRetryWithoutChangingSource(t *testing.T) {
	app, source, cache := currentCatalogRepairFixture(t)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(cache)
	if err := os.Rename(dir, dir+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("unavailable cache directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := app.RebuildSessionCatalog(); err == nil {
		t.Fatal("cache write failure reported success")
	}
	failed := app.GetSessionCatalogStatus()
	if failed.State != "degraded" || failed.RepairBlocked != 1 || !failed.CanRebuild {
		t.Fatalf("failed current repair: %+v", failed)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir+".saved", dir); err != nil {
		t.Fatal(err)
	}
	if err := app.RebuildSessionCatalog(); err != nil {
		t.Fatal(err)
	}
	if status := app.GetSessionCatalogStatus(); status.State != "ready" || status.RepairBlocked != 0 || status.LastError != "" {
		t.Fatalf("retry status: %+v", status)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repair failure or retry changed source: %v", err)
	}
}
