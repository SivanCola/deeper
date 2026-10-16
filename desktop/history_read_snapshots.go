package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

func snapshotReadError(err error) (*ReadError, bool) {
	if err == nil {
		return nil, false
	}
	var op *SessionOperationError
	if errors.As(err, &op) && op.Code == "stale_cursor" {
		return &ReadError{Code: "stale_cursor", Reason: op.ReadReason, Message: err.Error()}, true
	}
	return &ReadError{Code: "read_failed", Reason: "read_failed", Message: err.Error()}, false
}

func (a *App) ListHistorySessions(req HistorySessionPageRequest) HistorySessionPage {
	out := HistorySessionPage{Items: []SessionMeta{}}
	key := req
	key.Cursor, key.Limit = "", 0
	active := ""
	if tab := a.activeTab(); tab != nil {
		active = tab.currentSessionIdentity()
	}
	binding := snapshotBinding("history-sessions", []any{key, active})
	store := &a.desktopSessions.readSnapshots
	var first *readSnapshot
	var err error
	if req.Cursor == "" {
		first, err = store.build(a.bootContext(), binding, func(ctx context.Context, snap *readSnapshot) error {
			return a.buildHistorySessionSnapshot(ctx, store, snap, req, active)
		})
	}
	if err == nil {
		var meta json.RawMessage
		out.NextCursor, out.SnapshotID, out.SnapshotExpiresAt, meta, err = store.page(a.bootContext(), binding, req.Cursor, first, req.Limit, func(b []byte) error {
			var row SessionMeta
			if err := json.Unmarshal(b, &row); err != nil {
				return err
			}
			out.Items = append(out.Items, row)
			return nil
		})
		if err == nil {
			var frozen HistorySessionPage
			err = json.Unmarshal(meta, &frozen)
			out.Revision, out.Partial = frozen.Revision, frozen.Partial
		}
	}
	if err != nil {
		out = HistorySessionPage{Items: []SessionMeta{}}
		out.ReadError, out.StaleCursor = snapshotReadError(err)
	}
	return out
}

func (a *App) SearchHistoryContent(req HistorySearchRequest) HistorySearchPage {
	out := HistorySearchPage{Items: []HistorySearchHit{}}
	req.Query = strings.TrimSpace(req.Query)
	req.Kinds = append([]string(nil), req.Kinds...)
	if len(req.Kinds) == 0 {
		// The panel searches what the user saw. Every tool result is indexed as
		// tool_output, errors included, so adding tool_error would list them twice.
		req.Kinds = []string{"user_text", "assistant_text", "tool_input", "tool_output"}
	}
	sort.Strings(req.Kinds)
	key := req
	key.Cursor, key.Limit = "", 0
	active := ""
	if tab := a.activeTab(); tab != nil {
		active = tab.currentSessionIdentity()
	}
	binding := snapshotBinding("history-search", []any{key, active})
	store := &a.desktopSessions.readSnapshots
	var first *readSnapshot
	var err error
	if req.Cursor == "" {
		first, err = store.build(a.bootContext(), binding, func(ctx context.Context, snap *readSnapshot) error {
			return a.buildHistorySearchSnapshot(ctx, store, snap, req, active)
		})
	}
	if err == nil {
		var meta json.RawMessage
		out.NextCursor, out.SnapshotID, out.SnapshotExpiresAt, meta, err = store.page(a.bootContext(), binding, req.Cursor, first, req.Limit, func(b []byte) error {
			var row HistorySearchHit
			if err := json.Unmarshal(b, &row); err != nil {
				return err
			}
			out.Items = append(out.Items, row)
			return nil
		})
		if err == nil {
			var frozen HistorySearchPage
			err = json.Unmarshal(meta, &frozen)
			out.Revision, out.Partial, out.Status = frozen.Revision, frozen.Partial, frozen.Status
		}
	}
	if err != nil {
		out = HistorySearchPage{Items: []HistorySearchHit{}}
		out.ReadError, out.StaleCursor = snapshotReadError(err)
		out.Status.LastError = err.Error()
	}
	return out
}
