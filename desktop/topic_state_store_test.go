package main

import (
	"os"
	"testing"

	"deepzora/internal/config"
)

func TestFreshTopicStateUsesSQLite(t *testing.T) {
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
}
