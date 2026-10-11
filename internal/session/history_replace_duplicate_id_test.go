package session

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"deepzora/internal/provider"
)

func duplicateReplacementPayload(t *testing.T, kind string) json.RawMessage {
	t.Helper()
	messages := []provider.Message{
		{ID: "dup", Role: provider.RoleUser, Content: "first dup"},
		{ID: "answer", Role: provider.RoleAssistant, Content: "plan"},
		{ID: "dup", Role: provider.RoleUser, Content: "second dup"},
		{ID: "tail", Role: provider.RoleAssistant, Content: "done"},
	}
	body := map[string]any{"messages": messages, "reason": "fresh-resume"}

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestReplacementRefusesDuplicateMessageID(t *testing.T) {
	for _, kind := range []string{"history/replace"} {
		t.Run(kind, func(t *testing.T) {
			service, err := NewService("local", NewFilesystemPersistence(filepath.Join(t.TempDir(), "sessions")))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
			runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "dup-write"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = runtime.Session().Append(t.Context(), Batch{OperationID: "dup", Events: []Event{{Kind: kind, Payload: duplicateReplacementPayload(t, kind)}}})
			if !errors.Is(err, ErrDuplicateMessageID) {
				t.Fatalf("append duplicate %s: %v, want ErrDuplicateMessageID", kind, err)
			}
		})
	}
}

func TestReplacementReadersRejectDuplicateMessageID(t *testing.T) {
	ev := Event{Kind: "history/replace", Payload: duplicateReplacementPayload(t, "history/replace")}
	if _, err := replacementEventMessages(ev, ev.Payload); err == nil {
		t.Fatal("duplicate replacement was accepted")
	}
	if err := projectHistoryReplace(&Projection{}, Commit{}, ev); err == nil {
		t.Fatal("projection accepted duplicate replacement")
	}
}
func messageIDs(messages []provider.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	return ids
}
