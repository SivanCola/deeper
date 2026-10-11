package repair

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppBundleRollbackLocksBackupPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "Deepzora.app")
	exe := filepath.Join(app, "Contents", "MacOS", "Deepzora")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	originalExecutable := repairExecutable
	repairExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { repairExecutable = originalExecutable })

	backup := app + ".deepzora-update-backup"
	if _, err := PrepareAppBundleUpdate("v1", "v2", app, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	holder, err := LockRepairMutations(backup)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := RollbackPendingUpdate()
		done <- err
	}()
	select {
	case err := <-done:
		holder()
		t.Fatalf("rollback completed without the backup lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	holder()
	if err := <-done; err != nil {
		t.Fatalf("rollback after backup unlock: %v", err)
	}
}
