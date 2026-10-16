package main

import "strings"

func prepareRestoredTabIdentity(tab *WorkspaceTab, entry desktopTabEntry) bool {
	tab.goal = strings.TrimSpace(entry.Goal)

	if entry.restoreBlocked {
		tab.StartupErr = "Saved session identity could not be verified. Recovery data was preserved."
		return false
	}
	restoreTabPinnedContext(tab)
	return true
}
