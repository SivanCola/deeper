package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestSaveSessionMarkdownForTabExportsCanonicalRecords(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	t.Cleanup(app.closeSessionServices)
	runtime, err := app.desktopSessionService("").Create(t.Context(), session.CreateOptions{SessionID: "markdown"})
	if err != nil {
		t.Fatal(err)
	}
	app.tabs["export"] = &WorkspaceTab{ID: "export", Scope: "global", SessionID: runtime.Ref().SessionID}
	messages := []provider.Message{
		{ID: "user", Role: provider.RoleUser, Content: "question"},
		{ID: "assistant", Role: provider.RoleAssistant, ReasoningContent: "thought", Content: "answer", ToolCalls: []provider.ToolCall{{ID: "call", Name: "bash", Arguments: "echo ```"}}},
		{ID: "result", Role: provider.RoleTool, ToolCallID: "call", ToolRunState: provider.ToolRunCompleted, Content: "done"},
	}
	for _, message := range messages {
		payload, err := json.Marshal(map[string]any{"message": message})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Session().Append(t.Context(), session.Batch{OperationID: message.ID, TurnID: "turn", Events: []session.Event{{Kind: "message/complete", Payload: payload}}}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "session.md")
	if err := app.SaveSessionMarkdownForTab("export", path, "demo\nsession"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"# demo session", "question", "### Reasoning", "thought", "## Assistant", "answer", "### Tool: bash", "````", "done"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export missing %q:\n%s", want, text)
		}
	}
}

func TestWriteStreamingExportDoesNotPublishPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.md")
	err := writeStreamingExport(path, func(dst io.Writer) error {
		_, _ = dst.Write([]byte("partial"))
		return errors.New("stop")
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial destination published: %v", statErr)
	}
}
