package serve

import (
	"deepzora/internal/config"
	"deepzora/internal/servecontract"
	"deepzora/internal/session"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestHistoryOutlineHTTPIdentityAndFixedWindow(t *testing.T) {
	server, ctrl := newWindowTestServer(t)
	if !slices.Contains(New(ctrl, NewBroadcaster(), config.ServeConfig{}).capabilities(), servecontract.HistoryOutlineV1) {
		t.Fatal("current history outline capability is not advertised")
	}
	var window session.HistoryWindowPage
	getWindow(t, server, "anchor=newest&limit=1", &window)
	response, err := http.Get(server.URL + "/session-history/outline?sessionId=canonical&startTurn=1&limit=2")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page session.HistoryOutlinePage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil || response.StatusCode != http.StatusOK || page.Status != "ready" || page.TotalTurns != 8 || len(page.Entries) != 2 || page.Entries[0].MessageID != "m1" {
		t.Fatalf("outline: %+v %v", page, err)
	}
	for _, generation := range []string{page.Generation, "evicted"} {
		response, err := http.Get(server.URL + fmt.Sprintf("/session-history/outline?sessionId=canonical&generation=%s&snapshotSequence=%d&startTurn=%d&limit=8", generation, page.SnapshotSequence, page.NextTurn))
		if err != nil {
			t.Fatal(err)
		}
		var next session.HistoryOutlinePage
		decodeErr := json.NewDecoder(response.Body).Decode(&next)
		response.Body.Close()
		if decodeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("outline continuation: %+v, %v", next, decodeErr)
		}
		if generation == page.Generation {
			if next.Status != "ready" || next.Generation != page.Generation || next.SnapshotSequence != page.SnapshotSequence || len(next.Entries) != 6 || next.Entries[0].Turn != 3 || !next.Done {
				t.Fatalf("outline lost fixed-cut pagination: %+v", next)
			}
		} else if next.Status != "stale_cursor" {
			t.Fatalf("unknown generation answered as current: %+v", next)
		}
	}
	getWindow(t, server, fmt.Sprintf("anchor=message&messageId=m1&direction=newer&limit=1&generation=%s&snapshotSequence=%d", page.Generation, page.SnapshotSequence), &window)
	if window.Status != "ready" || window.SnapshotSequence != page.SnapshotSequence || window.Messages[0].MessageID != "m1" {
		t.Fatalf("window: %+v", window)
	}
	getWindow(t, server, "anchor=message&messageId=m1&generation=wrong", &window)
	if window.Status != "stale_cursor" {
		t.Fatalf("lost generation: %+v", window)
	}
	for _, query := range []string{"sessionId=other", "sessionId=canonical&limit=bad", "sessionId=canonical&snapshotSequence=-1"} {
		response, err := http.Get(server.URL + "/session-history/outline?" + query)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatalf("accepted invalid request: %s", query)
		}
	}
}
