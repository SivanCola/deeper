package sessiontool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func currentSessionService(t *testing.T) *session.Service {
	t.Helper()
	service, err := session.NewService("tool-host", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.CloseAll(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return service
}

func writeCurrentSession(t *testing.T, service *session.Service, id string, msgs []provider.Message) session.SessionRef {
	t.Helper()
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	for i := range msgs {
		msgs[i].ID = fmt.Sprintf("message-%d", i)
	}
	payload, err := json.Marshal(map[string]any{"messages": msgs, "reason": "test-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Session().Append(t.Context(), session.Batch{OperationID: "fixture", Events: []session.Event{
		{Kind: "history/replace", Payload: payload},
		{Kind: "session/config", Payload: json.RawMessage(`{"modelRef":"test-model"}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	return runtime.Ref()
}

// runTool is a convenience wrapper for calling a tool's Execute with JSON args.
func runTool(t *testing.T, tl interface {
	Execute(context.Context, json.RawMessage) (string, error)
	Name() string
}, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(b))
	if err != nil {
		t.Fatalf("%s: %v", tl.Name(), err)
	}
	return out
}

// list_sessions tests

func TestListSessions_EmptyDir(t *testing.T) {
	service := currentSessionService(t)
	tool := NewListSessionsTool(service.Query(), nil)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No sessions found") {
		t.Errorf("expected 'No sessions found', got: %s", out)
	}
}

func TestToolSchemasAreValidJSON(t *testing.T) {
	service := currentSessionService(t)
	for _, tool := range []struct {
		name   string
		schema json.RawMessage
	}{
		{name: "list_sessions", schema: NewListSessionsTool(service.Query(), nil).Schema()},
		{name: "read_session", schema: NewReadSessionTool(service.Query(), nil).Schema()},
	} {
		if !json.Valid(tool.schema) {
			t.Fatalf("%s schema is invalid JSON: %s", tool.name, tool.schema)
		}
	}
}

func TestListSessions_DeletedSession(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "hello"},
	})
	if err := service.Delete(t.Context(), session.SessionRef{HostID: "tool-host", SessionID: "conversation"}); err != nil {
		t.Fatal(err)
	}

	tool := NewListSessionsTool(service.Query(), nil)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No sessions found") {
		t.Errorf("deleted session should be excluded, got: %s", out)
	}
}

func TestListSessions_SingleSession(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "world"},
	})

	tool := NewListSessionsTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{})

	if !strings.Contains(out, "test-model") {
		t.Errorf("expected model name in output, got: %s", out)
	}
	if !strings.Contains(out, "1 turn") && !strings.Contains(out, "| 1 |") {
		t.Errorf("expected turn count in output, got: %s", out)
	}
}

// read_session tests

func TestReadSession_ValidSession(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Origin: provider.MessageOriginHost, Content: "<pinned_context_revision>private pinned body</pinned_context_revision>"},
		{Role: provider.RoleUser, Content: "user hello"},
		{Role: provider.RoleAssistant, Content: "assistant response"},
	})

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation"})

	if !strings.Contains(out, "user hello") {
		t.Errorf("expected user content, got: %s", out)
	}
	if !strings.Contains(out, "assistant response") {
		t.Errorf("expected assistant content, got: %s", out)
	}
	if strings.Contains(out, "private pinned body") || strings.Contains(out, "turn 2") {
		t.Errorf("pinned revision should be excluded from content and turn counts, got: %s", out)
	}
}

func TestReadSession_ExcludesSystemPrompt(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleSystem, Content: "SECRET_SYSTEM_PROMPT"},
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "hi"},
	})

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation"})

	if strings.Contains(out, "SECRET_SYSTEM_PROMPT") {
		t.Errorf("system prompt should be excluded, got: %s", out)
	}
}

func TestReadSession_ExcludesReasoningContent(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: "answer", ReasoningContent: "PASS_should_not_appear"},
	})

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation"})

	if strings.Contains(out, "PASS_should_not_appear") {
		t.Errorf("reasoning content should be excluded, got: %s", out)
	}
}

func TestReadSession_TruncatesLongContent(t *testing.T) {
	service := currentSessionService(t)
	longContent := strings.Repeat("a", 5000)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "hello"},
		{Role: provider.RoleAssistant, Content: longContent},
	})

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation"})

	if len(out) > 2500 {
		t.Errorf("output too long (%d chars) for truncated content", len(out))
	}
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation marker '...' in output")
	}
}

func TestReadSession_RespectsMaxTurns(t *testing.T) {
	service := currentSessionService(t)
	var msgs []provider.Message
	for range 10 {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: "turn"},
			provider.Message{Role: provider.RoleAssistant, Content: "answer"},
		)
	}
	writeCurrentSession(t, service, "conversation", msgs)

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation", "max_turns": 2})

	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation notice with max_turns=2, got: %s", out)
	}
	if strings.Contains(out, "User (turn 3)") {
		t.Errorf("should not show turn 3 with max_turns=2, got: %s", out)
	}
}

func TestReadSession_MaxTurnsZeroNoLimit(t *testing.T) {
	service := currentSessionService(t)
	var msgs []provider.Message
	for range 60 {
		msgs = append(msgs,
			provider.Message{Role: provider.RoleUser, Content: "turn"},
			provider.Message{Role: provider.RoleAssistant, Content: "answer"},
		)
	}
	writeCurrentSession(t, service, "conversation", msgs)

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation", "max_turns": 0})

	if strings.Contains(out, "truncated") {
		t.Errorf("max_turns=0 should show all turns, got truncation notice")
	}
	if !strings.Contains(out, "User (turn 60)") {
		t.Errorf("expected turn 60 with max_turns=0, got: %s", out)
	}
}

func TestReadSession_RejectsDeletedSession(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "data"},
	})
	if err := service.Delete(t.Context(), session.SessionRef{HostID: "tool-host", SessionID: "conversation"}); err != nil {
		t.Fatal(err)
	}

	tool := NewReadSessionTool(service.Query(), nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"session":"conversation"}`))
	if err == nil {
		t.Fatal("expected error for deleted session, got nil")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Errorf("expected 'session not found' error, got: %v", err)
	}
}

func TestReadSession_RejectsPathTraversal(t *testing.T) {
	service := currentSessionService(t)
	tool := NewReadSessionTool(service.Query(), nil)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"session":"../../etc/passwd"}`))
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}

}

func TestReadSession_ToolResultsOmittedByDefault(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "list files"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "call1", Name: "ls", Arguments: `{"path":"."}`},
		}},
		{Role: provider.RoleTool, Name: "ls", Content: "SECRET_FILE_CONTENT", ToolCallID: "call1"},
		{Role: provider.RoleAssistant, Content: "here are the files"},
	})

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation"})

	if !strings.Contains(out, "Tool Calls") {
		t.Errorf("expected Tool Calls section, got: %s", out)
	}
	if !strings.Contains(out, "Tool Result: ls") {
		t.Errorf("expected Tool Result header, got: %s", out)
	}
	if strings.Contains(out, "SECRET_FILE_CONTENT") {
		t.Errorf("tool result content should be omitted by default, got: %s", out)
	}
}

func TestReadSession_ToolResultsWithOptIn(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{
		{Role: provider.RoleUser, Content: "list files"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "call1", Name: "ls", Arguments: `{"path":"."}`},
		}},
		{Role: provider.RoleTool, Name: "ls", Content: "file1.txt\nfile2.go", ToolCallID: "call1"},
		{Role: provider.RoleAssistant, Content: "here are the files"},
	})

	tool := NewReadSessionTool(service.Query(), nil)
	out := runTool(t, tool, map[string]any{"session": "conversation", "show_tool_results": true})

	if !strings.Contains(out, "file1.txt") {
		t.Errorf("expected tool result content with opt-in, got: %s", out)
	}
}

// helper tests

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		s    string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 10, ""},
		{"   spaced   ", 10, "spaced"},
		{"a👨‍👩‍👧‍👦bc", 2, "a👨‍👩‍👧‍👦..."},
	}
	for _, tt := range tests {
		got := truncateRunes(tt.s, tt.max)
		if got != tt.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
		}
	}
}

func TestSessionToolsRejectOldFilesAndForeignCatalogs(t *testing.T) {
	service := currentSessionService(t)
	other := currentSessionService(t)
	writeCurrentSession(t, other, "foreign", []provider.Message{{Role: provider.RoleUser, Content: "other host data"}})
	oldPath := filepath.Join(t.TempDir(), "old.jsonl")
	if err := os.WriteFile(oldPath, []byte(`{"role":"user","content":"legacy secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{oldPath, "old.jsonl", "../old.jsonl", "foreign"} {
		args, _ := json.Marshal(map[string]string{"session": id})
		if out, err := NewReadSessionTool(service.Query(), nil).Execute(t.Context(), args); err == nil || out != "" {
			t.Fatalf("old/foreign identity %q: %q, %v", id, out, err)
		}
	}
}

func TestSessionToolsRequireCurrentCatalogAndHonorCancellation(t *testing.T) {
	for _, tool := range []interface {
		Execute(context.Context, json.RawMessage) (string, error)
	}{NewListSessionsTool(nil, nil), NewReadSessionTool(nil, nil)} {
		if _, err := tool.Execute(t.Context(), json.RawMessage(`{"session":"conversation"}`)); err == nil {
			t.Fatal("missing current catalog accepted")
		}
	}
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{{Role: provider.RoleUser, Content: "hello"}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tool := range []interface {
		Execute(context.Context, json.RawMessage) (string, error)
	}{NewListSessionsTool(service.Query(), nil), NewReadSessionTool(service.Query(), nil)} {
		if _, err := tool.Execute(ctx, json.RawMessage(`{"session":"conversation"}`)); err == nil {
			t.Fatal("cancelled read succeeded")
		}
	}
}

func TestSessionToolsFailClosedWhenScopeCannotBeRead(t *testing.T) {
	service := currentSessionService(t)
	writeCurrentSession(t, service, "conversation", []provider.Message{{Role: provider.RoleUser, Content: "private"}})
	failure := errors.New("membership unavailable")
	for _, scope := range []Scope{
		func(context.Context) (map[string]bool, error) { return nil, failure },
		func(context.Context) (map[string]bool, error) { return nil, nil },
	} {
		if out, err := NewReadSessionTool(service.Query(), scope).Execute(t.Context(), json.RawMessage(`{"session":"conversation"}`)); err == nil || out != "" {
			t.Fatalf("unavailable scope read: %q, %v", out, err)
		}
		if out, _ := NewListSessionsTool(service.Query(), scope).Execute(t.Context(), json.RawMessage(`{}`)); strings.Contains(out, "conversation") {
			t.Fatalf("unavailable scope listed session: %s", out)
		}
	}
}

func TestReadSessionUsesColdCurrentStoreWithoutTakingWriter(t *testing.T) {
	service := currentSessionService(t)
	ref := writeCurrentSession(t, service, "conversation", []provider.Message{{Role: provider.RoleUser, Content: "persisted content"}})
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if _, live := service.Runtime(ref); live {
		t.Fatal("fixture still owns a runtime")
	}
	out, err := NewReadSessionTool(service.Query(), nil).Execute(t.Context(), json.RawMessage(`{"session":"conversation"}`))
	if err != nil || !strings.Contains(out, "persisted content") {
		t.Fatalf("cold read: %q, %v", out, err)
	}
	if _, live := service.Runtime(ref); live {
		t.Fatal("read acquired a runtime writer")
	}
}
