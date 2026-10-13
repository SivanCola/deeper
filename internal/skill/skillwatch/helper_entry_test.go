package skillwatch

import (
	"deepzora/internal/testutil"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The helper backend re-executes os.Executable(). Windows always takes that
// path, so a host binary that does not serve the helper entry degrades every
// root to scan fallback while repeatedly spawning and killing children. Force
// the same default command here so the missing entry fails on every platform
// instead of only on Windows CI.
func TestDefaultHelperCommandServesTheRealBinaryEntry(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(Options{Stderr: io.Discard, ForceHelper: true})
	defer svc.Close()

	svc.Subscribe(dir, 2, countingScope, flatHash, func(string) {})
	testutil.WaitFor(t, "helper registration through the re-executed binary", 5*time.Second, func() bool {
		return svc.Diagnostics().PhysicalWatches == 1
	})

	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, "helper event delivery", 5*time.Second, func() bool {
		return svc.Diagnostics().EventsReceived >= 1
	})
}
