package main

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/store"
)

func TestRemoveDesktopSessionArtifactsRemovesPinnedContext(t *testing.T) {
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := savePinnedContextState(sessionPath, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	if err := removeDesktopSessionArtifacts(sessionPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.SessionPinnedContext(sessionPath)); !os.IsNotExist(err) {
		t.Fatalf("pinned sidecar survived permanent removal: %v", err)
	}
}
