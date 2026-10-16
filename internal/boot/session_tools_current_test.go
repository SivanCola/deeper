package boot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent/testutil"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/history"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestUseCapabilityReadsCurrentSessionCatalog(t *testing.T) {
	isolateConfigHome(t)
	if err := history.CloseSharedCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.CloseSharedCatalog(context.Background()) })
	root := robustTempDir(t)
	t.Chdir(root)
	writeFile(t, root, "deepzora.toml", `default_model = "test-model"
[agent]
system_prompt = "BASE"
[[providers]]
name = "test-model"
kind = "boot-token-profile-test"
models = ["x"]
`)
	approveWorkspace(t, root)
	registerBootTokenProfileTestProvider()
	calls := make([]testutil.Turn, 0, 3)
	for _, name := range []string{"list_sessions", "read_session"} {
		args, _ := json.Marshal(map[string]any{"action": "call", "capability_id": "tool:" + name, "arguments": map[string]string{"session": "saved"}})
		calls = append(calls, testutil.Turn{ToolCalls: []provider.ToolCall{{ID: name, Name: "use_capability", Arguments: string(args)}}})
	}
	args, _ := json.Marshal(map[string]any{"action": "call", "capability_id": "tool:history", "arguments": map[string]string{"operation": "search", "query": "sentinel"}})
	calls = append(calls, testutil.Turn{ToolCalls: []provider.ToolCall{{ID: "history", Name: "use_capability", Arguments: string(args)}}})
	calls = append(calls, testutil.Turn{Text: "read current session"})
	prov := testutil.NewMock("current-session-tools", calls...)
	setBootTokenProfileTestProvider(t, prov)
	opts := withTestSession(t, Options{SessionDir: robustTempDir(t), Sink: event.Discard, HeadlessApprovalMode: control.ToolApprovalDangerFullAccess})
	opts.SessionToolScope = func(context.Context) (map[string]bool, error) { return map[string]bool{"saved": true}, nil }
	runtime, err := opts.SessionService.Create(t.Context(), session.CreateOptions{SessionID: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"message":{"id":"saved-message","role":"user","content":"current catalog recall sentinel"}}`)
	if _, err := runtime.Session().AppendBatch(t.Context(), "seed", []session.Event{{Kind: "message/complete", Payload: payload}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := opts.SessionService.Close(t.Context(), runtime.Ref()); err != nil {
		t.Fatal(err)
	}
	ctrl, err := Build(t.Context(), withTestSession(t, opts))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { ctrl.Close(); <-ctrl.Closed() }()
	deadline := time.Now().Add(5 * time.Second)
	for history.SharedCatalog() == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if history.SharedCatalog() == nil {
		t.Fatal("current history catalog did not open")
	}
	if err := history.FlushSharedCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.BindFreshSession(t.Context(), "active"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.RunTurn(t.Context(), "find and read the saved conversation"); err != nil {
		t.Fatal(err)
	}
	var results []string
	for _, message := range ctrl.History() {
		if message.Role == provider.RoleTool {
			results = append(results, message.Content)
		}
	}
	if len(results) != 3 || !strings.Contains(results[0], "Session ID") || !strings.Contains(results[0], "saved") || strings.Contains(results[0], "`active`") || !strings.Contains(results[1], "current catalog recall sentinel") || !strings.Contains(results[2], "session_id=saved") || !strings.Contains(results[2], "current catalog recall sentinel") {
		t.Fatalf("session tool results: %#v", results)
	}
	for _, request := range prov.Requests() {
		if requestHasTool(request, "list_sessions") || requestHasTool(request, "read_session") || requestHasTool(request, "history") {
			t.Fatal("session tools escaped use_capability")
		}
	}
	if _, live := opts.SessionService.Runtime(runtime.Ref()); live {
		t.Fatal("history read acquired the saved session writer")
	}
}
