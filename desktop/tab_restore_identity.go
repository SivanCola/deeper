package main

import "strings"

func prepareRestoredTabIdentity(tab *WorkspaceTab, entry desktopTabEntry) bool {
	tab.goal = strings.TrimSpace(entry.Goal)
	tab.SessionHeadID = entry.SessionHeadID

	if entry.restoreBlocked {
		tab.StartupErr = "Saved session identity could not be verified. Recovery data was preserved."
		return false
	}
	tab.goal = runningTabSessionGoal(strings.TrimSpace(entry.SessionPath), tab.goal)
	restoreTabPinnedContext(tab)
	return true
}
