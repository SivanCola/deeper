package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func currentBoundHistoryFixture(t *testing.T, messages []provider.Message) (*App, *WorkspaceTab) {
	t.Helper()
	isolateDesktopUserDirs(t)
	a := NewApp()
	a.ctx = t.Context()
	t.Cleanup(a.closeSessionServices)
	t.Cleanup(a.closeHistoryReaders)
	ref := seedCurrentHistorySource(t, a, "bound-current", "global", globalWorkspaceRoot(), messages)
	tab := &WorkspaceTab{ID: "history-tab", Scope: "global", SessionID: ref.SessionID, SessionGeneration: 1}
	a.tabs[tab.ID] = tab
	a.activeTabID = tab.ID
	return a, tab
}

func currentBoundWindow(t *testing.T, a *App, id string, req session.HistoryWindowRequest) session.HistoryWindowPage {
	t.Helper()
	// Preparation publishes a derived index and durable content objects. Its I/O
	// latency is not a navigation assertion; this only catches a lost worker.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		page, err := a.ReadSessionHistoryWindow(id, req)
		if err != nil {
			t.Fatal(err)
		}
		if page.Status != "preparing" {
			return page
		}
		select {
		case <-ctx.Done():
			reader, err := a.historyReader(id)
			if err != nil {
				t.Fatalf("window remained preparing: %v; binding=%v", ctx.Err(), err)
			}
			view, err := reader.query.OpenSession(t.Context(), reader.ref)
			t.Fatalf("window remained preparing: %v; reader=%v index=%q observation=%v durable=%d maintenance=%+v", ctx.Err(), reader.ctx.Err(), view.History, err, view.DurableSequence, a.historyMaintenance.Diagnostics())
		case <-poll.C:
		}
	}
}

func TestCurrentBoundHistoryColdNavigationAndReadOnlyContent(t *testing.T) {
	var messages []provider.Message
	for i := range 140 {
		messages = append(messages, currentHistoryUser(i, fmt.Sprintf("question %d", i)), currentHistoryAssistant(i, fmt.Sprintf("answer %d", i)))
	}
	a, tab := currentBoundHistoryFixture(t, messages)
	query := a.desktopSessionService("").Query()
	info, err := query.Stat(t.Context(), session.SessionRef{HostID: "local", SessionID: tab.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(info.Path, "events.frames"))
	if err != nil {
		t.Fatal(err)
	}
	writer, err := session.NewFilesystemPersistence(query.StorageRoot()).Open(tab.SessionID, session.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(context.Background())
	resume, err := a.historyMaintenance.Foreground(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	open, err := query.OpenSession(t.Context(), session.SessionRef{HostID: "local", SessionID: tab.SessionID})
	if err != nil || open.History != session.PreparationPreparing {
		t.Fatalf("cold history index: %+v %v", open, err)
	}
	h, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.ReleaseSessionHistoryRead(h.ID)
	request := session.HistoryWindowRequest{Anchor: "newest", Limit: 4}
	pending, err := a.ReadSessionHistoryWindow(h.ID, request)
	if err != nil || pending.Status != "preparing" || len(pending.Messages) != 0 {
		t.Fatalf("blocked cold window: %+v %v", pending, err)
	}
	resume()
	newest := currentBoundWindow(t, a, h.ID, request)
	if newest.Status != "ready" || len(newest.Messages) != 4 || !newest.HasOlder {
		t.Fatalf("newest: %+v", newest)
	}
	older := currentBoundWindow(t, a, h.ID, session.HistoryWindowRequest{Anchor: "cursor", Cursor: newest.OlderCursor, Limit: 4})
	back := currentBoundWindow(t, a, h.ID, session.HistoryWindowRequest{Anchor: "cursor", Cursor: older.NewerCursor, Limit: 4})
	if !older.HasNewer || !reflect.DeepEqual(back.Messages, newest.Messages) {
		t.Fatalf("bidirectional reachability: %+v %+v", older, back)
	}
	outline, err := a.ReadSessionHistoryOutline(h.ID, session.HistoryOutlineRequest{Limit: 128})
	if err != nil || outline.Status != "ready" || len(outline.Entries) != 128 || outline.TotalTurns != 140 || outline.NextTurn != 129 {
		t.Fatalf("outline: %+v %v", outline, err)
	}
	last, err := a.ReadSessionHistoryOutline(h.ID, session.HistoryOutlineRequest{Generation: outline.Generation, SnapshotSequence: &outline.SnapshotSequence, StartTurn: outline.NextTurn, Limit: 128})
	if err != nil || last.Status != "ready" || len(last.Entries) != 12 || !last.Done || last.Entries[0].Prompt != "question 128" {
		t.Fatalf("outline suffix: %+v %v", last, err)
	}
	target := outline.Entries[3]
	location, err := a.LocateSessionHistoryMessage(h.ID, target.MessageID, outline.SnapshotSequence)
	if err != nil || location.Status != "ready" || location.Position != target.Position || location.VisibleTurn != target.Turn {
		t.Fatalf("locate: %+v %v", location, err)
	}
	for _, req := range []session.HistoryWindowRequest{{Anchor: "cursor", Cursor: location.Cursor}, {Anchor: "turn", Turn: target.Turn}, {Anchor: "message", MessageID: target.MessageID}} {
		req.Generation, req.SnapshotSequence, req.Limit = outline.Generation, &outline.SnapshotSequence, 2
		page := currentBoundWindow(t, a, h.ID, req)
		found := false
		for _, message := range page.Messages {
			found = found || message.MessageID == target.MessageID
		}
		if page.Status != "ready" || !found || !page.HasNewer {
			t.Fatalf("anchor: %+v", page)
		}
	}
	if page := currentBoundWindow(t, a, h.ID, session.HistoryWindowRequest{Anchor: "turn", Turn: 4, Generation: "foreign"}); page.Status != "stale_cursor" {
		t.Fatalf("foreign cut: %+v", page)
	}
	if page := currentBoundWindow(t, a, h.ID, session.HistoryWindowRequest{Anchor: "turn", Turn: 999}); page.Status != "not_found" {
		t.Fatalf("absent turn: %+v", page)
	}
	after, err := os.ReadFile(filepath.Join(info.Path, "events.frames"))
	if err != nil || !bytes.Equal(before, after) || tab.Ctrl != nil {
		t.Fatal("cold reader modified source or opened a controller")
	}
}

func TestCurrentBoundHistoryCancellationAndReplacement(t *testing.T) {
	a, tab := currentBoundHistoryFixture(t, []provider.Message{currentHistoryUser(0, "one"), currentHistoryAssistant(0, "answer")})
	resume, err := a.historyMaintenance.Foreground(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	one, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.historyReader(one.ID)
	second, _ := a.historyReader(two.ID)
	pending, err := a.ReadSessionHistoryWindow(one.ID, session.HistoryWindowRequest{Anchor: "newest"})
	if err != nil || pending.Status != "preparing" {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	a.ReleaseSessionHistoryRead(one.ID)
	if first.ctx.Err() == nil || second.ctx.Err() != nil {
		t.Fatal("release crossed reader ownership")
	}
	resume()
	if page := currentBoundWindow(t, a, two.ID, session.HistoryWindowRequest{Anchor: "newest"}); page.Status != "ready" {
		t.Fatalf("peer: %+v", page)
	}
	service := a.desktopSessionService("")
	info, err := service.Query().Stat(t.Context(), second.ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(info.Path, filepath.Join(t.TempDir(), "old")); err != nil {
		t.Fatal(err)
	}
	replacement, err := service.Create(t.Context(), session.CreateOptions{SessionID: tab.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	appendSessionTestMessage(t, replacement, "replace", provider.Message{ID: "replacement", Role: provider.RoleUser, Content: "new"})
	if err := service.Close(t.Context(), replacement.Ref()); err != nil {
		t.Fatal(err)
	}
	three, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ctx.Err() == nil {
		t.Fatal("new generation did not retire old reader")
	}
	if page := currentBoundWindow(t, a, two.ID, session.HistoryWindowRequest{Anchor: "newest"}); page.Status != "stale_cursor" {
		t.Fatalf("old reader: %+v", page)
	}
	a.ReleaseSessionHistoryRead(two.ID)
	if page := currentBoundWindow(t, a, three.ID, session.HistoryWindowRequest{Anchor: "newest"}); page.Status != "ready" || len(page.Messages) != 1 || page.Messages[0].MessageID != "replacement" {
		t.Fatalf("new generation: %+v", page)
	}
	a.closeHistoryReaders()
	if page := currentBoundWindow(t, a, three.ID, session.HistoryWindowRequest{Anchor: "newest"}); page.Status != "stale_cursor" {
		t.Fatal("shutdown retained reader")
	}
}

func TestCurrentBoundHistoryLargeContentKeepsExactHandle(t *testing.T) {
	text := strings.Repeat("完整内容🧭", 90000)
	a, tab := currentBoundHistoryFixture(t, []provider.Message{currentHistoryUser(0, "question"), currentHistoryAssistant(0, text)})
	h, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	page := currentBoundWindow(t, a, h.ID, session.HistoryWindowRequest{Anchor: "newest", Limit: 2})
	if page.Status != "ready" || len(page.Messages) != 2 || page.Messages[1].ContentRef == nil {
		t.Fatalf("large message: %+v", page)
	}
	ref := *page.Messages[1].ContentRef
	var body []byte
	for offset := int64(0); ; {
		chunk, err := a.ReadSessionHistoryContent(h.ID, ref, offset)
		if err != nil {
			t.Fatal(err)
		}
		part, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, part...)
		if chunk.Done {
			break
		}
		if chunk.NextOffset <= offset {
			t.Fatal("content did not advance")
		}
		offset = chunk.NextOffset
	}
	var message provider.Message
	if err := json.Unmarshal(body, &message); err != nil || message.Content != text {
		t.Fatalf("content changed: %v", err)
	}
	a.ReleaseSessionHistoryRead(h.ID)
	if _, err := a.ReadSessionHistoryContent(h.ID, ref, 0); err == nil {
		t.Fatal("released content handle remained usable")
	}
	next, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentBoundWindow(t, a, next.ID, session.HistoryWindowRequest{Anchor: "newest", Limit: 2})
	if _, err := a.ReadSessionHistoryContent(h.ID, ref, 0); err == nil {
		t.Fatal("reopen revived old content handle")
	}
	if _, err := a.ReadSessionHistoryContent(next.ID, ref, 0); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentBoundHistoryRequiresExplicitIdentity(t *testing.T) {
	a, tab := currentBoundHistoryFixture(t, nil)
	tab.SessionID = ""
	historicalPath := filepath.Join(t.TempDir(), "old.jsonl")
	if err := os.WriteFile(historicalPath, []byte("{\"role\":\"user\",\"content\":\"old\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.BeginSessionHistoryReadForTab(tab.ID); err == nil {
		t.Fatal("bound reader accepted a legacy path")
	}
}

func TestCurrentBoundHistoryReplacementDuringPreparation(t *testing.T) {
	a, tab := currentBoundHistoryFixture(t, []provider.Message{currentHistoryUser(0, "old marker")})
	resume, err := a.historyMaintenance.Foreground(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer resume()
	old, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := a.ReadSessionHistoryWindow(old.ID, session.HistoryWindowRequest{Anchor: "newest"}); err != nil || page.Status != "preparing" {
		t.Fatalf("old window: %+v %v", page, err)
	}
	if page, err := a.SearchSessionHistoryRead(old.ID, "marker", "", 10); err != nil || page.Status != "preparing" {
		t.Fatalf("old search: %+v %v", page, err)
	}
	reader, _ := a.historyReader(old.ID)
	service := a.desktopSessionService("")
	info, err := service.Query().Stat(t.Context(), reader.ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(info.Path, filepath.Join(t.TempDir(), "old")); err != nil {
		t.Fatal(err)
	}
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: tab.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	appendSessionTestMessage(t, runtime, "new", provider.Message{ID: "new", Role: provider.RoleUser, Content: "new marker"})
	if err := service.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	next, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reader.ctx.Err() == nil {
		t.Fatal("replacement retained old preparation owner")
	}
	resume()
	if page := currentBoundWindow(t, a, next.ID, session.HistoryWindowRequest{Anchor: "newest"}); page.Status != "ready" || len(page.Messages) != 1 || page.Messages[0].MessageID != "new" {
		t.Fatalf("replacement window: %+v", page)
	}
	if page := currentBoundSearch(t, a, next.ID, "marker", "", 10); page.Status != "ready" || len(page.Hits) != 1 || page.Hits[0].MessageID != "new" {
		t.Fatalf("replacement search: %+v", page)
	}
	if page := currentBoundSearch(t, a, old.ID, "marker", "", 10); page.Status != "stale_cursor" {
		t.Fatal("old preparation published after replacement")
	}
}
