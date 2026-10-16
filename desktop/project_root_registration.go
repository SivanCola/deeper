package main

// registerProjectRoot records workspaceRoot, realigns open tabs to its
// canonical spelling, and refreshes current history/task query roots.
func (a *App) registerProjectRoot(workspaceRoot string) error {
	// The project picker is a preference index. Its lock must not prevent an
	// already registered current session from opening its authoritative owner.
	_ = addProject(workspaceRoot, "")
	a.syncTabWorkspaceRootSpellings()
	a.requestSessionCatalogMetadataSync()
	return nil
}
