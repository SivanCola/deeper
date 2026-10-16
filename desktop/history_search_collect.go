package main

import (
	"strings"
)

func historyStatusMatches(status string, open, current bool) bool {
	switch strings.TrimSpace(status) {
	case "open":
		// Match HistoryPanel: open-but-not-current.
		return open && !current
	case "current":
		return current
	default:
		return true
	}
}
