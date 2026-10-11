package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A blocked parent prevents authoritative appends on every platform. Replacing
// only the JSONL display cache with a directory may leave a valid durable save.
func blockedPersistenceSessionPath(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, "session.jsonl")
}
