package historycatalog

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"deepzora/internal/provider"
)

func TestConcurrentCurrentReconcilesDoNotHidePresentSources(t *testing.T) {
	dir := t.TempDir()
	for i := range 12 {
		saveMessages(t, filepath.Join(dir, fmt.Sprintf("source-%02d", i)), provider.Message{Role: provider.RoleUser, Content: "concurrentsource"})
	}
	root := testRoot(t, Root{Path: dir, Scope: "global"})
	catalog, err := Open(t.Context(), Options{Path: filepath.Join(t.TempDir(), "index.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	gate := make(chan struct{})
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			<-gate
			for range 3 {
				if err := catalog.ReconcileRoot(t.Context(), root); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	close(gate)
	workers.Wait()
	result, err := catalog.Search(t.Context(), SearchRequest{Query: "concurrentsource", Limit: 50})
	if err != nil || len(result.Items) != 12 {
		t.Fatalf("present sources disappeared after concurrent scans: hits=%d, err=%v", len(result.Items), err)
	}
	var missing int
	if err := catalog.db.QueryRow(`SELECT count(*) FROM history_sources WHERE missing_since<>0 OR health<>'ok'`).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("false missing-source state: %d, %v", missing, err)
	}
}

func TestQueuedCurrentUpdatePreservesDiscoveryGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source")
	saveMessages(t, path, provider.Message{Role: provider.RoleUser, Content: "first"})
	root := testRoot(t, Root{Path: dir, Scope: "global"})
	catalog, err := Open(t.Context(), Options{InMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close(context.Background()) })
	if err := catalog.ReconcileRoot(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := catalog.db.QueryRow(`SELECT seen_generation FROM history_sources WHERE path=?`, path).Scan(&before); err != nil || before == 0 {
		t.Fatalf("initial generation=%d, %v", before, err)
	}
	appendFixtureMessage(t, path, provider.Message{Role: provider.RoleAssistant, Content: "second"})
	if err := catalog.indexQueuedSession(t.Context(), root, path); err != nil {
		t.Fatal(err)
	}
	if err := catalog.db.QueryRow(`SELECT seen_generation FROM history_sources WHERE path=?`, path).Scan(&after); err != nil || after != before {
		t.Fatalf("queued content update reset discovery: %d -> %d, %v", before, after, err)
	}
}
