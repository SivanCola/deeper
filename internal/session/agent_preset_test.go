package session

import (
	"testing"

	"deepzora/internal/provider"
)

func TestAgentPresetFirstTurnAtomicBatchLocksIdentity(t *testing.T) {
	_, runtime := reviewRuntime(t)
	store := runtime.Session()
	if _, err := store.Append(t.Context(), Batch{OperationID: "first", TurnID: "turn-1", Events: []Event{
		{Kind: "turn/start", Payload: []byte(`{"status":"in_progress"}`)},
		{Kind: AgentPresetKind, Payload: []byte(`{"preset":"review"}`)},
		{Kind: "turn/end", Payload: []byte(`{"status":"completed"}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	before := store.ExecutionSnapshot()
	if !store.HasTurns() || before.Projection.AgentPreset != "review" {
		t.Fatal("first turn did not lock its atomic preset choice")
	}
	if _, err := store.AppendBatch(t.Context(), "default-reset", []Event{{Kind: AgentPresetKind, Payload: []byte(`{"preset":""}`)}}); err == nil {
		t.Fatal("locked preset accepted default reset")
	}
	if store.ExecutionSnapshot().EventSequence != before.EventSequence {
		t.Fatal("rejected preset mutation changed session")
	}
}

func TestAgentPresetColdActivationCommitsBeforePublishingOwner(t *testing.T) {
	_, runtime := reviewRuntime(t)
	prepared, err := runtime.Session().PrepareBatchContext(t.Context(), "cold-preset", Batch{Events: []Event{
		{Kind: AgentPresetKind, Payload: []byte(`{"preset":"review"}`)},
		{Kind: "model/context-replace", Payload: []byte(`{"messages":[{"role":"system","id":"persona","content":"LATEST"}],"reason":"agent-preset"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	exec := &testExecution{phase: RuntimeIdle, runtime: runtime}
	generation, _, err := runtime.ReplaceExecutionAndCommit(0, exec, prepared)
	if err != nil || generation == 0 || !runtime.OwnsExecution(generation) {
		t.Fatalf("cold activation=%d err=%v", generation, err)
	}
	snapshot := runtime.Session().ExecutionSnapshot()
	if snapshot.Projection.AgentPreset != "review" || len(snapshot.Projection.ModelMessages) != 1 || snapshot.Projection.ModelMessages[0].Role != provider.RoleSystem {
		t.Fatal("cold activation published owner without prepared persona")
	}
}
