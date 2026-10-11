package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func queryPathFixture(t *testing.T) (*Query, string, string) {
	t.Helper()
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
	return query, root, outside
}

func TestQueryStorageGenerationUsesConfinedSessionIdentity(t *testing.T) {
	query, root, outside := queryPathFixture(t)
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

func TestQueryDerivedIndexesRejectUnconfinedIdentities(t *testing.T) {
	for name, ensure := range map[string]func(context.Context, *FilesystemPersistence, string, string) error{
		"history": ensureHistoryIndex,
		"search":  ensureSearchIndex,
	} {
		t.Run(name, func(t *testing.T) {
			query, root, outside := queryPathFixture(t)
			persistence := query.persistence.(*FilesystemPersistence)
			cache := filepath.Join(root, ".query-cache", "probe", name+".sqlite")
			if err := ensure(t.Context(), persistence, "current", cache); err != nil {
				t.Fatalf("current identity: %v", err)
			}
			ids := []string{"../outside", outside, `..\outside`}
			if err := os.Symlink(outside, filepath.Join(root, "linked")); err == nil {
				ids = append(ids, "linked")
			} else {
				t.Logf("symlink unavailable: %v", err)
			}
			for _, id := range ids {
				rejectedCache := filepath.Join(root, ".query-cache", "rejected", name+".sqlite")
				if err := ensure(t.Context(), persistence, id, rejectedCache); err == nil {
					t.Fatalf("index read unconfined identity %q", id)
				}
				if _, err := os.Stat(filepath.Dir(rejectedCache)); !os.IsNotExist(err) {
					t.Fatalf("rejected identity created a cache: %v", err)
				}
			}
		})
	}
}
