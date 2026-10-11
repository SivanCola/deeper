package control

import (
	"testing"

	"deepzora/internal/config"
)

// approveWorkspace approves what dir's configuration names, standing in for a
// person who ran `deepzora trust` there. Tests of the gate itself never call it.
func approveWorkspace(t *testing.T, dir string) {
	t.Helper()
	_, _ = config.ApproveWorkspacePrograms(dir)
}
