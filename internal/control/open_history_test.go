package control

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

// Opening a current session must preserve its durable display and model bytes,
// even when the newly constructed executor has a smaller context budget or a
// different startup prompt. Compaction belongs to an explicit maintenance/turn
// boundary, and history reads must not need a provider.
func TestOpenCurrentSessionPreservesHistoryWithoutProviderOrRewrite(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "cold"}[cold], func(t *testing.T) {
			service, err := session.NewService("test", session.NewFilesystemPersistence(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
			runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "saved"})
			if err != nil {
				t.Fatal(err)
			}
			messages := []provider.Message{
				{ID: "system", Role: provider.RoleSystem, Content: "persisted system"},
				{ID: "user", Role: provider.RoleUser, Content: "saved task"},
				{ID: "call", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"file.go"}`}}},
				{ID: "result", Role: provider.RoleTool, Name: "read_file", ToolCallID: "call-1", Content: strings.Repeat("important output\n", 5000)},
				{ID: "answer", Role: provider.RoleAssistant, Content: "saved answer"},
			}
			payload, err := json.Marshal(map[string]any{"messages": messages})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Session().AppendBatch(t.Context(), "seed", []session.Event{{Kind: "history/replace", Payload: payload}}); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Session().Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := service.Query().Snapshot(t.Context(), runtime.Ref())
			if err != nil {
				t.Fatal(err)
			}
			if cold {
				if err := service.Close(t.Context(), runtime.Ref()); err != nil {
					t.Fatal(err)
				}
			}
			exec := agent.New(nil, nil, agent.NewSession("different startup prompt"), agent.Options{ContextWindow: 1000}, event.Discard)
			ctrl := newOwnedTestController(t, Options{Executor: exec, SessionService: service, ExclusiveSession: true})
			ref, err := ctrl.OpenSession(t.Context(), runtime.Ref())
			if err != nil || ref != runtime.Ref() {
				t.Fatalf("OpenSession: %+v, %v", ref, err)
			}
			if !reflect.DeepEqual(exec.Session().Snapshot(), before.Projection.ModelMessages) {
				t.Fatal("opening changed provider-visible history")
			}
			if !reflect.DeepEqual(ctrl.History(), before.Projection.Messages) {
				t.Fatal("opening changed display history")
			}
			after, err := service.Query().Snapshot(t.Context(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if after.EventSequence != before.EventSequence || !reflect.DeepEqual(after.Projection.Messages, before.Projection.Messages) || !reflect.DeepEqual(after.Projection.ModelMessages, before.Projection.ModelMessages) {
				t.Fatal("opening rewrote the saved conversation")
			}
		})
	}
}
