package control

import (
	"reflect"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestCurrentMaintenanceCommitKeepsReceiptAtomicWithModelContext(t *testing.T) {
	ctrl, _, runtime := exclusiveTestController(t, event.Discard)
	before := runtime.Session().StateSnapshot().EventSequence
	state := agent.SessionContextMaintenance{Generation: 1, ProjectionVersion: 1, LineageKey: "current-test",
		Receipt: &agent.ContextMaintenanceReceipt{OperationID: "summary-test", Status: "applied", Action: "summary", ProjectionVersion: 1}}
	result, err := ctrl.RecordSessionModelContext(t.Context(), agent.SessionModelContextCommit{
		OperationID: "summary-test", Reason: "summary", Maintenance: &state,
		Messages: []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "compressed current context"}},
	})
	if err != nil || !result.Accepted || !result.Durable {
		t.Fatalf("commit = %+v, %v", result, err)
	}
	page, err := runtime.Session().Read(t.Context(), before, 10)
	if err != nil || len(page.Commits) != 1 {
		t.Fatalf("context operation = %+v, %v", page, err)
	}
	events := page.Commits[0].Events
	if len(events) != 2 || events[0].Kind != "model/context-replace" || events[1].Kind != session.ContextMaintenanceKind {
		t.Fatalf("receipt was not atomic with context: %+v", events)
	}
	saved := ctrl.SessionContextMaintenance()
	if !reflect.DeepEqual(saved, &state) {
		t.Fatalf("saved = %+v, want %+v", saved, state)
	}
	saved.Receipt.Status = "corrupted"
	if ctrl.SessionContextMaintenance().Receipt.Status != "applied" {
		t.Fatal("reader mutated authoritative receipt")
	}
	state.Generation++
	state.Receipt = &agent.ContextMaintenanceReceipt{OperationID: "blocked-test", Status: "blocked", Action: "summary", ProjectionVersion: 1}
	prior := runtime.Session().ExecutionSnapshot().Projection.ModelMessages
	result, err = ctrl.RecordSessionContextMaintenance(t.Context(), state)
	if err != nil || !result.Durable {
		t.Fatalf("blocked outcome = %+v, %v", result, err)
	}
	if after := runtime.Session().ExecutionSnapshot().Projection.ModelMessages; !reflect.DeepEqual(after, prior) {
		t.Fatal("blocked outcome replaced the model workset")
	}
}
