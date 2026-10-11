package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/memory"
	"deepzora/internal/sessioncatalog"
)

func TestNewCollidingProjectGetsOwnStateWithoutMovingExistingProject(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	root := t.TempDir()
	first := filepath.Join(root, "front-end", "app")
	second := filepath.Join(root, "front", "end-app")
	plain := filepath.Join(root, "backend")
	for _, path := range []string{first, second, plain} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := addProject(first, ""); err != nil {
		t.Fatal(err)
	}
	firstDir := config.ProjectSessionDir(first)
	if err := os.MkdirAll(firstDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldSession := filepath.Join(firstDir, "existing.jsonl")
	if err := os.WriteFile(oldSession, []byte("existing session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := addProject(second, ""); err != nil {
		t.Fatal(err)
	}
	secondDir := config.ProjectSessionDir(second)
	if secondDir == firstDir {
		t.Fatalf("new project still shares %q", firstDir)
	}
	if got := config.ProjectSessionDir(first); got != firstDir {
		t.Fatalf("existing project moved to %q", got)
	}
	if got, err := os.ReadFile(oldSession); err != nil || string(got) != "existing session\n" {
		t.Fatalf("existing session changed: %q, %v", got, err)
	}
	if err := removeProject(first); err != nil {
		t.Fatal(err)
	}
	if err := addProject(first, ""); err != nil {
		t.Fatal(err)
	}
	if got := config.ProjectSessionDir(first); got != firstDir {
		t.Fatalf("re-added original project moved to %q, want %q", got, firstDir)
	}
	if _, err := os.Stat(filepath.Join(secondDir, "existing.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("new project sees existing session: %v", err)
	}
	if err := addProject(plain, ""); err != nil {
		t.Fatal(err)
	}
	wantPlain := filepath.Join(config.ProjectStateDir(config.MemoryUserDir(), plain), "sessions")
	if got := config.ProjectSessionDir(plain); got != wantPlain {
		t.Fatalf("non-colliding project moved to %q, want %q", got, wantPlain)
	}
	if err := removeProject(second); err != nil {
		t.Fatal(err)
	}
	if err := addProject(second, ""); err != nil {
		t.Fatal(err)
	}
	if got := config.ProjectSessionDir(second); got != secondDir {
		t.Fatalf("re-added project moved to %q, want %q", got, secondDir)
	}
	if got := config.ProjectSessionStoreDir(first); got == config.ProjectSessionStoreDir(second) {
		t.Fatalf("canonical session stores overlap: %q", got)
	}
	if got := config.DesktopTopicStatePath(first); got == config.DesktopTopicStatePath(second) {
		t.Fatalf("topic stores overlap: %q", got)
	}
	if got := memory.StoreFor(config.MemoryUserDir(), first).Dir; got == memory.StoreFor(config.MemoryUserDir(), second).Dir {
		t.Fatalf("project memory stores overlap: %q", got)
	}
	if err := os.MkdirAll(secondDir, 0o700); err != nil {
		t.Fatal(err)
	}
	firstSession := writeTopicSession(t, firstDir, "first.jsonl", "first-topic", "First", first)
	secondSession := writeTopicSession(t, secondDir, "second.jsonl", "second-topic", "Second", second)
	app := NewApp()
	installSessionCatalogForTest(t, app, firstDir, "project", first)
	reconcileSessionCatalogForTest(t, app, secondDir, "project", second)
	for _, tc := range []struct{ root, dir, want, other string }{
		{first, firstDir, firstSession, secondSession},
		{second, secondDir, secondSession, firstSession},
	} {
		page, err := app.sessionCatalog.Load().ListSessions(context.Background(), sessioncatalog.SessionPageRequest{
			Scope: "project", WorkspaceRoot: tc.root, Directory: tc.dir, Limit: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range page.Items {
			if item.Path == tc.other {
				t.Fatalf("project %q lists another project's session %q", tc.root, tc.other)
			}
			found = found || item.Path == tc.want
		}
		if !found {
			t.Fatalf("project %q did not list its session %q: %+v", tc.root, tc.want, page.Items)
		}
	}
}

func TestProjectRegistrationKeepsOpeningOnProjectListLockFailure(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	root := t.TempDir()
	lockPath := filepath.Join(desktopConfigDir(), desktopProjectsFile+".lock")
	if err := os.MkdirAll(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	if err := app.registerProjectRoot(root); err != nil {
		t.Fatalf("project list lock failure blocked opening: %v", err)
	}
}
