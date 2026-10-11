package agent

import (
	"path/filepath"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

func restartProjectionState(path string, msgs []provider.Message, version uint64, covered int) CompactionState {
	key := promptCacheKey("workspace", BranchID(path), "provider/model")
	return CompactionState{
		SchemaVersion:     compactionStateSchemaCurrent,
		TranscriptVersion: version,
		PromptCacheKey:    key,
		Projection: ContextProjection{
			Messages: []provider.Message{
				{Role: provider.RoleSystem, Content: "system"},
				{Role: provider.RoleUser, Content: "[compacted context]"},
			},
			TranscriptVersion: version,
			ProjectionVersion: 1,
			CoveredCount:      covered,
			CoveredPrefixHash: coveredPrefixHash(msgs, covered),
		},
	}
}

func reopenProjectionAgent(t *testing.T, path string) *Agent {
	t.Helper()
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got := loaded.TranscriptVersion(); got != 0 {
		t.Fatalf("loaded transcript version = %d, want process-local reset to 0", got)
	}
	return New(nil, tool.NewRegistry(), loaded, Options{
		SessionPath: path,
		WorkspaceID: "workspace",
		ModelRef:    "provider/model",
	}, event.Discard)
}

func TestProjectionRestoresAfterRestartWithResetTranscriptVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	sess := NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "task"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "done"})
	msgs, version := sess.snapshotMessagesVersion()
	if version == 0 {
		t.Fatal("fixture must compact at a non-zero transcript version")
	}
	if err := sess.SaveSnapshot(path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if err := SaveCompactionState(path, restartProjectionState(path, msgs, version, len(msgs))); err != nil {
		t.Fatalf("SaveCompactionState: %v", err)
	}

	reopened := reopenProjectionAgent(t, path)
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state = %q, want restored", got)
	}
	if got := len(reopened.modelVisibleMessages()); got != 2 {
		t.Fatalf("visible messages = %d, want compacted projection", got)
	}
}
