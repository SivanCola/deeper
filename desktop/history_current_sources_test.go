package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/history"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/subagentpersist"
)

func readyCurrentDesktopHistory(t *testing.T, app *App) {
	t.Helper()
	if err := app.RebuildHistoryIndex(); err != nil {
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
	roots, err := app.currentHistoryCatalogRoots()
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if err := history.SharedCatalog().ReconcileRoot(t.Context(), root); err != nil {
			t.Fatal(err)
		}
	}
}

func seedCurrentHistoryChild(t *testing.T, app *App, id, parent, text string) string {
	t.Helper()
	children, err := app.desktopSessionService("").Subagents()
	if err != nil {
		t.Fatal(err)
	}
	meta := agent.SubagentMeta{Ref: id, ParentSession: parent, Kind: "task", Name: "task", Status: agent.SubagentCompleted}
	handle, err := subagentpersist.New(children).Create(t.Context(), meta, []provider.Message{{ID: "child-message", Role: provider.RoleAssistant, Content: text}})
	if err != nil {
		t.Fatal(err)
	}
	// Compaction changes continuation context, not the searchable transcript.
	if _, err := handle.RecordSessionModelContext(t.Context(), agent.SessionModelContextCommit{OperationID: "compact", Messages: []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "compressed summary"}}}); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(children.Query().StorageRoot(), id+".meta.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCurrentHistorySearchScopesChildrenAndCatalogIdentity(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	seedCurrentHistorySource(t, app, "sa_same-id", "global", globalWorkspaceRoot(), []provider.Message{{Role: provider.RoleUser, Content: "orchid parent content"}})
	project := t.TempDir()
	seedCurrentHistorySource(t, app, "project-parent", "project", project, []provider.Message{{Role: provider.RoleUser, Content: "orchid project content"}})
	childPath := seedCurrentHistoryChild(t, app, "sa_same-id", "sa_same-id", "orchid child content")
	seedCurrentHistoryChild(t, app, "sa_project-child", "project-parent", "orchid project child")
	seedCurrentHistoryChild(t, app, "sa_orphan-child", "unknown-parent", "orchid orphan")
	readyCurrentDesktopHistory(t, app)
	global := app.SearchHistoryContent(HistorySearchRequest{Query: "orchid", Scope: "global", Limit: 20})
	if global.ReadError != nil || len(global.Items) != 2 {
		t.Fatalf("global = %+v", global)
	}
	var child HistorySearchHit
	for _, hit := range global.Items {
		if hit.SessionID != "sa_same-id" {
			t.Fatalf("foreign source leaked: %+v", hit)
		}
		if hit.Catalog == historySubagentCatalog {
			child = hit
		}
	}
	if child.ContentDigest == "" {
		t.Fatal("child was lost through compaction")
	}
	request := HistorySearchContextRequest{Catalog: child.Catalog, SessionID: child.SessionID, MessageIndex: child.MessageIndex, ContentDigest: child.ContentDigest}
	if lines := app.GetHistorySearchContext(request); len(lines) != 1 || lines[0].Text != "orchid child content" {
		t.Fatalf("child context = %+v", lines)
	}
	request.Catalog = historySessionCatalog
	if lines := app.GetHistorySearchContext(request); len(lines) != 0 {
		t.Fatalf("catalog collision bypassed digest: %+v", lines)
	}
	request.Catalog = "../subagents"
	if lines := app.GetHistorySearchContext(request); len(lines) != 0 {
		t.Fatal("accepted path as catalog")
	}
	projectHits := app.SearchHistoryContent(HistorySearchRequest{Query: "orchid", Scope: "project", WorkspaceRoot: project})
	if projectHits.ReadError != nil || len(projectHits.Items) != 2 {
		t.Fatalf("project = %+v", projectHits)
	}
	for _, hit := range projectHits.Items {
		if hit.WorkspaceRoot != project || hit.Source != "project" {
			t.Fatalf("wrong project: %+v", hit)
		}
	}
	frozen := app.SearchHistoryContent(HistorySearchRequest{Query: "orchid", Scope: "global", Limit: 1})
	if frozen.NextCursor == "" {
		t.Fatalf("missing cursor: %+v", frozen)
	}
	if err := os.Remove(childPath); err != nil {
		t.Fatal(err)
	}
	next := app.SearchHistoryContent(HistorySearchRequest{Query: "orchid", Scope: "global", Limit: 1, Cursor: frozen.NextCursor})
	if !next.StaleCursor {
		t.Fatalf("removed child membership kept cursor: %+v", next)
	}
	request.Catalog = historySubagentCatalog
	if lines := app.GetHistorySearchContext(request); len(lines) != 0 {
		t.Fatal("child without parent metadata remained authorized")
	}
}

func TestCurrentHistorySearchSnapshotSurvivesAppendButFencesReplacement(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(app.closeSessionServices)
	ref := seedCurrentHistorySource(t, app, "append-source", "global", globalWorkspaceRoot(), []provider.Message{
		{Role: provider.RoleUser, Content: "orchid first"}, {Role: provider.RoleUser, Content: "orchid second"}})
	readyCurrentDesktopHistory(t, app)
	req := HistorySearchRequest{Query: "orchid", Scope: "global", Limit: 1}
	first := app.SearchHistoryContent(req)
	if first.ReadError != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first = %+v", first)
	}
	service := app.desktopSessionService("")
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	appendSessionTestMessage(t, binding.Runtime(), "append", provider.Message{ID: "append", Role: provider.RoleUser, Content: "orchid appended newmarker"})
	if err := binding.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	req.Cursor = first.NextCursor
	next := app.SearchHistoryContent(req)
	if next.ReadError != nil || len(next.Items) != 1 || next.NextCursor != "" {
		t.Fatalf("append changed fixed results: %+v", next)
	}
	old := first.Items[0]
	if lines := app.GetHistorySearchContext(HistorySearchContextRequest{Catalog: old.Catalog, SessionID: old.SessionID, MessageIndex: old.MessageIndex, ContentDigest: old.ContentDigest}); len(lines) != 0 {
		t.Fatal("context accepted stale positional digest")
	}
	missing := app.SearchHistoryContent(HistorySearchRequest{Query: "newmarker", Scope: "global"})
	if missing.ReadError != nil || !missing.Partial {
		t.Fatalf("missing index revision not reported: %+v", missing)
	}
	if err := history.SharedCatalog().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	refreshed := app.SearchHistoryContent(HistorySearchRequest{Query: "newmarker", Scope: "global"})
	if refreshed.ReadError != nil || len(refreshed.Items) != 1 {
		t.Fatalf("missing revision was not queued: %+v", refreshed)
	}
	info, err := service.Query().Stat(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(info.Path, filepath.Join(t.TempDir(), "original")); err != nil {
		t.Fatal(err)
	}
	replacement, err := service.Create(t.Context(), session.CreateOptions{SessionID: ref.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Close(t.Context(), replacement.Ref()); err != nil {
		t.Fatal(err)
	}
	if page := app.SearchHistoryContent(req); !page.StaleCursor {
		t.Fatalf("replacement kept old cursor: %+v", page)
	}
}
