package history

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/tool/sessiontool"
)

// Kind identifies the part of a saved message indexed for retrieval.
type Kind string

const (
	KindUserText      Kind = "user_text"
	KindAssistantText Kind = "assistant_text"
	KindToolInput     Kind = "tool_input"
	KindToolError     Kind = "tool_error"
	KindToolOutput    Kind = "tool_output"
)

const (
	scopeProject = "project"
	scopeGlobal  = "global"

	defaultLimit  = 8
	maxLimit      = 20
	defaultAround = 3
	maxAround     = 10
	maxSnippet    = 240
	scoreFloor    = 0.15
)

var defaultKinds = map[Kind]bool{
	KindUserText:      true,
	KindAssistantText: true,
	KindToolInput:     true,
	KindToolError:     true,
}

// Source grants access to current sessions through an explicit host catalog.
// Membership is resolved on every operation; nil allows the supplied catalog.
type Source struct {
	Query      *session.Query
	Scope      string
	Membership sessiontool.Scope
}

type Options struct{ Sources []Source }

// SearchRequest describes a history search.
type SearchRequest struct {
	Query    string
	Scope    string
	Kinds    []Kind
	ToolName string
	Limit    int
}

// AroundRequest fetches messages adjacent to a search hit.
type AroundRequest struct {
	CatalogID     string
	SessionID     string
	ContentDigest string
	MessageIndex  int
	Before        int
	After         int
}

// Hit is a ranked search result.
type Hit struct {
	Score         float64
	CatalogID     string
	SessionID     string
	ContentDigest string
	Source        string
	MessageIndex  int
	Role          provider.Role
	Kind          Kind
	ToolName      string
	Snippet       string
}

// MessageContext is one message returned by Around.
type MessageContext struct {
	Index int
	Text  string
}

func normalizeScope(scope string) (string, error) {
	switch strings.TrimSpace(scope) {
	case "", scopeProject:
		return scopeProject, nil
	case scopeGlobal:
		return scopeGlobal, nil
	default:
		return "", fmt.Errorf("scope must be %q or %q", scopeProject, scopeGlobal)
	}
}

func normalizeKinds(kinds []Kind) (map[Kind]bool, error) {
	if len(kinds) == 0 {
		out := make(map[Kind]bool, len(defaultKinds))
		maps.Copy(out, defaultKinds)
		return out, nil
	}
	out := map[Kind]bool{}
	for _, k := range kinds {
		switch k {
		case KindUserText, KindAssistantText, KindToolInput, KindToolError, KindToolOutput:
			out[k] = true
		default:
			return nil, fmt.Errorf("unknown kind %q", k)
		}
	}
	return out, nil
}

func renderMessage(idx int, msg provider.Message) string {
	var b strings.Builder
	switch msg.Role {
	case provider.RoleUser:
		fmt.Fprintf(&b, "[%d user]\n%s", idx, truncate(stripComposePrefixes(msg.Content), 2000))
	case provider.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" {
			fmt.Fprintf(&b, "[%d assistant]\n%s", idx, truncate(msg.Content, 2000))
		} else {
			fmt.Fprintf(&b, "[%d assistant]", idx)
		}
		for _, call := range msg.ToolCalls {
			fmt.Fprintf(&b, "\n[tool call: %s]\n%s", call.Name, truncate(call.Arguments, 1200))
		}
	case provider.RoleTool:
		fmt.Fprintf(&b, "[%d tool %s result]\n%s", idx, msg.Name, truncate(msg.Content, 2000))
	case provider.RoleSystem:
		fmt.Fprintf(&b, "[%d system]\n%s", idx, truncate(msg.Content, 1200))
	default:
		fmt.Fprintf(&b, "[%d %s]\n%s", idx, msg.Role, truncate(msg.Content, 2000))
	}
	return strings.TrimSpace(b.String())
}

func truncate(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func clamp(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// MarshalJSON keeps Hit stable if frontends choose to expose the same data later.
func (h Hit) MarshalJSON() ([]byte, error) {
	type hit struct {
		Score         float64       `json:"score"`
		CatalogID     string        `json:"catalog_id"`
		SessionID     string        `json:"session_id"`
		ContentDigest string        `json:"content_digest"`
		Source        string        `json:"source"`
		MessageIndex  int           `json:"message_index"`
		Role          provider.Role `json:"role"`
		Kind          Kind          `json:"kind"`
		ToolName      string        `json:"tool_name,omitempty"`
		Snippet       string        `json:"snippet"`
	}
	return json.Marshal(hit(h))
}
