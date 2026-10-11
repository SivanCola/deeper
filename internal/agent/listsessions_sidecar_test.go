package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/provider"
)

func writeSessionFile(t *testing.T, path string, msgs []provider.Message) {
	t.Helper()
	session, err := LoadSession(path)
	if os.IsNotExist(err) {
		session = NewSession("")
	} else if err != nil {
		t.Fatal(err)
	}
	session.Rewrite(msgs, "fixture")
	if err := session.SaveSnapshot(path); err != nil {
		t.Fatal(err)
	}
}

// SessionPreviewFromMessages must match a from-disk decode byte-for-byte, since
// Session.Save persists exactly the messages it is handed.
func TestSessionPreviewFromMessagesMatchesDecode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-000000-deepseek-chat.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "you are helpful"},
		{Role: provider.RoleUser, Content: "first question about the bug"},
		{Role: provider.RoleAssistant, Content: "here is an answer", ReasoningContent: "thinking"},
		{Role: provider.RoleUser, Content: "follow up"},
		{Role: provider.RoleAssistant, Content: "more"},
	}
	writeSessionFile(t, path, msgs)

	filePreview, fileTurns := previewSession(path)
	memPreview, memTurns := SessionPreviewFromMessages(msgs)
	if fileTurns != memTurns || filePreview != memPreview {
		t.Fatalf("mismatch: file=(%q,%d) mem=(%q,%d)", filePreview, fileTurns, memPreview, memTurns)
	}
	if memTurns != 2 {
		t.Fatalf("expected 2 user turns, got %d", memTurns)
	}
}

// When the sidecar records Turns/Preview, ListSessions must trust them and not
// re-derive from the .jsonl. We prove that by planting counts that disagree with
// the file: if ListSessions returns the planted values, it used the sidecar.
func TestListSessionsUsesSidecarWithoutDecoding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-000000-deepseek-chat.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "real content in the file"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	writeSessionFile(t, path, msgs)
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBranchMeta(path, BranchMeta{ID: BranchID(path), Revision: 1, ContentDigest: digestString(digest)}); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}
	// Sidecar deliberately disagrees with the file (3 turns, custom preview).
	if err := UpdateSessionMeta(path, "", "cached preview line", 3, true); err != nil {
		t.Fatalf("UpdateSessionMeta: %v", err)
	}

	infos, err := ListSessions(dir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 session, got %d", len(infos))
	}
	if infos[0].Turns != 3 || infos[0].Preview != "cached preview line" {
		t.Fatalf("expected sidecar values (3, %q), got (%d, %q)", "cached preview line", infos[0].Turns, infos[0].Preview)
	}
}

// A counts-authoritative sidecar (SchemaVersion stamped) that records Turns == 0
// must be trusted as empty and skipped WITHOUT decoding the .jsonl. We prove the
// version gate by planting a file that actually has content but a meta claiming
// it is empty: if the session is decoded it would be listed; trusting the meta
// skips it.
func TestListSessionsTrustsRecordedEmptyWithoutDecoding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260101-000000-deepseek-chat.jsonl")
	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "real content the meta will lie about"},
		{Role: provider.RoleAssistant, Content: "a"},
	}
	writeSessionFile(t, path, msgs)
	digest, err := digestSessionMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	meta := BranchMeta{
		ID:            BranchID(path),
		Turns:         0,
		SchemaVersion: BranchMetaCountsVersion,
		Revision:      1,
		ContentDigest: digestString(digest),
	}
	stampSessionListingProjection(&meta)
	if err := SaveBranchMeta(path, meta); err != nil {
		t.Fatalf("SaveBranchMeta: %v", err)
	}

	infos, err := ListSessions(dir)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("a counts-authoritative empty session should be skipped without decoding; got %d", len(infos))
	}
}

func TestListSessionsKeepsUnreadableNonEmptySessionsVisible(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cachedZero bool
	}{
		{name: "legacy"},
		{name: "previously cached as empty", cachedZero: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "20260101-000000-deepseek-chat.jsonl")
			if err := os.WriteFile(path, []byte("not valid json\n"), 0o600); err != nil {
				t.Fatalf("write corrupt session: %v", err)
			}
			if tc.cachedZero {
				if err := SaveBranchMeta(path, BranchMeta{
					ID:            BranchID(path),
					Turns:         0,
					SchemaVersion: BranchMetaCountsVersion,
				}); err != nil {
					t.Fatalf("SaveBranchMeta: %v", err)
				}
			}

			infos, err := ListSessions(dir)
			if err != nil {
				t.Fatalf("ListSessions: %v", err)
			}
			if len(infos) != 1 {
				t.Fatalf("unreadable non-empty session must remain visible; got %d entries", len(infos))
			}
			if infos[0].Turns != 0 || infos[0].CountsKnown || !strings.Contains(infos[0].Preview, "being indexed") {
				t.Fatalf("unexpected corrupt-session listing: %+v", infos[0])
			}

			meta, ok, err := LoadBranchMeta(path)
			if err != nil {
				t.Fatalf("LoadBranchMeta: %v", err)
			}
			if ok && sessionListingProjectionFresh(meta.SchemaVersion, meta.Turns, meta.Revision, meta.ListingRevision, meta.ContentDigest, meta.ListingContentDigest) {
				t.Fatalf("unreadable session was incorrectly stamped authoritative: %+v", meta)
			}
		})
	}
}
