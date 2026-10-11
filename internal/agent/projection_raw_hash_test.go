package agent

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestCoveredPrefixHashIgnoresLocalToolRawContent(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "call-1", Name: "read", Content: "bounded", RawContent: "full result A"},
	}
	first := coveredPrefixHash(msgs, len(msgs))
	msgs[1].RawContent = "full result B"
	if second := coveredPrefixHash(msgs, len(msgs)); second != first {
		t.Fatal("local RawContent edit changed the provider-visible covered-prefix hash")
	}
	msgs[1].Content = "different bounded result"
	if second := coveredPrefixHash(msgs, len(msgs)); second == first {
		t.Fatal("provider-visible Content edit did not invalidate the covered-prefix hash")
	}
}

func projectionJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadProjectionSidecarDropsUnverifiableBodyButKeepsReceipt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	msgs := []provider.Message{{Role: provider.RoleSystem, Content: "system"}, {Role: provider.RoleUser, Content: "task"}}
	if err := SaveCompactionState(path, CompactionState{
		SchemaVersion:  compactionStateSchemaV3,
		PromptCacheKey: promptCacheKey("ws", BranchID(path), "model"),
		Projection: ContextProjection{
			Messages:     []provider.Message{{Role: provider.RoleSystem, Content: "system"}, formatSummaryMessage("summary")},
			CoveredCount: len(msgs), CoveredPrefixHash: "unrelated-stale-hash",
		},
		LastReceipt: &ContextMaintenanceReceipt{Status: "applied", Action: "summary", CoveredPrefixHash: "unrelated-stale-hash"},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(nil, nil, &Session{Messages: msgs}, Options{SessionPath: path, WorkspaceID: "ws", ModelRef: "model"}, event.Discard)
	if len(a.sess.compactionState.Projection.Messages) != 0 {
		t.Fatal("unverifiable projection body survived load")
	}
	if a.sess.compactionState.LastReceipt == nil || a.sess.compactionState.LastReceipt.Action != "summary" {
		t.Fatalf("maintenance receipt was lost: %+v", a.sess.compactionState.LastReceipt)
	}
	if got := a.Session().Snapshot(); !reflect.DeepEqual(got, msgs) {
		t.Fatalf("canonical transcript changed: got=%+v want=%+v", got, msgs)
	}
}
