package history

import (
	"context"
	"testing"

	"deepzora/internal/historycatalog"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func newEmptyCurrentRoot(t *testing.T) historycatalog.Root {
	t.Helper()
	service, err := session.NewService("history-test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return historycatalog.CurrentRoot(service.Query())
}

func prepareHistoryIndex(t *testing.T) {
	t.Helper()
	if err := CloseSharedCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	processHistoryCatalog.mu.Lock()
	previous := processHistoryCatalog.open
	processHistoryCatalog.open = func(ctx context.Context, opts historycatalog.Options) (*historycatalog.Catalog, error) {
		opts.Path, opts.InMemory = "", true
		return historycatalog.Open(ctx, opts)
	}
	processHistoryCatalog.mu.Unlock()
	t.Cleanup(func() {
		if err := CloseSharedCatalog(context.Background()); err != nil {
			t.Error(err)
		}
		processHistoryCatalog.mu.Lock()
		processHistoryCatalog.open = previous
		processHistoryCatalog.mu.Unlock()
	})
}

func awaitHistoryIndex(t *testing.T) {
	t.Helper()
	waitManagerOpen(t, &processHistoryCatalog)
	if SharedCatalog() == nil {
		t.Fatal("history index did not open")
	}
	if err := FlushSharedCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func newReadyIndexedSearcher(t *testing.T, opts Options) *IndexedSearcher {
	t.Helper()
	prepareHistoryIndex(t)
	searcher := NewIndexedSearcher(opts)
	awaitHistoryIndex(t)
	return searcher
}

func newReadyIndexedTool(t *testing.T, opts Options) tool.Tool {
	t.Helper()
	prepareHistoryIndex(t)
	result := NewIndexedTool(opts)
	awaitHistoryIndex(t)
	return result
}
