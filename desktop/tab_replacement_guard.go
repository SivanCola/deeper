package main

import "fmt"

// The publication lock keeps a replacement from escaping onto a closed tab.
// Execution ownership is committed separately by activateReplacementController.
func (a *App) validateTabReplacementLocked(tab *WorkspaceTab, action string) error {
	if !a.ownsRuntimeTabLocked(tab) {
		return fmt.Errorf("tab %q changed while %s; retry", tab.ID, action)
	}
	return nil
}
