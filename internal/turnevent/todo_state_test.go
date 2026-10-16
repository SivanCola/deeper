package turnevent

import (
	"deepzora/internal/event"
	"testing"
)

func TestTodoStateIsCommittedWithToolResultAndClearedByCommittedStart(t *testing.T) {
	ledger := NewMemory("session")
	turnID, err := ledger.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ledger.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append start: ok=%v err=%v", ok, err)
	}
	todos := []event.Todo{{Content: "inspect", Status: "completed"}, {Content: "ship", Status: "in_progress"}}
	if _, ok, err := ledger.Append(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "todo-1", Name: "todo_write", TodoWritten: true, Todos: todos}}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append todo result: ok=%v err=%v", ok, err)
	}
	if got, written := ledger.TodoState(); !written || len(got) != 2 || got[1] != todos[1] {
		t.Fatalf("live todo state = %+v written=%v", got, written)
	}
	if _, ok, err := ledger.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
		t.Fatalf("append terminal: ok=%v err=%v", ok, err)
	}
	if err := ledger.AcknowledgeProjection(turnID); err != nil {
		t.Fatal(err)
	}

	if got, written := ledger.TodoState(); !written || len(got) != 2 || got[0] != todos[0] {
		t.Fatalf("terminal todo state = %+v written=%v", got, written)
	}
	if _, err := ledger.Begin(); err != nil {
		t.Fatal(err)
	}
	if got, written := ledger.TodoState(); !written || len(got) != 2 {
		t.Fatalf("turn allocation changed todo state before commit = %+v written=%v", got, written)
	}
	if _, ok, err := ledger.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress); err != nil || !ok {
		t.Fatalf("append new start: ok=%v err=%v", ok, err)
	}
	if got, written := ledger.TodoState(); written || len(got) != 0 {
		t.Fatalf("new turn retained todo state = %+v written=%v", got, written)
	}
}

func TestExplicitEmptyTodoStateSurvivesReplay(t *testing.T) {
	ledger := NewMemory("session")
	if _, err := ledger.Begin(); err != nil {
		t.Fatal(err)
	}
	_, _, _ = ledger.Append(event.Event{Kind: event.TurnStarted}, event.TurnInProgress)
	_, _, _ = ledger.Append(event.Event{Kind: event.ToolResult, Tool: event.Tool{Name: "todo_write", TodoWritten: true, Todos: []event.Todo{}}}, event.TurnInProgress)
	_, _, _ = ledger.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted)
	if got, written := ledger.TodoState(); !written || len(got) != 0 {
		t.Fatalf("empty todo state = %+v written=%v", got, written)
	}
}

func TestRecoveryStatusPreservesExactReason(t *testing.T) {
	ledger := NewMemory("session")
	if _, err := ledger.Begin(); err != nil {
		t.Fatal(err)
	}
	want := &event.RecoveryStatus{State: "recovery_required", Reason: "cancellation_grace_expired", Phase: "tool_execution", RequiresUserDecision: true}
	if _, ok, err := ledger.Append(event.Event{Kind: event.TurnDone, Recovery: want}, event.TurnRecoveryRequired); err != nil || !ok {
		t.Fatalf("append recovery = (%v, %v)", ok, err)
	}

	got := ledger.RecoveryStatus()
	if got == nil || got.Reason != want.Reason || got.Phase != want.Phase || !got.RequiresUserDecision {
		t.Fatalf("recovery = %#v, want %#v", got, want)
	}
}
