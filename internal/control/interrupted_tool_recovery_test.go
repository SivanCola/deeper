package control

import (
	"context"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func currentRecoveryEvidenceFixture(t *testing.T, id, name string, started bool) *interruptedTailEvidence {
	t.Helper()
	c := newCurrentOwnedTestController(t, Options{Sink: event.Discard})
	if err := c.prepareTurnAdmission(func(context.Context) error { return nil })(t.Context()); err != nil {
		t.Fatal(err)
	}
	call := event.Tool{ID: id, Name: name, RunState: provider.ToolRunPending}
	if err := c.emitTurnEventChecked(event.Event{Kind: event.ToolDispatch, Tool: call}); err != nil {
		t.Fatal(err)
	}
	if started {
		call.RunState = provider.ToolRunRunning
		if err := c.emitTurnEventChecked(event.Event{Kind: event.ToolStarted, Tool: call}); err != nil {
			t.Fatal(err)
		}
	}
	return c.currentToolRecoveryEvidence()
}

func TestInterruptedRecoveryPrefersCurrentFactOverPlaceholder(t *testing.T) {
	recovery := &provider.InterruptedTurnRecovery{Pending: true}
	msgs := []provider.Message{
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "write-1", Name: "write_file", Arguments: `{}`}}},
		{Role: provider.RoleTool, ToolCallID: "write-1", Name: "write_file", Content: "[no result: the previous turn was interrupted before this tool call completed]"},
	}
	evidence := currentRecoveryEvidenceFixture(t, "write-1", "write_file", true)
	recordInterruptedAssistantRecovery(recovery, msgs, 0, evidence)
	if len(recovery.UnknownTools) != 1 || recovery.RequiresUserDecision {
		t.Fatalf("recovery=%+v, want one fact-only unknown call", recovery)
	}
	if len(recovery.ToolCalls) != 1 || recovery.ToolCalls[0].State != provider.ToolRunUnknown {
		t.Fatalf("tool records=%+v, want current runtime's unknown state", recovery.ToolCalls)
	}
}

func TestInterruptedRecoveryUsesNotStartedForUndispatchedCurrentCall(t *testing.T) {
	recovery := &provider.InterruptedTurnRecovery{Pending: true}
	msgs := []provider.Message{{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read_file", Arguments: `{}`}}}, {Role: provider.RoleTool, ToolCallID: "read-1", Name: "read_file", Content: "[no result: the previous turn was interrupted before this tool call completed]"}}
	evidence := currentRecoveryEvidenceFixture(t, "read-1", "read_file", false)
	recordInterruptedAssistantRecovery(recovery, msgs, 0, evidence)
	if len(recovery.NotStartedTools) != 1 || len(recovery.UnknownTools) != 0 {
		t.Fatalf("recovery=%+v, want unstarted call outside unknown bucket", recovery)
	}
}
