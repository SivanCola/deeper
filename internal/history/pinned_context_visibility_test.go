package history

import (
	"context"
	"strings"
	"testing"

	"deepzora/internal/historycatalog"
	"deepzora/internal/provider"
)

func TestHistorySurfacesExcludePinnedContextRevisions(t *testing.T) {
	service := currentHistoryStore(t)
	revision := provider.Message{
		Role: provider.RoleUser, Origin: provider.MessageOriginHost,
		Content: "<pinned_context_revision>private pinned body</pinned_context_revision>",
	}
	saveCurrentHistory(t, service, "pinned", revision, provider.Message{Role: provider.RoleUser, Content: "visible question"})

	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(context.Background(), SearchRequest{Query: "private pinned body"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search() returned pinned revision hits: %#v", hits)
	}
	around, err := searcher.Around(context.Background(), AroundRequest{CatalogID: catalogID(service.Query()), SessionID: "pinned", ContentDigest: currentDigest(t, service, "pinned"), MessageIndex: 1, Before: 1, After: 1})
	if err != nil {
		t.Fatalf("Around() error = %v", err)
	}
	if len(around) != 1 || strings.Contains(around[0].Text, "private pinned body") {
		t.Fatalf("Around() exposed pinned revision: %#v", around)
	}
	if _, ok := candidateText([]provider.Message{revision}, historycatalog.Candidate{MessageIndex: 0, Kind: string(KindUserText)}); ok {
		t.Fatal("candidateText accepted a stale pinned-revision index entry")
	}
}
