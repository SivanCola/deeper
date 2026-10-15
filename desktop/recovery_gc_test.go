package main

import (
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
)

// forkCoveredRecoveryBranch builds the reclaimable shape in dir: a conflict
// fork whose parent went on to contain everything the fork preserved.
func forkCoveredRecoveryBranch(t *testing.T, dir, name string) (parentPath, branchPath string) {
	t.Helper()
	parentPath = filepath.Join(dir, name+".jsonl")
	disk := agent.NewSession("sys")
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	disk.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	disk.Add(provider.Message{Role: provider.RoleUser, Content: "disk " + name})
	if err := disk.Save(parentPath); err != nil {
		t.Fatalf("Save parent: %v", err)
	}
	stale := agent.NewSession("sys")
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	stale.Add(provider.Message{Role: provider.RoleAssistant, Content: "one"})
	stale.Add(provider.Message{Role: provider.RoleUser, Content: "local " + name})
	info, err := stale.SaveRecoveryBranch(agent.RecoveryBranchOptions{OriginalPath: parentPath})
	if err != nil {
		t.Fatalf("SaveRecoveryBranch: %v", err)
	}
	covering, err := agent.LoadSession(parentPath)
	if err != nil {
		t.Fatalf("Load covering parent: %v", err)
	}
	covering.Replace(append([]provider.Message(nil), stale.Snapshot()...))
	covering.Add(provider.Message{Role: provider.RoleAssistant, Content: "answered after recovery"})
	if err := covering.SaveRewrite(parentPath); err != nil {
		t.Fatalf("Save covering parent: %v", err)
	}
	return parentPath, info.Path
}
