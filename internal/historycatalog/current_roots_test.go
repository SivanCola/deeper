package historycatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/provider"
)

func TestCatalogRequiresCurrentQueryAndNeverScansLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "current")
	saveMessages(t, path, provider.Message{Role: provider.RoleUser, Content: "currentneedle"})
	old := filepath.Join(dir, "legacy.jsonl")
	if err := os.WriteFile(old, []byte("invalid historical transcript legacyneedle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(t.Context(), Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	root := testRoot(t, Root{Path: dir, Scope: "global"})
	for _, invalid := range []Root{{Path: dir}, {Path: t.TempDir(), Query: root.Query}} {
		if err := catalog.ReconcileRoot(t.Context(), invalid); err == nil {
			t.Fatal("path-only or mismatched root was accepted")
		}
		if catalog.RegisterRoot(invalid) || catalog.EnqueuePath(invalid, path) {
			t.Fatal("invalid source was queued")
		}
	}
	if err := catalog.ReconcileRoot(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if catalog.EnqueueExisting(t.Context(), path) {
		t.Fatal("database path reconstructed missing read authority")
	}
	if !catalog.RegisterRoot(root) || !catalog.EnqueueExisting(t.Context(), path) {
		t.Fatal("registered current reader was not queued")
	}
	if catalog.EnqueuePath(root, old) {
		// The directory fence admits an opaque ID, but the current query must
		// resolve it before reading. No extension-based file reader may run.
		if err := catalog.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := catalog.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"currentneedle", "legacyneedle"} {
		result, err := catalog.Search(t.Context(), SearchRequest{Query: query})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if query == "currentneedle" {
			want = 1
		}
		if len(result.Items) != want {
			t.Fatalf("query %q returned %d results, want %d", query, len(result.Items), want)
		}
	}
	if body, err := os.ReadFile(old); err != nil || string(body) != "invalid historical transcript legacyneedle\n" {
		t.Fatalf("old source modified: %v", err)
	}
}
