package main

// A failed reconciliation write keeps the presentation available while
// preventing any persisted session from opening with an uncommitted identity.
func blockSavedTabUnsafeRestore(file desktopTabsFile, reason string) desktopTabsFile {
	file.Tabs = append([]desktopTabEntry(nil), file.Tabs...)
	for i := range file.Tabs {
		if file.Tabs[i].SessionID != "" {
			file.Tabs[i].restoreBlocked = true
			file.Tabs[i].restoreBlockReason = reason
		}
	}
	return file
}
