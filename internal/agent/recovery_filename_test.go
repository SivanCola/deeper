package agent

import "testing"

func TestRecoveryFilenameParentAndRoot(t *testing.T) {
	t.Parallel()
	root, ok := RecoveryFilenameRootID("/tmp/session-recovery-aabbccddeeff0011.jsonl")
	if !ok || root != "session" {
		t.Fatalf("root = %q ok=%v", root, ok)
	}
}
