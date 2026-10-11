package main

import (
	"deepzora/internal/config"
	"deepzora/internal/worktree"
)

func tabUsesIsolatedWorktree(tab *WorkspaceTab) bool {
	return tab != nil && worktree.IsManagedPath(tab.WorkspaceRoot, config.DeliveryWorktreeDir())
}
