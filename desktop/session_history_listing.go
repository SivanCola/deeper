package main

// ListSessions returns the saved sessions newest-first for the history panel,
// marking the one the current conversation is writing to and attaching any
// user-chosen titles.
func (a *App) ListSessions() []SessionMeta {
	dir := a.activeSessionDir()
	active := a.activeSessionPath(dir)
	if tab := a.activeTab(); tab != nil {
		active = tab.currentSessionIdentity()
	}
	return a.listSessionsFromDir(dir, active)
}

// ListSessionsForTab returns sessions from the directory owned by tabID. Task
// Monitor uses this stable target after asynchronous control lookups so a tab
// switch cannot redirect the eventual session lookup to another workspace.
func (a *App) ListSessionsForTab(tabID string) []SessionMeta {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return []SessionMeta{}
	}
	active := target.sessionPath
	if tab := a.tabByID(tabID); tab != nil {
		active = tab.currentSessionIdentity()
	}
	return a.listSessionsFromDir(target.sessionDir, active)
}

func (a *App) listSessionsFromDir(dir, active string) []SessionMeta {
	return a.listCanonicalSessionsFromDir(dir, active)
}
