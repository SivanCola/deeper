package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func createCanonicalTestSession(t *testing.T, v4root, id, prompt string) {
	t.Helper()
	persistence := session.NewFilesystemPersistence(v4root)
	sess, err := persistence.Create(session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "" {
		messagePayload, err := json.Marshal(map[string]any{
			"message": provider.Message{ID: id + "-m1", Role: provider.RoleUser, Content: prompt},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sess.Append(context.Background(), session.Batch{OperationID: "seed", TurnID: id + "-turn", Events: []session.Event{
			{Kind: "message/complete", Payload: messagePayload},
			{Kind: "turn/start"},
			{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sess.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func waitForCatalogMetadata(t *testing.T, sessionDir string, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	service := testCLISessionService(t, sessionDir)
	for time.Now().Before(deadline) {
		page, err := service.Query().List(context.Background(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		ready := map[string]bool{}
		for _, info := range page.Sessions {
			ready[info.SessionID] = info.MetadataStatus == session.MetadataReady
		}
		all := true
		for _, id := range ids {
			if !ready[id] {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("catalog metadata for %v did not become ready", ids)
}

// The test owns each temporary host service, including its cold-query workers.
// Register this before controller cleanup so controllers finish saves first.
func testCLISessionService(t *testing.T, sessionDir string) *session.Service {
	t.Helper()
	service := cliSessionService(sessionDir)
	if service == nil {
		t.Fatal("no session service for test workspace")
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown test session service: %v", err)
		}
		root := session.RootForSessionDir(sessionDir)
		cliSessionServices.Lock()
		if cliSessionServices.byRoot[root] == service {
			delete(cliSessionServices.byRoot, root)
		}
		cliSessionServices.Unlock()
	})
	return service
}

func TestCanonicalResumeHidden(t *testing.T) {
	if !canonicalResumeHidden(session.SessionInfo{MetadataStatus: session.MetadataReady}) {
		t.Fatal("ready empty session should be hidden")
	}
	if canonicalResumeHidden(session.SessionInfo{MetadataStatus: session.MetadataReady, Turns: 2}) {
		t.Fatal("session with turns should stay listed")
	}
	if canonicalResumeHidden(session.SessionInfo{MetadataStatus: session.MetadataReady, Preview: "hi"}) {
		t.Fatal("session with preview should stay listed")
	}
	if canonicalResumeHidden(session.SessionInfo{MetadataStatus: session.MetadataPending}) {
		t.Fatal("pending metadata must stay listed while the rebuild runs")
	}
}

// fakeSessionCatalog pages a synthetic catalog in session-id order with the
// same cursor contract as FilesystemPersistence.List.
type fakeSessionCatalog struct {
	infos []session.SessionInfo
	calls int
}

func (f *fakeSessionCatalog) List(_ context.Context, cursor string, limit int) (session.SessionPage, error) {
	f.calls++
	page := session.SessionPage{Sessions: []session.SessionInfo{}}
	for _, info := range f.infos {
		if info.SessionID <= cursor {
			continue
		}
		if len(page.Sessions) == limit {
			page.NextCursor = page.Sessions[len(page.Sessions)-1].SessionID
			break
		}
		page.Sessions = append(page.Sessions, info)
	}
	return page, nil
}

func newFakeSessionCatalog(rows int, updatedAt func(i int) time.Time) *fakeSessionCatalog {
	catalog := &fakeSessionCatalog{}
	for i := range rows {
		id := fmt.Sprintf("s%05d", i)
		catalog.infos = append(catalog.infos, session.SessionInfo{
			SessionID: id, Ref: session.SessionRef{HostID: "local", SessionID: id}, Codec: session.Codec,
			MetadataStatus: session.MetadataReady, Turns: 1, Preview: id, UpdatedAt: updatedAt(i), Path: "/sessions-v4/" + id,
		})
	}
	return catalog
}

// TestCanonicalResumeEntriesRankNewestAcrossCatalogPages proves the listing
// walks every catalog page before ranking: with more than one page of
// sessions whose newest activity sorts last by id, the newest rows are the
// ones offered (and therefore the ones --continue picks), not the first page
// in lexical order.
func TestCanonicalResumeEntriesRankNewestAcrossCatalogPages(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	catalog := newFakeSessionCatalog(250, func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) })

	entries := canonicalResumeEntriesFrom(context.Background(), catalog)

	if len(entries) != canonicalResumeScanCap {
		t.Fatalf("listed %d rows, want the display cap of %d", len(entries), canonicalResumeScanCap)
	}
	if got := entries[0].target.ref.SessionID; got != "s00249" {
		t.Fatalf("newest row = %q, want s00249 from the last catalog page", got)
	}
	if got := entries[len(entries)-1].target.ref.SessionID; got != "s00150" {
		t.Fatalf("oldest offered row = %q, want s00150", got)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].session.UpdatedAt.After(entries[i-1].session.UpdatedAt) {
			t.Fatalf("rows are not newest-first at %d: %v after %v", i, entries[i].session.UpdatedAt, entries[i-1].session.UpdatedAt)
		}
	}
	if catalog.calls != 3 {
		t.Fatalf("catalog pages walked = %d, want 3 (250 rows at %d per page)", catalog.calls, canonicalResumeScanCap)
	}
}

// TestCanonicalResumeEntriesBoundTheCatalogWalk pins the documented hard cap:
// a store larger than canonicalResumeWalkCap stops paging instead of scanning
// every directory on each /resume.
func TestCanonicalResumeEntriesBoundTheCatalogWalk(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	catalog := newFakeSessionCatalog(canonicalResumeWalkCap+canonicalResumeScanCap, func(int) time.Time { return base })

	entries := canonicalResumeEntriesFrom(context.Background(), catalog)

	if want := canonicalResumeWalkCap / canonicalResumeScanCap; catalog.calls != want {
		t.Fatalf("catalog pages walked = %d, want %d (walk cap %d)", catalog.calls, want, canonicalResumeWalkCap)
	}
	if len(entries) != canonicalResumeScanCap {
		t.Fatalf("listed %d rows, want the display cap of %d", len(entries), canonicalResumeScanCap)
	}
}
