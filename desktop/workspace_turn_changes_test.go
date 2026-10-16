package main

import (
	"deepzora/internal/checkpoint"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"testing"
)

type turnResultReader struct {
	control.SessionAPI
	path      string
	changes   *checkpoint.TurnChanges
	afterRead func()
}

func (r *turnResultReader) SessionRoute() string { return r.path }
func (r *turnResultReader) CheckpointTurnChanges(int) *checkpoint.TurnChanges {
	if r.afterRead != nil {
		r.afterRead()
	}
	return r.changes
}
func (r *turnResultReader) History() []provider.Message {
	if r.afterRead != nil {
		r.afterRead()
	}
	return []provider.Message{{Role: provider.RoleTool, ID: "entry-old", ToolCallID: "call", Content: "old log"}, {Role: provider.RoleTool, ID: "entry-new", ToolCallID: "call", Content: "new log"}}
}

func TestWorkspaceTurnResultIsSessionAndResultScoped(t *testing.T) {
	ctrl := &turnResultReader{path: "session-a", changes: &checkpoint.TurnChanges{ID: "result-a", Turn: 0, Coverage: "complete", Files: []checkpoint.TurnFile{{Path: "f", Patch: "frozen"}}, Reasons: []string{}}}
	a := &App{tabs: map[string]*WorkspaceTab{"a": {ID: "a", Ctrl: ctrl}}}
	if got := a.WorkspaceTurnChanges("a", "session-a", 0, "result-a"); got.Coverage != "complete" || got.Files[0].Patch != "" {
		t.Fatalf("summary: %+v", got)
	}
	if got := a.WorkspaceTurnChangeDetail("a", "session-a", 0, "result-a", "f"); got == nil || got.Patch != "frozen" {
		t.Fatalf("detail: %+v", got)
	}
	if a.WorkspaceTurnChangeDetail("a", "session-a", 0, "old-result", "f") != nil {
		t.Fatal("reused turn leaked another result")
	}
	if a.WorkspaceTurnChangeDetail("a", "session-a", 0, "result-a", "../unrecorded") != nil {
		t.Fatal("unrecorded path read")
	}
	if a.TurnCheckLog("a", "session-a", "call", "entry-old").Output != "old log" {
		t.Fatal("reused provider ID selected a newer log")
	}
	if a.TurnCheckLog("a", "session-a", "call", "missing") != nil {
		t.Fatal("missing source ID read succeeded")
	}
	if a.TurnCheckLog("a", "session-b", "call", "entry-old") != nil {
		t.Fatal("wrong session log")
	}
	ctrl.afterRead = func() { ctrl.path = "session-b" }
	if a.WorkspaceTurnChanges("a", "session-a", 0, "result-a").Coverage != "unknown" {
		t.Fatal("session rotated during result read")
	}
	ctrl.path = "session-a"
	if a.TurnCheckLog("a", "session-a", "call", "entry-old") != nil {
		t.Fatal("session rotated during log read")
	}
}

func TestWorkspaceTurnResultMissingControllerHasEmptyArrays(t *testing.T) {
	a := &App{}
	r := a.WorkspaceTurnChanges("missing", "session", 0, "result")
	if r.Files == nil || r.Reasons == nil || r.Coverage != "unknown" {
		t.Fatalf("result: %+v", r)
	}
	if a.WorkspaceTurnChangeDetail("missing", "session", 0, "result", "f") != nil || a.TurnCheckLog("missing", "session", "tool", "entry") != nil {
		t.Fatal("missing controller read succeeded")
	}
}

func TestTurnResultTranscriptCheckpointKeepsWorkspaceChanges(t *testing.T) {
	turn := 0
	receipt := &event.CompletionReceipt{Verdict: "partial", Diff: &checkpoint.TurnChanges{ID: "frozen", Turn: 0, Coverage: "complete", Files: []checkpoint.TurnFile{}, Reasons: []string{}}, Verifications: []event.ReceiptVerification{{Command: "go test ./...", ToolCallID: "check-id", Passed: true}}}
	rows := roundTripDesktopTranscript(t, event.Event{Kind: event.Message, MessageID: "answer", Text: "normal answer"}, event.Event{Kind: event.TurnDone, TurnID: "turn", CheckpointTurn: &turn, Receipt: receipt})
	if len(rows) != 2 || rows[0].Content != "normal answer" || rows[1].CompletionReceipt == nil || rows[1].CheckpointTurn == nil || *rows[1].CheckpointTurn != 0 {
		t.Fatalf("checkpoint rows: %+v", rows)
	}
	got := rows[1].CompletionReceipt
	if got.Verifications[0].ToolCallID != "check-id" || got.Diff.ID != "frozen" || got.Diff.Coverage != "complete" {
		t.Fatalf("checkpoint lost workspace result: %+v", got)
	}
}
