package main

func currentStartupSession(tab *WorkspaceTab, root, scope, sessionID string) string {
	dir := desktopSessionDir(root)
	if scope == "global" {
		dir = desktopSessionDir(globalWorkspaceRoot())
	}
	prepareStartupPinnedContext(tab, sessionID)
	return dir
}
