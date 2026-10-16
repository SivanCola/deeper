package session

import (
	"encoding/json"
	"reflect"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

func TestInvalidMaintenanceReceiptCannotPartiallyReplaceModelContext(t *testing.T) {
	_, _, runtime := newSourceService(t, "maintenance-atomic")
	s := runtime.Session()
	appendReplayEvent(t, s, "initial", "message/complete", map[string]any{"message": provider.Message{ID: "initial", Role: provider.RoleUser, Content: "original context"}})
	before := s.ExecutionSnapshot()
	payload, err := json.Marshal(map[string]any{"messages": []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "summary"}}})
	if err != nil {
		t.Fatal(err)
	}
	state := agent.SessionContextMaintenance{LineageKey: "current", Generation: 1, ProjectionVersion: 1,
		Receipt: &agent.ContextMaintenanceReceipt{OperationID: "summary", Status: "applied", ProjectionVersion: 1}}
	for _, mutate := range []func(*agent.SessionContextMaintenance){
		func(s *agent.SessionContextMaintenance) { s.Generation = 0 },
		func(s *agent.SessionContextMaintenance) { s.LineageKey = "" },
		func(s *agent.SessionContextMaintenance) { s.Receipt.Status = "old-format-status" },
		func(s *agent.SessionContextMaintenance) { s.Receipt.ProjectionVersion = 2 },
	} {
		invalid := agent.CloneSessionContextMaintenance(&state)
		mutate(invalid)
		metadata, err := ContextMaintenanceEvent(*invalid)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AppendBatch(t.Context(), "invalid-maintenance", []Event{{Kind: "model/context-replace", Payload: payload}, metadata})
		if err == nil {
			t.Fatalf("accepted invalid receipt: %+v", invalid)
		}
		after := s.ExecutionSnapshot()
		if after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.ModelMessages, before.Projection.ModelMessages) || after.Projection.ContextMaintenance != nil {
			t.Fatal("rejected metadata partially committed its model context")
		}
	}
}
