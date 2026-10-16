package main

import "fmt"

func (a *App) resumeSessionForTranscript(tabID, path string, limit int, includeHistory bool) (HistoryPage, error) {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if tab == nil || ctrl == nil {
		return HistoryPage{}, fmt.Errorf("tab is not ready")
	}
	if _, ok := parseSessionRoute(path); !ok {
		return HistoryPage{}, newSessionOperationError("unsupported", "Use a current session identity.")
	}
	return a.resumeCanonicalSessionForTranscript(tab, ctrl, path, limit, includeHistory)
}
