package main

import (
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func currentBoundSearch(t *testing.T, a *App, id, text, cursor string, limit int) session.SearchHistoryPage {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		page, err := a.SearchSessionHistoryRead(id, text, cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		if page.Status != "preparing" {
			return page
		}
		select {
		case <-deadline:
			t.Fatal("search remained preparing")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestCurrentBoundHistorySearchReachabilityAndCursorIsolation(t *testing.T) {
	a, tab := currentBoundHistoryFixture(t, []provider.Message{currentHistoryUser(0, "needle 问题"), currentHistoryAssistant(0, "second needle 🧭"), currentHistoryUser(1, "third needle"), currentHistoryAssistant(1, "literal quote \"needle\"")})
	h, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := currentBoundSearch(t, a, h.ID, "needle", "", 2)
	if first.Status != "ready" || len(first.Hits) != 2 || !first.HasMore || first.Hits[0].Position != 4 {
		t.Fatalf("first: %+v", first)
	}
	last := currentBoundSearch(t, a, h.ID, "needle", first.NextCursor, 2)
	if last.Status != "ready" || len(last.Hits) != 2 || last.HasMore || last.Hits[1].Position != 1 {
		t.Fatalf("last: %+v", last)
	}
	for _, hit := range append(first.Hits, last.Hits...) {
		location := currentBoundLocation(t, a, h.ID, hit.MessageID, first.SnapshotSequence)
		if location.Status != "ready" || location.Position != hit.Position {
			t.Fatalf("unreachable hit: %+v", location)
		}
	}
	for _, text := range []string{"问题", "🧭", `"needle"`} {
		if page := currentBoundSearch(t, a, h.ID, text, "", 10); page.Status != "ready" || len(page.Hits) != 1 {
			t.Fatalf("literal %q: %+v", text, page)
		}
	}
	for _, cursor := range []string{"garbage", first.NextCursor} {
		if page := currentBoundSearch(t, a, h.ID, "different", cursor, 2); page.Status != "stale_cursor" || len(page.Hits) != 0 {
			t.Fatalf("foreign cursor: %+v", page)
		}
	}
	a.ReleaseSessionHistoryRead(h.ID)
	if page := currentBoundSearch(t, a, h.ID, "needle", "", 2); page.Status != "stale_cursor" {
		t.Fatalf("released search: %+v", page)
	}
	reopened, err := a.BeginSessionHistoryReadForTab(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	if page := currentBoundSearch(t, a, reopened.ID, "needle", "", 2); page.Status != "ready" || len(page.Hits) != 2 {
		t.Fatalf("reopened: %+v", page)
	}
	a.mu.Lock()
	tab.SessionGeneration++
	a.mu.Unlock()
	if page := currentBoundSearch(t, a, reopened.ID, "needle", "", 2); page.Status != "stale_cursor" {
		t.Fatalf("switched source: %+v", page)
	}
}

func currentBoundLocation(t *testing.T, a *App, id, messageID string, snapshot uint64) session.MessageLocation {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		location, err := a.LocateSessionHistoryMessage(id, messageID, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if location.Status != "preparing" {
			return location
		}
		select {
		case <-deadline:
			t.Fatal("locator remained preparing")
		case <-time.After(time.Millisecond):
		}
	}
}
