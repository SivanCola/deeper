package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"deepzora/internal/history"
	"deepzora/internal/historycatalog"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestHistorySessionSnapshotKeepsOrderAndFencesRemoval(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), "global", globalWorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	service := app.desktopSessionService("")
	for i := range 405 {
		id := fmt.Sprintf("%03d", i)
		runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Close(t.Context(), runtime.Ref()); err != nil {
			t.Fatal(err)
		}
		if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, id, ""); err != nil {
			t.Fatal(err)
		}
		if err := app.workspaceRegistry().EnsureSessionTopic(t.Context(), id, id, id); err != nil {
			t.Fatal(err)
		}
	}
	req := HistorySessionPageRequest{Scope: "global", Limit: 200}
	first := app.ListHistorySessions(req)
	if first.ReadError != nil || len(first.Items) != 200 || first.NextCursor == "" {
		t.Fatalf("first=%+v", first)
	}
	changed := session.SessionRef{HostID: localDesktopHostID, SessionID: "000"}
	if err := service.SetTitle(t.Context(), changed, "Updated"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range first.Items {
		seen[row.Path] = true
	}
	req.Cursor = first.NextCursor
	for req.Cursor != "" {
		page := app.ListHistorySessions(req)
		if page.ReadError != nil || page.StaleCursor {
			t.Fatalf("page=%+v", page)
		}
		for _, row := range page.Items {
			if seen[row.Path] || row.Title == "Updated" {
				t.Fatalf("live order leaked: %+v", row)
			}
			seen[row.Path] = true
		}
		req.Cursor = page.NextCursor
	}
	if len(seen) != 405 {
		t.Fatalf("rows=%d", len(seen))
	}
	for _, id := range []string{"000", "001", "000"} {
		app.tabs[id] = &WorkspaceTab{ID: id, Scope: "global", SessionID: id, TopicID: id}
		app.activeTabID = id
		if page := app.ListHistorySessions(HistorySessionPageRequest{Scope: "global", Cursor: first.NextCursor, Limit: 200}); !page.StaleCursor {
			t.Fatal("metadata cursor crossed active-session switch")
		}
		current := app.ListHistorySessions(HistorySessionPageRequest{Scope: "global", Status: "current", Limit: 200})
		if current.ReadError != nil || len(current.Items) != 1 || current.Items[0].SessionID != id || !current.Items[0].Current {
			t.Fatalf("current metadata after A-B-A: %+v", current)
		}
	}
	app.activeTabID = ""
	fresh := app.ListHistorySessions(req)
	found := false
	for _, row := range fresh.Items {
		if row.SessionID == "000" {
			found = row.Title == "Updated"
		}
	}
	if !found {
		t.Fatal("refresh did not expose updated title/activity")
	}
	if err := app.workspaceRegistry().ArchiveSession(t.Context(), "000"); err != nil {
		t.Fatal(err)
	}
	req.Cursor = first.NextCursor
	if page := app.ListHistorySessions(req); !page.StaleCursor {
		t.Fatal("archived source was not fenced")
	}
}

func TestHistoryContentSearchDefaultsCoverEveryToolResultOnce(t *testing.T) {
	isolateDesktopUserDirs(t)
	messages := []provider.Message{{Role: provider.RoleUser, Content: "list the servers"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "ok", Name: "mcp_list", Arguments: "{}"},
			{ID: "bad", Name: "mcp_list", Arguments: "{}"},
		}},
		{Role: provider.RoleTool, ToolCallID: "ok", Name: "mcp_list", Content: "servers: alpha zulumarker"},
		{Role: provider.RoleTool, ToolCallID: "bad", Name: "mcp_list", Content: "error: quebecmarker unreachable"}}
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	seedCurrentHistorySource(t, app, "tools", "global", globalWorkspaceRoot(), messages)
	root := historycatalog.CurrentRoot(app.desktopSessionService("").Query())
	if err := history.RebuildSharedCatalog(t.Context(), []historycatalog.Root{root}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.CloseSharedCatalog(context.Background()) })
	deadline := time.After(5 * time.Second)
	for history.SharedCatalog() == nil {
		select {
		case <-deadline:
			t.Fatal("history catalog did not open")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := history.SharedCatalog().ReconcileRoot(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"zulumarker", "quebecmarker"} {
		page := app.SearchHistoryContent(HistorySearchRequest{Query: query, Scope: "global", Limit: 50})
		if page.ReadError != nil || len(page.Items) != 1 || page.Items[0].Kind != "tool_output" {
			t.Fatalf("query %q: want exactly one tool_output hit, got %+v", query, page)
		}
		hit := page.Items[0]
		if hit.ContentDigest == "" {
			t.Fatal("search hit has no source identity")
		}
		req := HistorySearchContextRequest{Catalog: hit.Catalog, SessionID: hit.SessionID, MessageIndex: hit.MessageIndex, ContentDigest: hit.ContentDigest, Before: 1, After: 1}
		lines := app.GetHistorySearchContext(req)
		found := false
		for _, line := range lines {
			if line.Index == hit.MessageIndex && line.Role == "tool" && strings.Contains(line.Text, query) {
				found = true
			}
		}
		if !found {
			t.Fatalf("context lost search hit %q: %+v", query, lines)
		}
		for _, digest := range []string{"", "stale-digest"} {
			req.ContentDigest = digest
			if lines := app.GetHistorySearchContext(req); len(lines) != 0 {
				t.Fatalf("context read accepted absent/stale source identity: %+v", lines)
			}
		}
		req.ContentDigest = hit.ContentDigest
		for _, index := range []int{-1, int(^uint(0) >> 1)} {
			req.MessageIndex = index
			if lines := app.GetHistorySearchContext(req); len(lines) != 0 {
				t.Fatalf("context accepted invalid message index %d: %+v", index, lines)
			}
		}
	}
}

func TestHistorySearchBuildReadsEachSessionOnceAndKeepsRankOrder(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	digests := map[string]string{}
	paths := []string{}
	for _, id := range []string{"a", "b"} {
		ref := seedCurrentHistorySource(t, app, id, "global", globalWorkspaceRoot(), []provider.Message{
			{Role: provider.RoleUser, Content: "first interleave"}, {Role: provider.RoleUser, Content: "second interleave"}})
		_, digest, info, err := historycatalog.ReadCurrent(t.Context(), app.desktopSessionService("").Query(), ref)
		if err != nil {
			t.Fatal(err)
		}
		paths, digests[info.Path] = append(paths, info.Path), digest
	}
	loads := 0
	original := loadHistorySearchMessages
	loadHistorySearchMessages = func(ctx context.Context, query *session.Query, ref session.SessionRef) ([]provider.Message, string, session.SessionInfo, error) {
		loads++
		return original(ctx, query, ref)
	}
	t.Cleanup(func() { loadHistorySearchMessages = original })
	store := &app.desktopSessions.readSnapshots
	candidates, out := &readSnapshot{}, &readSnapshot{}
	t.Cleanup(func() { store.dispose(candidates); store.dispose(out) })
	ranked := []historycatalog.Candidate{
		{SessionPath: paths[0], MessageIndex: 0, Kind: "user_text"},
		{SessionPath: paths[1], MessageIndex: 1, Kind: "user_text"},
		{SessionPath: paths[0], MessageIndex: 1, Kind: "user_text"},
		{SessionPath: paths[1], MessageIndex: 0, Kind: "user_text"},
	}
	for _, row := range ranked {
		row.ContentDigest = digests[row.SessionPath]
		if err := store.append(t.Context(), candidates, row); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := app.currentHistorySources(t.Context(), HistorySearchRequest{Scope: "all"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fence := &historyCurrentFence{app: app, entries: map[string]historyCurrentSource{}, generations: map[string]string{}}
	build := &historySearchSnapshotBuild{ctx: t.Context(), app: app, store: store, snapshot: out, request: HistorySearchRequest{Query: "interleave"}, fence: fence, entries: entries, terms: []string{"interleave"}, cutoff: time.Now()}
	if err := build.run(candidates); err != nil {
		t.Fatal(err)
	}
	var got []HistorySearchHit
	if err := out.walk(t.Context(), func(b []byte) error {
		var hit HistorySearchHit
		if err := json.Unmarshal(b, &hit); err != nil {
			return err
		}
		got = append(got, hit)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if loads != len(paths) {
		t.Fatalf("session loads = %d, want %d (one per session)", loads, len(paths))
	}
	if len(got) != len(ranked) {
		t.Fatalf("hits = %+v", got)
	}
	for i, hit := range got {
		if hit.SessionID != entries[ranked[i].SessionPath].info.SessionID || hit.Catalog != historySessionCatalog || hit.MessageIndex != ranked[i].MessageIndex || hit.Snippet == "" {
			t.Fatalf("hit %d = %+v, want rank order %+v", i, hit, ranked[i])
		}
	}
}

func seedCurrentHistorySource(t *testing.T, app *App, id, scope, root string, messages []provider.Message) session.SessionRef {
	t.Helper()
	service := app.desktopSessionService("")
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	for i, message := range messages {
		message.ID = fmt.Sprintf("%s-%d", id, i)
		appendSessionTestMessage(t, runtime, message.ID, message)
	}
	if err := service.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	workspaceID, err := app.ensureDesktopWorkspace(t.Context(), scope, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.workspaceRegistry().AttachSession(t.Context(), "", workspaceID, id, ""); err != nil {
		t.Fatal(err)
	}
	return runtime.Ref()
}
