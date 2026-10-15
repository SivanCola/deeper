package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestPromptHistoryUsesCurrentSessionAndWorkspace(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	root, other := t.TempDir(), t.TempDir()
	current := writeSuggestionSession(t, app, root, "current", provider.Message{Role: provider.RoleUser, Content: "first", CreatedAt: 100}, provider.Message{Role: provider.RoleUser, Content: "second", CreatedAt: 200})
	writeSuggestionSession(t, app, root, "recent", provider.Message{Role: provider.RoleUser, Content: "other session", CreatedAt: 300})
	writeSuggestionSession(t, app, other, "other-workspace", provider.Message{Role: provider.RoleUser, Content: "must stay isolated"})
	archived := writeSuggestionSession(t, app, root, "archived", provider.Message{Role: provider.RoleUser, Content: "must stay archived"})
	if err := app.ArchiveCanonicalSession(archived); err != nil {
		t.Fatal(err)
	}
	app.tabs["active"] = &WorkspaceTab{ID: "active", Scope: "project", WorkspaceRoot: root, SessionID: current.SessionID}
	app.activeTabID = "active"
	var entries []PromptHistoryEntry
	cursor := ""
	for {
		raw, _ := json.Marshal(promptHistoryRequest{Cursor: cursor, Limit: 1})
		page, err := app.ScanPromptHistory(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, page.Entries...)
		if !page.HasOlder {
			break
		}
		if page.OlderCursor == cursor || len(entries) > 10 {
			t.Fatal("cursor did not advance")
		}
		cursor = page.OlderCursor
	}
	if len(entries) != 3 || entries[0].Text != "second" || entries[1].Text != "first" || entries[2].Text != "other session" {
		t.Fatalf("recall order: %+v", entries)
	}
	if entries[0].SessionPath != sessionRoute(current.SessionID) {
		t.Fatalf("noncanonical identity: %+v", entries[0])
	}
	previous := app.promptHistoryTape.nonce
	app.tabs["active"].WorkspaceRoot = other
	app.tabs["active"].SessionID = "other-workspace"
	page, err := app.ScanPromptHistory("{}")
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Text != "must stay isolated" || page.Nonce == previous {
		t.Fatalf("workspace isolation: %+v %v", page, err)
	}
	previous = page.Nonce
	app.tabs["active"].SessionID = ""
	page, err = app.ScanPromptHistory("{}")
	if err != nil || page.Nonce == previous {
		t.Fatalf("session change reused a previous tape: %+v %v", page, err)
	}
}

func TestPromptHistoryReadsCanonicalMessagesOnly(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "host instruction"},
		{Role: provider.RoleUser, Origin: provider.MessageOriginUser, Content: "expanded", RawContent: "visible prompt", CreatedAt: 123},
		{Role: provider.RoleAssistant, Content: "answer"},
		{Role: provider.RoleUser, Content: "untimed"},
	}
	entries := promptHistoryEntries(session.SessionInfo{SessionID: "chat", UpdatedAt: time.UnixMilli(456)}, messages)
	if len(entries) != 2 || entries[0].Text != "visible prompt" || entries[0].At != 123 || entries[1].At != 456 {
		t.Fatalf("entries: %+v", entries)
	}
	if got := promptHistoryEntries(session.SessionInfo{}, nil); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestPreviewAndRecallRejectRetiredTranscript(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	path := filepath.Join(t.TempDir(), "old.jsonl")
	body := []byte(`{"role":"user","content":"retired"}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PreviewSession(path); err == nil {
		t.Fatal("preview accepted retired path")
	}
	tape, err := app.newPromptHistoryTape("global", "", "")
	if err != nil || len(tape.readAll()) != 0 {
		t.Fatalf("retired history was read: %v", err)
	}
	ref := writeSuggestionSession(t, app, "", "native", provider.Message{Role: provider.RoleUser, Content: "current"})
	preview, err := app.PreviewSession(sessionRoute(ref.SessionID))
	if err != nil || len(preview) != 1 || preview[0].Content != "current" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if err := app.ArchiveCanonicalSession(ref); err != nil {
		t.Fatal(err)
	}
	preview, err = app.PreviewSession(sessionRoute(ref.SessionID))
	if err != nil || len(preview) != 1 || preview[0].Content != "current" {
		t.Fatalf("archived preview: %+v %v", preview, err)
	}
	if got := app.loadSuggestionSessions("global", "", 12); len(got) != 0 {
		t.Fatalf("archived session entered suggestions: %+v", got)
	}
}
