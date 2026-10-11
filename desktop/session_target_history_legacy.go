package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type targetHistorySliceCursor struct {
	V      int    `json:"v"`
	Target string `json:"target"`
	Cursor string `json:"cursor"`
}

func decodeTargetHistorySliceCursor(raw, target string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", newSessionOperationError("stale_cursor", "The session content changed. Reload it and try again.")
	}
	var cursor targetHistorySliceCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.V != 1 || cursor.Target != target {
		return "", newSessionOperationError("stale_cursor", "The session content changed. Reload it and try again.")
	}
	return cursor.Cursor, nil
}

func encodeTargetHistorySliceCursor(target, cursor string) string {
	if strings.TrimSpace(cursor) == "" {
		return ""
	}
	data, err := json.Marshal(targetHistorySliceCursor{V: 1, Target: target, Cursor: cursor})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

// HistorySliceForTarget reads a bounded current-session history page without
// selecting it or creating a controller.
func (a *App) HistorySliceForTarget(selector SessionSelector, req HistorySliceRequest) (HistorySlice, error) {
	target, err := a.resolveSessionTargetWithArchived(selector, true)
	if err != nil {
		return emptyHistorySlice(), err
	}
	targetKey := target.key()
	req.Cursor, err = decodeTargetHistorySliceCursor(req.Cursor, targetKey)
	if err != nil {
		return emptyHistorySlice(), err
	}
	req = normalizeHistorySliceRequest(req)
	if target.SessionRef.SessionID != "" {
		page, err := a.canonicalHistorySlice(a.desktopSessionService("").Query(), target.SessionRef, "", "", req)
		page.NextCursor = encodeTargetHistorySliceCursor(targetKey, page.NextCursor)
		return page, err
	}
	return emptyHistorySlice(), newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
}
