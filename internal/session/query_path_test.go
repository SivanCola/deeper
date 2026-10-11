package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueryStorageGenerationUsesConfinedSessionIdentity(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	seed := func(dir, id string) {
		t.Helper()
		stored, err := CreateStore(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := stored.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	seed(filepath.Join(root, "current"), "current")
	outside := filepath.Join(base, "outside")
	seed(outside, "outside")
	query := newQuery("local", NewFilesystemPersistence(root), nil)
	t.Cleanup(query.Close)
	if got := query.storageGeneration("current"); got == "" {
		t.Fatal("current identity lost its storage generation")
	}
	for _, id := range []string{"../outside", outside, `..\outside`, "current/../../outside"} {
		if got := query.storageGeneration(id); got != "" {
			t.Fatalf("path %q read outside storage generation %q", id, got)
		}
	}
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if got := query.storageGeneration("linked"); got != "" {
			t.Fatalf("symlink read outside storage generation %q", got)
		}
	})
}
