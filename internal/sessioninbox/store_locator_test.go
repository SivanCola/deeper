package sessioninbox

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAtRejectsFileLocatorWithoutCreatingStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "inbox")
	if st, err := OpenAt("/old/sessions/session.jsonl", dir, Limits{}); err == nil || st != nil {
		t.Fatalf("file locator accepted: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected locator created storage: %v", err)
	}
}

func TestOpenAtKeepsIdentityAndReceiptsAcrossReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime-inbox")
	const locator = "session-id:canonical.with.dots"
	s, err := OpenAt(locator, dir, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	receipt, err := s.Enqueue(EnqueueRequest{Intent: IntentFollowup, Idempotency: "request", Envelope: PromptEnvelope{SubmitText: "queued"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionRoute() != locator || s.Snapshot().SessionPath != locator || s.Dir() != dir {
		t.Fatal("logical locator was replaced by its storage path")
	}
	if meta, _, err := s.ReadItem(receipt.ItemID); err != nil || meta.SessionID != "canonical.with.dots" {
		t.Fatalf("default metadata lost the canonical session identity: %+v %v", meta, err)
	}
	s.Close()
	// Reopening the same current identity retains the manifest and blobs.
	reopened, err := OpenAt(locator, dir, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, found := reopened.LookupReceipt("request"); !found || got.ItemID != receipt.ItemID {
		t.Fatalf("reopened reader lost receipt: %+v %v", got, found)
	}
	if _, env, err := reopened.ReadItem(receipt.ItemID); err != nil || env.SubmitText != "queued" {
		t.Fatalf("reopened reader lost body: %+v %v", env, err)
	}
}
