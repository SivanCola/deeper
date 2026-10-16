package main

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/jobs"
)

func TestPurgeCanonicalSessionRemovesOwnedArtifacts(t *testing.T) {
	app, ref := lifecycleFixture(t)
	dir, err := app.desktopSessionService("").SessionDir(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := []string{
		filepath.Join(jobs.ArtifactDir(dir), "job.log"),
		filepath.Join(dir, "inbox", "blobs", "queued.json"),
		filepath.Join(dir, "pinned-context.json"),
	}
	for _, path := range artifacts {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("session-owned content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	for _, path := range artifacts {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("archive lost artifact %s: %v", path, err)
		}
	}
	if err := app.RestoreCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	for _, path := range artifacts {
		if body, err := os.ReadFile(path); err != nil || string(body) != "session-owned content" {
			t.Fatalf("restore lost artifact %s: %q %v", path, body, err)
		}
	}
	if err := app.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	if err := app.PurgeCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("purged session directory remains: %v", err)
	}
}
