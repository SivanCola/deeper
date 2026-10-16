package history

import (
	"context"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/subagentpersist"
)

func TestSearchSubagentsUsesCurrentHistoryAndParentMembership(t *testing.T) {
	parent := currentHistoryStore(t)
	saveCurrentHistory(t, parent, "allowed", provider.Message{Role: provider.RoleUser, Content: "parent task"})
	saveCurrentHistory(t, parent, "hidden", provider.Message{Role: provider.RoleUser, Content: "other task"})
	children, err := parent.Subagents()
	if err != nil {
		t.Fatal(err)
	}
	store := agent.NewSubagentStore(subagentpersist.New(children))
	var visible string
	for _, owner := range []string{"allowed", "hidden"} {
		run, err := store.PrepareFresh(agent.SubagentSpec{ParentSession: owner, Kind: "task", Name: "task", SystemPrompt: "system"})
		if err != nil {
			t.Fatal(err)
		}
		if err := run.Persistence.RecordSessionMessages(t.Context(), "answer", []provider.Message{{ID: "answer", Role: provider.RoleAssistant, Content: "orchid durable finding"}}); err != nil {
			t.Fatal(err)
		}
		// Search must retain full history even when continuation has a summary.
		_, err = run.Persistence.RecordSessionModelContext(t.Context(), agent.SessionModelContextCommit{OperationID: "compact", Messages: []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "compressed"}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SaveCompleted(run); err != nil {
			t.Fatal(err)
		}
		if err := run.Release(); err != nil {
			t.Fatal(err)
		}
		if owner == "allowed" {
			visible = run.Ref
		}
	}
	allowed := map[string]bool{"allowed": true}
	opts := currentOptions(parent)
	opts.Sources[0].Membership = func(context.Context) (map[string]bool, error) { return allowed, nil }
	searcher := newReadyIndexedSearcher(t, opts)
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid", Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].SessionID != visible {
		t.Fatalf("child search = %+v, %v", hits, err)
	}
	hit := hits[0]
	if _, err := searcher.Around(t.Context(), AroundRequest{CatalogID: hit.CatalogID, SessionID: hit.SessionID, ContentDigest: hit.ContentDigest, MessageIndex: hit.MessageIndex}); err != nil {
		t.Fatal(err)
	}
	allowed = nil
	if _, err := searcher.Around(t.Context(), AroundRequest{CatalogID: hit.CatalogID, SessionID: hit.SessionID, ContentDigest: hit.ContentDigest, MessageIndex: hit.MessageIndex}); err == nil {
		t.Fatal("child around bypassed revoked parent membership")
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("nil parent membership widened: %+v, %v", hits, err)
	}
}
