package workspacestate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRejectsPastVersionsWithoutRewriting(t *testing.T) {
	for _, version := range []int{1, 2} {
		path := filepath.Join(t.TempDir(), "state.json")
		raw := fmt.Appendf(nil, "{\"version\":%d}", version)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		store := NewStore(path)
		if _, err := store.Load(t.Context()); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("load: %v", err)
		}
		if err := store.RenameWorkspace(t.Context(), GlobalWorkspaceID, "Renamed"); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("edit: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatal("rejected registry changed")
		}
	}
}
