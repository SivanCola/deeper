//go:build windows

package workspacelease

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/filelock"
)

func TestJunctionWorkspaceSharesCurrentTargetLock(t *testing.T) {
	root := t.TempDir()
	target, alias := filepath.Join(root, "target"), filepath.Join(root, "alias")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	lockDir := filepath.Join(root, "locks")
	owner, err := New(alias, lockDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot, err := CanonicalWorkspace(target)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := workspaceLockPath(lockDir, targetRoot)
	if owner.lockPath != lockPath {
		t.Fatalf("junction and target locks differ: %q != %q", owner.lockPath, lockPath)
	}
	for _, group := range []bool{false, true} {
		releaseTarget, err := filelock.TryAcquire(lockPath)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		var release func()
		if group {
			release, err = HoldWriteRoots(ctx, lockDir, alias)
		} else {
			release, err = owner.HoldWrite(ctx)
		}
		cancel()
		releaseTarget()
		if release != nil {
			release()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("group=%v ignored target writer: %v", group, err)
		}
	}
	release, err := owner.HoldWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	targetRelease, err := filelock.TryAcquire(lockPath)
	if targetRelease != nil {
		targetRelease()
	}
	if !errors.Is(err, filelock.ErrHeld) {
		t.Fatalf("target writer ignored junction holder: %v", err)
	}
}
