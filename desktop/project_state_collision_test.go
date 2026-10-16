package main

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/memory"
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
	if got := memory.StoreFor(config.MemoryUserDir(), first).Dir; got == memory.StoreFor(config.MemoryUserDir(), second).Dir {
		t.Fatalf("project memory stores overlap: %q", got)
	}
	if err := os.MkdirAll(secondDir, 0o700); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	firstSession, _ := createCurrentRegistrySession(t, app, first, "first", true)
	secondSession, _ := createCurrentRegistrySession(t, app, second, "second", true)
	for _, tc := range []struct{ root, want, other string }{
		{first, firstSession.SessionID, secondSession.SessionID},
		{second, secondSession.SessionID, firstSession.SessionID},
	} {
		page, err := app.ListProjectTopics(ProjectTopicPageRequest{Scope: "project", WorkspaceRoot: tc.root, Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].Session == nil || page.Items[0].Session.SessionID != tc.want {
			t.Fatalf("current project membership: %+v", page)
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
