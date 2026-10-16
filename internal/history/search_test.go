package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/historycatalog"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func currentHistoryStore(t *testing.T) *session.Service {
	t.Helper()
	service, err := session.NewService("local", session.NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	return service
}
func saveCurrentHistory(t *testing.T, service *session.Service, id string, messages ...provider.Message) {
	t.Helper()
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	events := []session.Event{}
	for i, message := range messages {
		if message.ID == "" {
			message.ID = fmt.Sprintf("message-%d", i)
		}
		payload, err := json.Marshal(map[string]any{"message": message})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, session.Event{Kind: "message/complete", Payload: payload})
	}
	if len(events) > 0 {
		if _, err := runtime.Session().AppendBatch(t.Context(), "seed", events); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
}
func currentOptions(service *session.Service) Options {
	return Options{Sources: []Source{{Query: service.Query(), Scope: scopeProject}}}
}
func currentDigest(t *testing.T, service *session.Service, id string) string {
	t.Helper()
	_, digest, _, err := historycatalog.ReadCurrent(t.Context(), service.Query(), session.SessionRef{HostID: service.HostID(), SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestSearchRanksCurrentHistoryAndKeepsColdSessionsCold(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "decision", provider.Message{Role: provider.RoleUser, Content: "Should history use vector embeddings?"}, provider.Message{Role: provider.RoleAssistant, Content: "Keep lightweight BM25 retrieval without a vector database."})
	saveCurrentHistory(t, service, "other", provider.Message{Role: provider.RoleUser, Content: "dashboard colors"})
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "BM25 vector database"})
	if err != nil || len(hits) == 0 || hits[0].SessionID != "decision" || hits[0].MessageIndex != 1 || hits[0].Kind != KindAssistantText {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	around, err := searcher.Around(t.Context(), AroundRequest{CatalogID: catalogID(service.Query()), SessionID: hits[0].SessionID, ContentDigest: hits[0].ContentDigest, MessageIndex: 1})
	if err != nil || len(around) != 2 || !strings.Contains(around[0].Text, "[0 user]") {
		t.Fatalf("around=%+v err=%v", around, err)
	}
	if _, live := service.Runtime(session.SessionRef{HostID: "local", SessionID: "decision"}); live {
		t.Fatal("history acquired a cold session writer")
	}
}

func TestSearchUsesDynamicMembershipBeforeRankingAndAround(t *testing.T) {
	service := currentHistoryStore(t)
	for i := range 105 {
		saveCurrentHistory(t, service, fmt.Sprintf("hidden-%03d", i), provider.Message{Role: provider.RoleUser, Content: "orchid secret"})
	}
	saveCurrentHistory(t, service, "visible", provider.Message{Role: provider.RoleUser, Content: "orchid visible"})
	allowed := map[string]bool{"visible": true}
	opts := currentOptions(service)
	opts.Sources[0].Membership = func(context.Context) (map[string]bool, error) { return allowed, nil }
	searcher := newReadyIndexedSearcher(t, opts)
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid", Limit: 1})
	if err != nil || len(hits) != 1 || hits[0].SessionID != "visible" {
		t.Fatalf("authorized hit starved by other catalog rows: %+v %v", hits, err)
	}
	allowed = map[string]bool{}
	if _, err := searcher.Around(t.Context(), AroundRequest{CatalogID: catalogID(service.Query()), SessionID: "visible", ContentDigest: hits[0].ContentDigest, MessageIndex: 0}); err == nil {
		t.Fatal("around ignored membership removal")
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("empty membership widened: %+v %v", hits, err)
	}
}

func TestSearchGlobalUsesOnlyHostGrantedCatalogs(t *testing.T) {
	project, global, foreign := currentHistoryStore(t), currentHistoryStore(t), currentHistoryStore(t)
	saveCurrentHistory(t, project, "project", provider.Message{Role: provider.RoleUser, Content: "local task"})
	saveCurrentHistory(t, global, "archived", provider.Message{Role: provider.RoleUser, Content: "orchid archived decision"})
	saveCurrentHistory(t, foreign, "foreign", provider.Message{Role: provider.RoleUser, Content: "orchid forbidden"})
	opts := currentOptions(project)
	opts.Sources = append(opts.Sources, Source{Query: global.Query(), Scope: scopeGlobal})
	searcher := newReadyIndexedSearcher(t, opts)
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("project widened: %+v %v", hits, err)
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid", Scope: scopeGlobal})
	if err != nil || len(hits) != 1 || hits[0].SessionID != "archived" || hits[0].Source != scopeGlobal {
		t.Fatalf("global hits: %+v %v", hits, err)
	}
	if _, err := searcher.Around(t.Context(), AroundRequest{CatalogID: catalogID(foreign.Query()), SessionID: "foreign", ContentDigest: currentDigest(t, foreign, "foreign"), MessageIndex: 0}); err == nil {
		t.Fatal("read a foreign catalog")
	}
}

func TestSearchIndexesCurrentToolInputsErrorsAndExplicitOutputs(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "tools", provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call", Name: "bash", Arguments: `{"command":"orchid"}`}}}, provider.Message{Role: provider.RoleTool, ToolCallID: "call", Name: "bash", Content: "Error: orchid permission denied"}, provider.Message{Role: provider.RoleTool, ToolCallID: "other", Name: "read_file", Content: "orchid ordinary output"})
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[Kind]bool{}
	for _, hit := range hits {
		seen[hit.Kind] = true
	}
	if !seen[KindToolInput] || !seen[KindToolError] || seen[KindToolOutput] {
		t.Fatalf("default kinds: %+v", hits)
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid", Kinds: []Kind{KindToolOutput}, ToolName: "read_file"})
	if err != nil || len(hits) != 1 || hits[0].ToolName != "read_file" || hits[0].MessageIndex != 2 {
		t.Fatalf("outputs: %+v %v", hits, err)
	}
}

func TestSearchDropsCommonWordNoise(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "rare", provider.Message{Role: provider.RoleUser, Content: "rareterm common common common"})
	for i := range 12 {
		saveCurrentHistory(t, service, fmt.Sprintf("common-%d", i), provider.Message{Role: provider.RoleUser, Content: "common"})
	}
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "rareterm common", Limit: 20})
	if err != nil || len(hits) != 1 || hits[0].SessionID != "rare" {
		t.Fatalf("noise filter: %+v %v", hits, err)
	}
}

func TestAroundRejectsStaleDigestAndSearchRefreshesCurrentProjection(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "changing", provider.Message{Role: provider.RoleUser, Content: "old orchid decision"})
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits: %+v %v", hits, err)
	}
	ref := session.SessionRef{HostID: "local", SessionID: "changing"}
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(t.Context())
	runtime, ok := service.Runtime(ref)
	if !ok {
		t.Fatal("opened session has no runtime")
	}
	payload := json.RawMessage(`{"message":{"id":"message-0","role":"user","content":"new phoenix decision"}}`)
	if _, err := runtime.Session().AppendBatch(t.Context(), "replace", []session.Event{{Kind: "message/upsert", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A term absent from the old index must queue the current revision without
	// receiving a path-save observer hint or first matching the old content.
	fresh, err := searcher.Search(t.Context(), SearchRequest{Query: "phoenix"})
	if err != nil || len(fresh) != 0 || searcher.IndexStatus().State != "building" {
		t.Fatalf("unindexed current revision: %+v %v %+v", fresh, err, searcher.IndexStatus())
	}
	if _, err := searcher.Around(t.Context(), AroundRequest{CatalogID: catalogID(service.Query()), SessionID: "changing", ContentDigest: hits[0].ContentDigest, MessageIndex: 0}); err == nil {
		t.Fatal("stale positional hit accepted")
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale hit returned: %+v %v", hits, err)
	}
	if err := FlushSharedCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "phoenix"})
	if err != nil || len(hits) != 1 || !strings.Contains(hits[0].Snippet, "phoenix") {
		t.Fatalf("refresh: %+v %v", hits, err)
	}
}

func TestAroundClampsLargeWindowAndRejectsMissingDigest(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "nearby", provider.Message{Role: provider.RoleUser, Content: "first"}, provider.Message{Role: provider.RoleAssistant, Content: "second"})
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	req := AroundRequest{CatalogID: catalogID(service.Query()), SessionID: "nearby", MessageIndex: 0, After: int(^uint(0) >> 1)}
	if _, err := searcher.Around(t.Context(), req); err == nil {
		t.Fatal("missing digest accepted")
	}
	req.ContentDigest = currentDigest(t, service, "nearby")
	lines, err := searcher.Around(t.Context(), req)
	if err != nil || len(lines) != 2 || lines[1].Index != 1 {
		t.Fatalf("around: %+v %v", lines, err)
	}
}

func TestCurrentHistoryIgnoresOldFilesAndReportsUnindexedSessions(t *testing.T) {
	service := currentHistoryStore(t)
	if err := os.MkdirAll(service.Query().StorageRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(service.Query().StorageRoot(), "old.jsonl")
	oldBytes := []byte("{\"role\":\"user\",\"content\":\"obsolete orchid\"}\n")
	if err := os.WriteFile(oldPath, oldBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("old data entered current catalog: %+v %v", hits, err)
	}
	if err := FlushSharedCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	saveCurrentHistory(t, service, "new", provider.Message{Role: provider.RoleUser, Content: "current orchid"})
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 0 || searcher.IndexStatus().State != "building" {
		t.Fatalf("new source should report pending index: %+v %v %+v", hits, err, searcher.IndexStatus())
	}
	if err := FlushSharedCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	hits, err = searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 1 || hits[0].SessionID != "new" {
		t.Fatalf("new current source not indexed: %+v %v", hits, err)
	}
	got, err := os.ReadFile(oldPath)
	if err != nil || string(got) != string(oldBytes) {
		t.Fatalf("query rewrote old data: %q %v", got, err)
	}
}

func TestHistoryRetainsCompactedConversation(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "compacted", provider.Message{Role: provider.RoleUser, Content: "original orchid decision"})
	ref := session.SessionRef{HostID: "local", SessionID: "compacted"}
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release(t.Context())
	runtime, _ := service.Runtime(ref)
	payload := json.RawMessage(`{"messages":[{"id":"summary","role":"user","content":"short summary"}]}`)
	if _, err := runtime.Session().AppendBatch(t.Context(), "compact", []session.Event{{Kind: "model/context-replace", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	searcher := newReadyIndexedSearcher(t, currentOptions(service))
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("compacted history was lost: %+v %v", hits, err)
	}
	lines, err := searcher.Around(t.Context(), AroundRequest{CatalogID: catalogID(service.Query()), SessionID: hits[0].SessionID, ContentDigest: hits[0].ContentDigest, MessageIndex: hits[0].MessageIndex})
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0].Text, "original orchid decision") {
		t.Fatalf("compacted context: %+v %v", lines, err)
	}
}

func TestGlobalHistoryDisambiguatesEqualIDsAcrossCatalogs(t *testing.T) {
	project, global := currentHistoryStore(t), currentHistoryStore(t)
	saveCurrentHistory(t, project, "shared", provider.Message{Role: provider.RoleUser, Content: "orchid project"})
	saveCurrentHistory(t, global, "shared", provider.Message{Role: provider.RoleUser, Content: "orchid global"})
	opts := currentOptions(project)
	opts.Sources = append(opts.Sources, Source{Query: global.Query(), Scope: scopeGlobal})
	searcher := newReadyIndexedSearcher(t, opts)
	hits, err := searcher.Search(t.Context(), SearchRequest{Query: "orchid", Scope: scopeGlobal})
	if err != nil || len(hits) != 2 || hits[0].CatalogID == hits[1].CatalogID {
		t.Fatalf("ambiguous results: %+v %v", hits, err)
	}
	for _, hit := range hits {
		lines, err := searcher.Around(t.Context(), AroundRequest{CatalogID: hit.CatalogID, SessionID: hit.SessionID, ContentDigest: hit.ContentDigest, MessageIndex: hit.MessageIndex})
		if err != nil || len(lines) != 1 || !strings.Contains(lines[0].Text, "orchid "+hit.Source) {
			t.Fatalf("wrong catalog body: %+v %v %+v", lines, err, hit)
		}
	}
}
