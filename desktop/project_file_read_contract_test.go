package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectFileReadDoesNotCreateOrganizationSidecar(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := desktopProjectFile{GlobalTitle: "Existing workspace"}
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, desktopProjectsFile)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if loaded := loadProjectsFile(); loaded.GlobalTitle != file.GlobalTitle {
		t.Fatalf("project metadata changed: %+v", loaded)
	}
	if _, err := os.Stat(filepath.Join(dir, "desktop-project-tree-organization.json")); !os.IsNotExist(err) {
		t.Fatalf("read created an organization sidecar: %v", err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, body) {
		t.Fatalf("read rewrote project metadata: %v", err)
	}
}

func TestProjectFileReadersIgnoreRetiredOrganizationSidecar(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "desktop-project-tree-organization.json")
	body := []byte("retired data")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveProjectsFile(loadProjectsFile()); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, body) {
		t.Fatalf("retired sidecar changed: %v", err)
	}
}
