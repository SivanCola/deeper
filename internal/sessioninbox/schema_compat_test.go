package sessioninbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInboxNewerSchemaIsReadonlyPaused(t *testing.T) {
	session := filepath.Join(t.TempDir(), "inbox")
	inboxDir := session
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(manifest{SchemaVersion: SchemaVersion + 1, Items: []InboxItemMeta{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, manifestName), body, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenAt("session-id:s", session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := s.Snapshot()
	if !snap.Paused || !snap.Readonly || snap.SchemaVersion != SchemaVersion+1 {
		t.Fatalf("newer schema snapshot = %+v, want readonly pause", snap)
	}
	if _, err := s.Enqueue(EnqueueRequest{Envelope: PromptEnvelope{SubmitText: "nope"}}); !errors.Is(err, ErrSchemaReadonly) {
		t.Fatalf("enqueue newer schema = %v, want %v", err, ErrSchemaReadonly)
	}
}
