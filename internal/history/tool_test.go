package history

import (
	"deepzora/internal/provider"
	"encoding/json"
	"strings"
	"testing"
)

func TestHistoryToolSearchAndAroundAreUsable(t *testing.T) {
	service := currentHistoryStore(t)
	saveCurrentHistory(t, service, "decision", provider.Message{Role: provider.RoleUser, Content: "Should history use vector embeddings?"}, provider.Message{Role: provider.RoleAssistant, Content: "Decision: keep lightweight BM25 retrieval."})
	tool := newReadyIndexedTool(t, currentOptions(service))
	out, err := tool.Execute(t.Context(), []byte(`{"operation":"search","query":"BM25"}`))
	if err != nil || !strings.Contains(out, "session_id=decision") || !strings.Contains(out, "content_digest:") || !strings.Contains(out, "message_index=1") {
		t.Fatalf("search: %s %v", out, err)
	}
	args, _ := json.Marshal(map[string]any{"operation": "around", "catalog_id": catalogID(service.Query()), "session_id": "decision", "content_digest": currentDigest(t, service, "decision"), "message_index": 1})
	out, err = tool.Execute(t.Context(), args)
	if err != nil || !strings.Contains(out, "[0 user]") || !strings.Contains(out, "[1 assistant]") {
		t.Fatalf("around: %s %v", out, err)
	}
	if tool.Name() != "history" || !tool.ReadOnly() {
		t.Fatal("tool identity changed")
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["session_path"] != nil || schema.Properties["session_id"] == nil || schema.Properties["catalog_id"] == nil || schema.Properties["content_digest"] == nil {
		t.Fatalf("obsolete tool contract: %s", tool.Schema())
	}
}

func TestHistoryToolValidatesInputs(t *testing.T) {
	tool := newReadyIndexedTool(t, currentOptions(currentHistoryStore(t)))
	for _, args := range []string{`{}`, `{"operation":"scan"}`, `{"operation":"around","session_id":"missing"}`, `{"operation":"around","session_path":"/tmp/old.jsonl","message_index":0}`, `{"operation":`, `{"operation":"search","query":""}`, `{"operation":"search","query":"test","scope":"foreign"}`, `{"operation":"search","query":"test","kind":["unknown"]}`} {
		if _, err := tool.Execute(t.Context(), []byte(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
}
