// Package sessiontool provides list_sessions and read_session tools that let
// the AI discover and read past conversation sessions, enabling cross-session
// AI context sharing through the host's current Session Query.
package sessiontool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/textutil"
	"deepzora/internal/tool"
)

// list_sessions tool

// Scope resolves current host membership on every request. Nil uses the whole
// supplied catalog; a non-nil scope with no IDs grants access to no sessions.
type Scope func(context.Context) (map[string]bool, error)

type listSessionsTool struct {
	query *session.Query
	scope Scope
}

// NewListSessionsTool creates a tool that lists saved sessions.
func NewListSessionsTool(query *session.Query, scope Scope) *listSessionsTool {
	return &listSessionsTool{query: query, scope: scope}
}

func (t *listSessionsTool) Name() string   { return tool.HostListSessions }
func (t *listSessionsTool) ReadOnly() bool { return true }

func (t *listSessionsTool) Description() string {
	return "List saved AI conversation sessions in the current host catalog, newest first. Returns timestamp, model, turn count, preview, and session ID. Use read_session with the ID to view a conversation."
}

func (t *listSessionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *listSessionsTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	if t.query == nil {
		return "", fmt.Errorf("list_sessions: current session catalog is unavailable")
	}
	var allowed map[string]bool
	if t.scope != nil {
		var err error
		allowed, err = t.scope(ctx)
		if err != nil {
			return "", fmt.Errorf("list_sessions: %w", err)
		}
	}
	var ordered []session.SessionInfo
	for cursor := ""; ; {
		page, err := t.query.List(ctx, cursor, 100)
		if err != nil {
			return "", fmt.Errorf("list_sessions: %w", err)
		}
		for _, info := range page.Sessions {
			if t.scope == nil || allowed[info.SessionID] {
				ordered = append(ordered, info)
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(ordered) == 0 {
		return "No sessions found.\n", nil
	}

	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt) })
	var b strings.Builder
	fmt.Fprintf(&b, "# Saved Sessions (%d total)\n\n", len(ordered))
	b.WriteString("| # | Timestamp | Model | Turns | Preview | Session ID\n")
	b.WriteString("|---|-----------|-------|-------|-----------------|-----\n")
	for i, s := range ordered {
		ts := s.UpdatedAt.Format("2006-01-02 15:04")
		fmt.Fprintf(&b, "| %d | %s | %s | %d | %s | `%s`\n",
			i+1, ts, s.ModelRef, s.Turns, s.Preview, s.SessionID)
	}
	b.WriteString("\nUse `read_session` with the Session ID to view the session.\n")
	return b.String(), nil
}

// read_session tool

type readSessionTool struct {
	query *session.Query
	scope Scope
}

// NewReadSessionTool creates a tool that reads saved sessions.
func NewReadSessionTool(query *session.Query, scope Scope) *readSessionTool {
	return &readSessionTool{query: query, scope: scope}
}

func (t *readSessionTool) Name() string   { return tool.HostReadSession }
func (t *readSessionTool) ReadOnly() bool { return true }

func (t *readSessionTool) Description() string {
	return `Read a saved AI conversation by session ID from the current host catalog. Each message is truncated to 2000 grapheme clusters. Excludes reasoning, system prompts, and tool result content (opt-in via show_tool_results). Use list_sessions to discover available sessions.`
}

func (t *readSessionTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
	"properties": {
		"session": {
			"type": "string",
			"description": "Session ID from list_sessions. Filesystem paths and old session filenames are not accepted."
		},
    "max_turns": {
      "type": "integer",
      "description": "Maximum user-assistant turns to return (default 50). 0 = no limit."
    },
    "show_tool_results": {
      "type": "boolean",
      "description": "When true, include tool result content (default false). Tool results may contain secrets, command output, environment data, or private file contents."
    }
  },
  "required": ["session"]
}`)
}

func (t *readSessionTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Session         string `json:"session"`
		MaxTurns        *int   `json:"max_turns"`
		ShowToolResults bool   `json:"show_tool_results"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("read_session: invalid args: %w", err)
	}
	if params.Session == "" {
		return "", fmt.Errorf("read_session: 'session' argument is required")
	}

	if t.query == nil {
		return "", fmt.Errorf("read_session: current session catalog is unavailable")
	}
	if t.scope != nil {
		allowed, err := t.scope(ctx)
		if err != nil {
			return "", fmt.Errorf("read_session: %w", err)
		}
		if !allowed[params.Session] {
			return "", fmt.Errorf("read_session: session is outside the current workspace")
		}
	}
	ref, err := t.query.ResolveSessionID(ctx, params.Session)
	if err != nil {
		return "", fmt.Errorf("read_session: %w", err)
	}
	msgs, err := t.query.History(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("read_session: %w", err)
	}
	if len(msgs) == 0 {
		return "Session is empty.\n", nil
	}

	// Parse max_turns: default 50, 0 means no limit.
	maxTurns := 50
	if params.MaxTurns != nil {
		if *params.MaxTurns == 0 {
			maxTurns = 0
		} else if *params.MaxTurns > 0 {
			maxTurns = *params.MaxTurns
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Session: %s\n", ref.SessionID)

	turnCount := 0
loop:
	for _, m := range msgs {
		if agent.IsPinnedContextRevision(m) {
			continue
		}
		switch m.Role {
		case provider.RoleSystem:
			// System prompts excluded for privacy (matching history tool).

		case provider.RoleUser:
			turnCount++
			if maxTurns > 0 && turnCount > maxTurns {
				b.WriteString("... (truncated, use max_turns to increase limit)\n")
				break loop
			}
			fmt.Fprintf(&b, "## User (turn %d)\n", turnCount)
			b.WriteString(truncateRunes(m.Content, 2000))
			b.WriteString("\n\n")

		case provider.RoleAssistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "## Assistant (turn %d)\n", max(turnCount, 1))
				b.WriteString(truncateRunes(m.Content, 2000))
				b.WriteString("\n\n")
			}
			if len(m.ToolCalls) > 0 {
				b.WriteString("### Tool Calls\n\n")
				for _, tc := range m.ToolCalls {
					fmt.Fprintf(&b, "- `%s(%s)`\n", tc.Name, truncateRunes(string(tc.Arguments), 1200))
				}
				b.WriteString("\n")
			}

		case provider.RoleTool:
			fmt.Fprintf(&b, "### Tool Result: %s\n\n", m.Name)
			if params.ShowToolResults && m.Content != "" {
				b.WriteString(truncateRunes(m.Content, 2000))
				b.WriteString("\n\n")
			}
		}
	}

	return b.String(), nil
}

// helpers

// truncateRunes truncates by grapheme clusters to preserve visible characters.
func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	return textutil.TruncateGraphemes(s, max, "...")
}
