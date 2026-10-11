package main

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/config"
)

func seedLegacyTopicBridge(t *testing.T, workspaceRoot string) {
	t.Helper()
	for _, path := range legacyTopicPaths(workspaceRoot) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFreshTopicStateUsesSQLiteWithoutLegacyFiles(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspaceRoot := t.TempDir()
	if err := setTopicTitle(workspaceRoot, "topic-fresh", "Fresh"); err != nil {
		t.Fatal(err)
	}
	if got := loadTopicTitle(workspaceRoot, "topic-fresh"); got != "Fresh" {
		t.Fatalf("title = %q", got)
	}
	if _, err := os.Stat(config.DesktopTopicStatePath(workspaceRoot)); err != nil {
		t.Fatalf("SQLite topic state missing: %v", err)
	}
	for _, path := range legacyTopicPaths(workspaceRoot) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("fresh workspace created legacy file %s: %v", filepath.Base(path), err)
		}
	}
}
