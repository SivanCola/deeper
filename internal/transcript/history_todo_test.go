package transcript

import (
	"encoding/json"
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/tool"
	_ "deepzora/internal/tool/builtin"
)

func TestHistoryUsesCurrentTodoResultsWithoutSerialRepair(t *testing.T) {
	writer, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write is not registered")
	}
	for _, args := range []string{
		`{"todos":[{"content":" first ","status":"pending"},{"content":"done","status":"completed"},{"content":"parallel A","status":"in_progress"},{"content":"parallel B","status":"in_progress"}]}`,
		`{"todos":[]}`,
	} {
		output, err := writer.Execute(t.Context(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Todos json.RawMessage `json:"todos"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatal(err)
		}
		want := `{"todos":` + string(result.Todos) + `}`
		messages := []provider.Message{
			{ID: "write", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "todo", Name: "todo_write", Arguments: args}}},
			{ID: "result", Role: provider.RoleTool, ToolCallID: "todo", Name: "todo_write", Content: output},
			{ID: "retired", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "step", Name: "complete_step", Arguments: `{"step":"first"}`}}},
			{ID: "retired-result", Role: provider.RoleTool, ToolCallID: "step", Name: "complete_step", Content: "signed off"},
		}
		rows := History(messages, HistoryOptions{})
		if got := rows[0].ToolCalls[0].Arguments; got != want {
			t.Fatalf("display todos = %s, want %s", got, want)
		}
		if messages[0].ToolCalls[0].Arguments != args {
			t.Fatal("history presentation rewrote persisted tool arguments")
		}
	}
}

func TestHistoryDoesNotRepairTodoFromOldOrFailedResults(t *testing.T) {
	const args = `{"todos":[{"content":"first","status":"pending"},{"content":"second","status":"completed"}]}`
	for _, output := range []string{"", "updated", "Model task list updated:", "error: invalid todos", `{"todos":null}`} {
		messages := []provider.Message{
			{ID: "write", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "todo", Name: "todo_write", Arguments: args}}},
			{ID: "result", Role: provider.RoleTool, ToolCallID: "todo", Content: output},
		}
		if got := History(messages, HistoryOptions{})[0].ToolCalls[0].Arguments; got != args {
			t.Fatalf("result %q changed todo arguments to %s", output, got)
		}
	}
}
