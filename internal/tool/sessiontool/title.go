package sessiontool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/tool"
)

// TitleChangedFunc projects a committed title into the host UI's current session index.
type TitleChangedFunc func(sessionDir, sessionID, title string) error

type setSessionTitleTool struct {
	currentSessionID func() string
	writeEventTitle  func(context.Context, string) error
}

// NewSetSessionTitleEventTool writes through the final session event owner.
func NewSetSessionTitleEventTool(currentSessionID func() string, write func(context.Context, string) error) *setSessionTitleTool {
	return &setSessionTitleTool{currentSessionID: currentSessionID, writeEventTitle: write}
}

const setSessionTitleMaxRunes = 120

func (t *setSessionTitleTool) Name() string   { return tool.HostSetSessionTitle }
func (t *setSessionTitleTool) ReadOnly() bool { return false }
func (t *setSessionTitleTool) Description() string {
	return "Set or clear the current conversation's saved title. Pass an empty title to fall back to the topic title or first-message preview. The host binds the current session; this tool cannot rename other sessions."
}

func (t *setSessionTitleTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "maxLength": 120,
      "description": "Short title for the current conversation. Empty clears the explicit session title."
    }
  },
  "required": ["title"]
}`)
}

func (t *setSessionTitleTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if agent.SubagentDepth(ctx) > 0 {
		return "", fmt.Errorf("set_session_title: only the parent conversation can rename itself")
	}
	var params struct {
		Title *string `json:"title"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("set_session_title: invalid args: %w", err)
	}
	if params.Title == nil {
		return "", fmt.Errorf("set_session_title: 'title' argument is required")
	}
	title := strings.TrimSpace(*params.Title)
	if len([]rune(title)) > setSessionTitleMaxRunes {
		return "", fmt.Errorf("set_session_title: title exceeds %d characters", setSessionTitleMaxRunes)
	}
	if t == nil || t.writeEventTitle == nil || t.currentSessionID == nil || strings.TrimSpace(t.currentSessionID()) == "" {
		return "", fmt.Errorf("set_session_title: current session is unavailable")
	}
	if err := t.writeEventTitle(ctx, title); err != nil {
		return "", fmt.Errorf("set_session_title: %w", err)
	}

	if title == "" {
		return "Cleared the current conversation title.", nil
	}
	return fmt.Sprintf("Set the current conversation title to %q.", title), nil
}
