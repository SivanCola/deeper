package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/provider"
)

func TestCurrentProtocolEvidenceSurvivesColdOpenWithoutLeakingToModel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "current")
	s, err := CreateWithOptions(dir, "current", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	message := provider.Message{ID: "receipt", Role: provider.RoleTool, LocalOnly: true,
		ProtocolRecovery: json.RawMessage(`{"version":1,"id":"incident","state":"pending","extra":"private"}`)}
	appendReplayEvent(t, s, "pending", "message/upsert", map[string]any{"message": message})
	message.ProtocolRecovery = json.RawMessage(`{"version":1,"id":"incident","state":"consumed","extra":"private"}`)
	appendReplayEvent(t, s, "consumed", "message/upsert", map[string]any{"message": message})
	appendReplayEvent(t, s, "model-context", "model/context-replace", map[string]any{"messages": []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "summary"}}})
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	s, err = OpenWithOptions(dir, "current", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	view := s.ExecutionSnapshot().Projection
	if view.ProtocolRecovery == nil || string(view.ProtocolRecovery.Record) != string(message.ProtocolRecovery) {
		t.Fatalf("lost exact receipt: %+v", view.ProtocolRecovery)
	}
	if len(view.ModelMessages) != 2 || !view.ModelMessages[1].LocalOnly || view.ModelMessages[1].ID != message.ID {
		t.Fatalf("execution messages: %+v", view.ModelMessages)
	}
	wire, _ := json.Marshal(provider.ModelMessages(view.ModelMessages))
	if strings.Contains(string(wire), "private") || strings.Contains(string(wire), "protocol_recovery") {
		t.Fatalf("local receipt leaked: %s", wire)
	}
	view.ProtocolRecovery.Record[0] = 'X'
	view.ModelMessages[1].ProtocolRecovery[0] = 'X'
	if got := s.ExecutionSnapshot().Projection.ProtocolRecovery; string(got.Record) != string(message.ProtocolRecovery) {
		t.Fatal("snapshot mutation corrupted authoritative receipt")
	}
	if messages := s.Snapshot().Projection.Messages; len(messages) != 1 || messages[0].ID != "receipt" {
		t.Fatalf("fabricated history: %+v", messages)
	}
	appendReplayEvent(t, s, "retract", "message/retract", map[string]any{"messageIds": []string{"receipt"}, "reason": "removed"})
	if s.ExecutionSnapshot().Projection.ProtocolRecovery != nil {
		t.Fatal("retracted receipt survived")
	}
}
