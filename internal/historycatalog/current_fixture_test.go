package historycatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func testRoot(t *testing.T, root Root) Root {
	t.Helper()
	service, err := session.NewService("history-test", session.NewFilesystemPersistence(root.Path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	root.Query = service.Query()
	return root
}

func openFixtureSession(t *testing.T, path string) *session.Session {
	t.Helper()
	persistence := session.NewFilesystemPersistence(filepath.Dir(path))
	sess, err := persistence.Open(filepath.Base(path), session.ReadWrite)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, session.ErrSessionNotFound) {
		sess, err = persistence.Create(session.CreateOptions{SessionID: filepath.Base(path)})
	}
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

func writeFixtureEvent(t *testing.T, path, kind string, payload any) {
	t.Helper()
	sess := openFixtureSession(t, path)
	defer func() {
		if err := sess.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	operation := fmt.Sprintf("fixture-%d", sess.StateSnapshot().EventSequence+1)
	if _, err := sess.Append(t.Context(), session.Batch{OperationID: operation, Events: []session.Event{{Kind: kind, Payload: body}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func saveMessages(t *testing.T, path string, messages ...provider.Message) {
	t.Helper()
	for i := range messages {
		if messages[i].ID == "" {
			messages[i].ID = fmt.Sprintf("message-%d", i)
		}
		if messages[i].Role == provider.RoleUser {
			messages[i].Origin = provider.MessageOriginUser
		}
	}
	writeFixtureEvent(t, path, "history/replace", map[string]any{"messages": messages, "reason": "fixture"})
}

func appendFixtureMessage(t *testing.T, path string, message provider.Message) {
	t.Helper()
	if message.ID == "" {
		message.ID = "appended-" + message.Content
	}
	if message.Role == provider.RoleUser {
		message.Origin = provider.MessageOriginUser
	}
	writeFixtureEvent(t, path, "message/complete", map[string]any{"message": message})
}

// Capacity eviction consumes this derived activity column. Setting it directly
// makes age ordering deterministic without changing any authoritative session.
func setIndexedAge(t *testing.T, catalog *Catalog, path string, at time.Time) {
	t.Helper()
	result, err := catalog.db.Exec(`UPDATE history_sources SET last_activity_at=? WHERE path=?`, at.UnixMilli(), path)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("source age rows=%d err=%v", n, err)
	}
}
