package main

import (
	"deepzora/internal/agent"
	"fmt"
	"os"
)

// coldHistorySlice pages a current session through the bounded native pager.
func (a *App) coldHistorySlice(sessionDir, path string, req HistorySliceRequest) (HistorySlice, error) {
	sessionDir, sessionPath, err := a.historyReadSource(sessionDir, path)
	if err != nil {
		return emptyHistorySlice(), err
	}
	info, err := os.Stat(sessionPath)
	if err != nil {
		return emptyHistorySlice(), err
	}
	if info.IsDir() {
		return emptyHistorySlice(), fmt.Errorf("not a session file: %s", sessionPath)
	}
	if page, ok, err := a.pagedColdHistorySlice(a.bootContext(), sessionDir, sessionPath, req); ok {
		return page, err
	}
	return emptyHistorySlice(), agent.ErrDisplayFormatUnsupported
}
