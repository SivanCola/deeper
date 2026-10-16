package main

import (
	"errors"
	"strings"
	"time"

	"deepzora/internal/history"
	"deepzora/internal/historycatalog"
	"deepzora/internal/provider"
)

type HistorySessionPageRequest struct {
	Scope         string `json:"scope"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	Status        string `json:"status"`
	TimeFilter    string `json:"timeFilter"`
	Query         string `json:"query"`
	Cursor        string `json:"cursor"`
	Limit         int    `json:"limit"`
}

type HistorySessionPage struct {
	SnapshotID        string        `json:"snapshotId,omitempty"`
	SnapshotExpiresAt int64         `json:"snapshotExpiresAt,omitempty"`
	ReadError         *ReadError    `json:"readError,omitempty"`
	Items             []SessionMeta `json:"items"`
	NextCursor        string        `json:"nextCursor"`
	Revision          uint64        `json:"revision"`
	Partial           bool          `json:"partial"`
	StaleCursor       bool          `json:"staleCursor"`
}

type HistorySearchRequest struct {
	Query         string   `json:"query"`
	Scope         string   `json:"scope"`
	WorkspaceRoot string   `json:"workspaceRoot,omitempty"`
	Status        string   `json:"status"`
	TimeFilter    string   `json:"timeFilter"`
	Kinds         []string `json:"kinds"`
	ToolName      string   `json:"toolName"`
	Cursor        string   `json:"cursor"`
	Limit         int      `json:"limit"`
}

type HistorySearchHit struct {
	PartIndex      int     `json:"partIndex,omitempty"`
	ContentDigest  string  `json:"contentDigest"`
	Catalog        string  `json:"catalog"`
	SessionID      string  `json:"sessionId"`
	Source         string  `json:"source"`
	MessageIndex   int     `json:"messageIndex"`
	Role           string  `json:"role"`
	Kind           string  `json:"kind"`
	ToolName       string  `json:"toolName,omitempty"`
	Snippet        string  `json:"snippet"`
	Score          float64 `json:"score"`
	SessionTitle   string  `json:"sessionTitle,omitempty"`
	TopicTitle     string  `json:"topicTitle,omitempty"`
	WorkspaceRoot  string  `json:"workspaceRoot,omitempty"`
	LastActivityAt int64   `json:"lastActivityAt"`
	Open           bool    `json:"open"`
	Running        bool    `json:"running"`
	Current        bool    `json:"current"`
}

type HistoryIndexStatus = historycatalog.Status

type HistoryIndexChangedV1 struct {
	Revision uint64   `json:"revision"`
	Indexed  int64    `json:"indexed"`
	Total    int64    `json:"total"`
	Pending  int64    `json:"pending"`
	Roots    []string `json:"roots"`
	Reason   string   `json:"reason"`
}

func (a *App) registerHistoryIndexEvents() {
	history.RegisterCatalogObserver(func(status historycatalog.Status, roots []string, reason string) {
		if roots == nil {
			roots = []string{}
		}
		a.emitRuntimeEvent("history-index:changed-v1", HistoryIndexChangedV1{Revision: status.Revision,
			Indexed: status.Indexed, Total: status.Total, Pending: status.Pending, Roots: roots, Reason: reason})
	})
}

type HistorySearchPage struct {
	SnapshotID        string             `json:"snapshotId,omitempty"`
	SnapshotExpiresAt int64              `json:"snapshotExpiresAt,omitempty"`
	ReadError         *ReadError         `json:"readError,omitempty"`
	Items             []HistorySearchHit `json:"items"`
	NextCursor        string             `json:"nextCursor"`
	Revision          uint64             `json:"revision"`
	Partial           bool               `json:"partial"`
	StaleCursor       bool               `json:"staleCursor"`
	Status            HistoryIndexStatus `json:"status"`
}

type HistorySearchContextRequest struct {
	ContentDigest string `json:"contentDigest"`
	Catalog       string `json:"catalog"`
	SessionID     string `json:"sessionId"`
	MessageIndex  int    `json:"messageIndex"`
	Before        int    `json:"before"`
	After         int    `json:"after"`
}

type HistorySearchContextLine struct {
	Index int    `json:"index"`
	Role  string `json:"role"`
	Text  string `json:"text"`
}

func (a *App) GetHistoryIndexStatus() HistoryIndexStatus {
	if catalog := history.SharedCatalog(); catalog != nil {
		return catalog.Status()
	}
	return HistoryIndexStatus{State: "opening", Pending: 1, Mode: "memory"}
}

func desktopHistoryText(messages []provider.Message, candidate historycatalog.Candidate) (string, bool) {
	if candidate.MessageIndex < 0 || candidate.MessageIndex >= len(messages) {
		return "", false
	}
	message := messages[candidate.MessageIndex]
	switch candidate.Kind {
	case "user_text", "assistant_text":
		return message.Content, true
	case "tool_input":
		if candidate.PartIndex < 0 || candidate.PartIndex >= len(message.ToolCalls) {
			return "", false
		}
		call := message.ToolCalls[candidate.PartIndex]
		return strings.TrimSpace(call.Name + " " + call.Arguments), true
	case "tool_error", "tool_output":
		return strings.TrimSpace(message.Name + " " + message.Content), true
	default:
		return "", false
	}
}

func historyTimeMatchesAt(timestamp int64, filter string, now time.Time) bool {
	if strings.TrimSpace(filter) == "" || filter == "all" {
		return true
	}
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	value := time.UnixMilli(timestamp)
	switch filter {
	case "today":
		return !value.Before(startToday)
	case "yesterday":
		return !value.Before(startToday.AddDate(0, 0, -1)) && value.Before(startToday)
	case "older":
		return value.Before(startToday.AddDate(0, 0, -1))
	default:
		return true
	}
}

func (a *App) GetHistorySearchContext(req HistorySearchContextRequest) []HistorySearchContextLine {
	out := []HistorySearchContextLine{}
	if strings.TrimSpace(req.ContentDigest) == "" || req.MessageIndex < 0 {
		return out
	}
	entry, allowed := a.historyContextSource(a.bootContext(), req.Catalog, req.SessionID)
	if !allowed {
		return out
	}
	generation, err := entry.query.StorageGeneration(a.bootContext(), entry.info.Ref)
	if err != nil {
		return out
	}
	messages, digest, _, err := historycatalog.ReadCurrent(a.bootContext(), entry.query, entry.info.Ref)
	if err != nil || digest != req.ContentDigest || req.MessageIndex >= len(messages) {
		return out
	}
	fence := historyCurrentFence{app: a, entries: map[string]historyCurrentSource{entry.info.Path: entry}, generations: map[string]string{entry.info.Path: generation}}
	if fence.validate() != nil {
		return out
	}
	start := max(0, req.MessageIndex-min(max(req.Before, 0), 20))
	end := req.MessageIndex + min(min(max(req.After, 0), 20), len(messages)-req.MessageIndex-1) + 1
	for i := start; i < end; i++ {
		out = append(out, HistorySearchContextLine{Index: i, Role: string(messages[i].Role), Text: messages[i].Content})
	}
	return out
}

func (a *App) RebuildHistoryIndex() error {
	if a == nil || a.shuttingDown.Load() {
		return errors.New("application is shutting down")
	}
	roots, err := a.currentHistoryCatalogRoots()
	if err != nil {
		return err
	}
	return history.RebuildSharedCatalog(a.bootContext(), roots)
}
