package agent

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSubagentStoreCleanupStaleRunningParentGuard(t *testing.T) {
	live, dead := true, false
	tests := []struct {
		name        string
		guardBusy   *bool
		wantCleaned int
		wantStatus  SubagentStatus
	}{
		{name: "live parent", guardBusy: &live, wantStatus: SubagentRunning},
		{name: "dead parent", guardBusy: &dead, wantCleaned: 1, wantStatus: SubagentInterrupted},
		{name: "no guard", wantStatus: SubagentRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionDir := t.TempDir()
			store := newTestSubagentStore(t, filepath.Join(sessionDir, "subagents"))
			spec := testSubagentSpec(t, "review")
			spec.ParentSession = "probe-parent"
			run, err := store.PrepareFresh(spec)
			if err != nil {
				t.Fatalf("PrepareFresh: %v", err)
			}
			if err := store.MarkRunning(run); err != nil {
				t.Fatalf("MarkRunning: %v", err)
			}
			ref := run.Ref
			run.Release()

			probeCalls := 0
			store.WithParentIdleGuard(nil)
			if tt.guardBusy != nil {
				store.WithParentIdleGuard(func(_ context.Context, id string, work func() error) (bool, error) {
					probeCalls++
					if id != spec.ParentSession {
						t.Fatalf("parent identity = %q, want %q", id, spec.ParentSession)
					}
					if *tt.guardBusy {
						return false, nil
					}
					return true, work()
				})
			}
			cleaned, err := store.CleanupStaleRunning()
			if err != nil {
				t.Fatalf("CleanupStaleRunning: %v", err)
			}
			if cleaned != tt.wantCleaned {
				t.Fatalf("cleaned = %d, want %d", cleaned, tt.wantCleaned)
			}
			if tt.guardBusy != nil && probeCalls != 1 {
				t.Fatalf("probe calls = %d, want 1", probeCalls)
			}
			meta, err := store.LoadMeta(ref)
			if err != nil {
				t.Fatalf("LoadMeta: %v", err)
			}
			if meta.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", meta.Status, tt.wantStatus)
			}
		})
	}
}
