package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	file := desktopProjectFile{GlobalManualTopicOrder: true, GlobalTopics: []string{"existing-topic"}}
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, desktopProjectsFile)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if loaded := loadProjectsFile(); len(loaded.GlobalTopics) != 1 {
		t.Fatalf("project metadata changed: %+v", loaded)
	}
	if _, err := readTopicRemovalProjects(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, desktopProjectOrganizationFile)); !os.IsNotExist(err) {
		t.Fatalf("read created an organization sidecar: %v", err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, body) {
		t.Fatalf("read rewrote project metadata: %v", err)
	}
}

func TestProjectFileReadersRejectUnknownOrganizationVersion(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := desktopConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, desktopProjectOrganizationFile)
	for _, version := range []int{0, desktopProjectOrganizationVersion, desktopProjectOrganizationVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			body, err := json.Marshal(desktopProjectOrganizationFileData{Version: version})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			current := version == desktopProjectOrganizationVersion
			if _, ok := loadProjectOrganizationFile(); ok != current {
				t.Fatalf("display reader accepted version %d: %v", version, ok)
			}
			if _, err := readTopicRemovalProjects(); (err == nil) != current {
				t.Fatalf("removal reader accepted version %d: %v", version, err)
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, body) {
				t.Fatalf("read rewrote organization data: %v", err)
			}
		})
	}
}
