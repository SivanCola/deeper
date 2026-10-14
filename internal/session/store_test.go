package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestCommitBatchAndProjectionAreAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	todos, _ := json.Marshal(map[string]any{"todos": []event.Todo{{Content: "A", Status: "in_progress"}, {Content: "B", Status: "in_progress"}}})
	commit, err := s.Append(t.Context(), Batch{OperationID: "todo-1", TurnID: "turn-1", Events: []Event{
		{Kind: "turn/start"},
		{Kind: "todo/write", Payload: todos},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if commit.FirstSequence != 1 || commit.EventCount != 2 || commit.Events[1].Sequence != 2 {
		t.Fatalf("commit boundary = %+v", commit)
	}
	projection := s.Snapshot().Projection
	if !projection.TodoWritten || len(projection.Todos) != 2 || projection.Todos[1].Content != "B" {
		t.Fatalf("live todo projection = %+v", projection)
	}
	if _, err := s.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	commits, err := Replay(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Project(commits)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.TodoWritten || len(replayed.Todos) != 2 || replayed.Todos[1].Content != "B" {
		t.Fatalf("replayed todo projection = %+v", replayed)
	}
}

func TestProjectionRebuildsMessagesGoalPlanInteractionsAndRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	message, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "m1", Role: provider.RoleUser, Content: "hello"}})
	interaction, _ := json.Marshal(map[string]any{"id": "p1", "state": "pending"})
	recovery, _ := json.Marshal(event.RecoveryStatus{State: "recovery_required", RequiresUserDecision: true, Reason: "worker did not exit"})
	if _, err := s.Append(t.Context(), Batch{OperationID: "state-1", TurnID: "t1", Events: []Event{
		{Kind: "turn/start"},
		{Kind: "message/complete", Payload: message},
		{Kind: "interaction/created", Payload: interaction},
		{Kind: "plan/state", Payload: json.RawMessage(`{"status":"approved"}`)},
		{Kind: "goal/state", Payload: json.RawMessage(`{"objective":"ship","status":"active"}`)},
		{Kind: "runtime/recovery", Payload: recovery},
	}}); err != nil {
		t.Fatal(err)
	}
	projection := s.Snapshot().Projection
	if len(projection.Messages) != 1 || projection.Messages[0].ID != "m1" || projection.Interactions["p1"] != "pending" {
		t.Fatalf("message/interaction projection = %+v", projection)
	}
	if projection.Recovery == nil || !projection.Recovery.RequiresUserDecision || len(projection.PlanState) == 0 || len(projection.GoalState) == 0 {
		t.Fatalf("state projection = %+v", projection)
	}
}

func TestModelContextReplaceDoesNotRewriteUIHistory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "replace")
	store, err := Open(dir, "replace")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	messages := []provider.Message{{ID: "u1", Role: provider.RoleUser, Content: "new context"}}
	payload, _ := json.Marshal(map[string]any{"messages": messages, "reason": "compaction", "sourceSequences": []uint64{1}})
	if _, err := store.Append(context.Background(), Batch{OperationID: "replace-1", Events: []Event{{Kind: "model/context-replace", Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Projection.Messages
	if len(got) != 0 {
		t.Fatalf("messages = %#v", got)
	}
	derived := store.DeriveMessages()
	if len(derived) != 1 || derived[0].ID != "u1" {
		t.Fatalf("model messages = %#v", derived)
	}
}

func TestMessageUpsertPersistsLocalMetadataWithoutEnteringModelHistory(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "upsert"), "upsert")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	local := provider.Message{ID: "receipt", Role: provider.RoleTool, Name: "", ToolCallID: "", LocalOnly: true, ProtocolRecovery: json.RawMessage(`{"version":1,"id":"r","state":"pending"}`)}
	payload, _ := json.Marshal(map[string]any{"message": local})
	if _, err := store.Append(t.Context(), Batch{OperationID: "upsert-local", Events: []Event{{Kind: "message/upsert", Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot().Projection
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].ID != "receipt" {
		t.Fatalf("ui messages = %#v", snapshot.Messages)
	}
	if len(snapshot.ModelMessages) != 0 {
		t.Fatalf("local receipt leaked into model history: %#v", snapshot.ModelMessages)
	}
	local.ProtocolRecovery = json.RawMessage(`{"version":1,"id":"r","state":"consumed"}`)
	payload, _ = json.Marshal(map[string]any{"message": local})
	if _, err := store.Append(t.Context(), Batch{OperationID: "upsert-local-again", Events: []Event{{Kind: "message/upsert", Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Projection; len(got.Messages) != 1 || len(got.ModelMessages) != 0 {
		t.Fatalf("updated projections = %#v / %#v", got.Messages, got.ModelMessages)
	}
}

func TestMessageCompleteRejectsDuplicateStableIDAtomically(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "duplicates"), "duplicates")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	payload, _ := json.Marshal(map[string]any{"message": provider.Message{ID: "same", Role: provider.RoleUser, Content: "one"}})
	if _, err := store.Append(t.Context(), Batch{OperationID: "one", Events: []Event{{Kind: "message/complete", Payload: payload}}}); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	payload, _ = json.Marshal(map[string]any{"message": provider.Message{ID: "same", Role: provider.RoleAssistant, Content: "two"}})
	if _, err := store.Append(t.Context(), Batch{OperationID: "two", Events: []Event{{Kind: "message/complete", Payload: payload}}}); err == nil {
		t.Fatal("duplicate stable id accepted")
	}
	after := store.Snapshot()
	if after.EventSequence != before.EventSequence || len(after.Projection.Messages) != 1 {
		t.Fatalf("failed batch mutated state: before=%+v after=%+v", before, after)
	}
}

func TestCompactionReplacesOnlyModelProjection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "compaction")
	store, err := Open(dir, "compaction")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	original := provider.Message{ID: "u1", Role: provider.RoleUser, Content: "original"}
	messagePayload, _ := json.Marshal(map[string]any{"message": original})
	projected := []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "summary"}}
	compactionPayload, _ := json.Marshal(map[string]any{"messages": projected, "trigger": "manual"})
	if _, err := store.Append(context.Background(), Batch{OperationID: "compact", Events: []Event{
		{Kind: "message/complete", Payload: messagePayload},
		{Kind: "compaction", Payload: compactionPayload},
	}}); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot().Projection
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].ID != "u1" {
		t.Fatalf("canonical messages changed: %#v", snapshot.Messages)
	}
	if len(snapshot.ModelMessages) != 1 || snapshot.ModelMessages[0].ID != "summary" {
		t.Fatalf("model projection = %#v", snapshot.ModelMessages)
	}
}

func TestRestartRecoveryClosesTurnToolsAndInteractionsWithoutReplay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recover")
	first, err := Open(dir, "recover")
	if err != nil {
		t.Fatal(err)
	}
	call := json.RawMessage(`{"id":"call-1","name":"bash"}`)
	interaction := json.RawMessage(`{"id":"approval-1","state":"pending"}`)
	if _, err := first.Append(t.Context(), Batch{OperationID: "open-turn", TurnID: "turn-1", Events: []Event{
		{Kind: "turn/start"}, {Kind: "tool/start", Payload: call}, {Kind: "interaction/created", Payload: interaction},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	second, err := Open(dir, "recover")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	if _, recovered, err := second.RecoverInterrupted(t.Context()); err != nil || !recovered {
		t.Fatalf("RecoverInterrupted = recovered %v, err %v", recovered, err)
	}
	projection := second.Snapshot().Projection
	if projection.TurnID != "" || projection.TurnStatus != event.TurnInterrupted || len(projection.ActiveTools) != 0 || len(projection.Interactions) != 0 {
		t.Fatalf("recovered projection = %+v", projection)
	}
}

func TestAppendValidationDoesNotChangeLiveState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	tests := []Event{
		{Kind: "todo/write", Payload: json.RawMessage(`{"todos":[{"content":" A ","status":"pending"}]}`)},
		{Kind: "interaction/created", Payload: json.RawMessage(`{"id":"p","state":"answered"}`)},
		{Kind: "interaction/resolved", Payload: json.RawMessage(`{"id":"p","state":"pending"}`)},
		{Kind: "message/complete", Payload: json.RawMessage(`{"message":{"role":"user","content":"missing id"}}`)},
		{Kind: "plan/state", Payload: json.RawMessage(`[]`)},
	}
	for i, invalid := range tests {
		before := s.Snapshot()
		_, err := s.Append(t.Context(), Batch{OperationID: "invalid-" + string(rune('a'+i)), Events: []Event{invalid}})
		if !errors.Is(err, ErrDamagedStore) {
			t.Fatalf("event %s error = %v", invalid.Kind, err)
		}
		after := s.Snapshot()
		if after.EventSequence != before.EventSequence {
			t.Fatalf("invalid %s advanced sequence: before=%d after=%d", invalid.Kind, before.EventSequence, after.EventSequence)
		}
	}
}

func TestSessionConfigIsProjectedFromTheEventStream(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "config", Events: []Event{{Kind: "session/config", Payload: json.RawMessage(`{"modelRef":"provider/model","modelIdentity":"revision-1"}`)}}}); err != nil {
		t.Fatal(err)
	}
	if projection := s.Snapshot().Projection; projection.ModelRef != "provider/model" || projection.ModelIdentity != "revision-1" {
		t.Fatalf("live model selection = %q / %q", projection.ModelRef, projection.ModelIdentity)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	commits, err := Replay(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := Project(commits)
	if err != nil {
		t.Fatal(err)
	}
	if projection.ModelRef != "provider/model" || projection.ModelIdentity != "revision-1" {
		t.Fatalf("replayed model selection = %q / %q", projection.ModelRef, projection.ModelIdentity)
	}
}

func TestReplayIgnoresTornTailAndWriterRecoversUnderLease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "turn", Events: []Event{{Kind: "turn/start"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, currentLogName)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"torn`)
	_ = file.Close()
	commits, err := Replay(dir, nil)
	if err != nil || len(commits) != 1 {
		t.Fatalf("Replay partial tail = %d, %v", len(commits), err)
	}
	reopened, err := Open(dir, "s")
	if err != nil {
		t.Fatalf("writer recovery: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "events.torn-*.tail"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("torn tail backups = %v, %v", backups, err)
	}
	tail, err := os.ReadFile(backups[0])
	if err != nil || string(tail) != `{"torn` {
		t.Fatalf("preserved tail = %q, %v", tail, err)
	}
	if _, err := reopened.Append(t.Context(), Batch{OperationID: "after-recovery", Events: []Event{{Kind: "diagnostic", Optional: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	commits, err = Replay(dir, nil)
	if err != nil || len(commits) != 2 {
		t.Fatalf("Replay recovered log = %d, %v", len(commits), err)
	}
}

func TestReplayRejectsUnknownRequiredEventAndAllowsOptionalDiagnostic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	s, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "unknown", Events: []Event{{Kind: "future/required"}}}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("unknown required append error = %v", err)
	}
	if _, err := s.Append(t.Context(), Batch{OperationID: "diagnostic", Events: []Event{{Kind: "future/diagnostic", Optional: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if commits, err := Replay(dir, nil); err != nil || len(commits) != 1 {
		t.Fatalf("optional diagnostic replay = %d, %v", len(commits), err)
	}
}

func TestOpenUsesSingleWriterLeaseAndAdvancesGeneration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	first, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, "s"); !errors.Is(err, ErrWriterOwned) {
		t.Fatalf("second writer error = %v", err)
	}
	firstGeneration := first.manifest.WriterGeneration
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	if second.manifest.WriterGeneration != firstGeneration+1 {
		t.Fatalf("writer generation = %d, want %d", second.manifest.WriterGeneration, firstGeneration+1)
	}
}
