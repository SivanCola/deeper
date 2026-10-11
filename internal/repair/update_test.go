package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateVersionsEqualNormalizesLeadingV(t *testing.T) {
	if !UpdateVersionsEqual("1.19.7", "v1.19.7") {
		t.Fatal("expected 1.19.7 and v1.19.7 to match")
	}
	if !UpdateVersionsEqual("v1.20.0", "V1.20.0") {
		t.Fatal("expected case-insensitive v prefix normalization")
	}
	if UpdateVersionsEqual("v1.19.7", "v1.19.8") {
		t.Fatal("different versions must not match")
	}
	if UpdateVersionsEqual("", "v1") {
		t.Fatal("empty version must not match")
	}
}

func TestAppBundleRollbackRestoresWhenLiveBundleIsMissing(t *testing.T) {
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
	result, err := RollbackPendingUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if !result.RolledBack || result.ToVersion != "v1" {
		t.Fatalf("rollback result = %+v", result)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "old" {
		t.Fatalf("restored bundle executable = %q (%v)", got, err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup bundle survived successful rollback: %v", err)
	}
	if HasPendingUpdate() {
		t.Fatal("pending update survived successful rollback")
	}
}

func TestAppBundleRollbackRecognizesAlreadyRestoredBundle(t *testing.T) {
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
	// This is the durable state after backup -> live succeeded but the process
	// exited before pending-update.json was removed. It is also the safe
	// pre-handoff cancellation state.
	result, err := RollbackPendingUpdate()
	if err != nil || !result.RolledBack {
		t.Fatalf("already-restored rollback = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "old" {
		t.Fatalf("recognized live bundle = %q, %v", got, err)
	}
	if HasPendingUpdate() {
		t.Fatal("already-restored app transaction was not cleared")
	}
}

func TestMarkUpdateHealthyRejectsAppBundleDrift(t *testing.T) {
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
	staging, err := os.MkdirTemp("", "deepzora-mac-update-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(staging) })
	stagedApp := filepath.Join(staging, "Deepzora.app")
	stagedExe := filepath.Join(stagedApp, "Contents", "MacOS", "Deepzora")
	if err := os.MkdirAll(filepath.Dir(stagedExe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedExe, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	tx, err := PrepareAppBundleUpdateHandoff(
		"v1", "v2", app, backup, stagedApp, staging, os.Getpid(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("drifted-after-launch"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := MarkUpdateHealthyMatching(tx.ToVersion, tx.CreatedAt); err == nil ||
		!strings.Contains(err.Error(), "installed bundle differs") {
		t.Fatalf("healthy commit error = %v, want installed-bundle drift rejection", err)
	}
	if _, err := ReadPendingUpdate(); err != nil {
		t.Fatalf("rejected healthy commit removed pending transaction: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("rejected healthy commit removed rollback backup: %v", err)
	}
}

func TestMarkUpdateHealthyRejectsLegacyAppBundleWithoutInstalledDigest(t *testing.T) {
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
	tx, err := PrepareAppBundleUpdate("v1", "v2", app, backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := MarkUpdateHealthyMatching(tx.ToVersion, tx.CreatedAt); err == nil ||
		!strings.Contains(err.Error(), "installed bundle state is missing") {
		t.Fatalf("legacy healthy commit = %v, want fail-closed rejection", err)
	}
	if !HasPendingUpdate() {
		t.Fatal("legacy healthy rejection removed pending transaction")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("legacy healthy rejection removed rollback backup: %v", err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "new" {
		t.Fatalf("legacy healthy rejection changed live bundle: %q, %v", got, err)
	}
}

func TestAppBundleRollbackRejectsChangedBackupTree(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(backup, "Contents", "MacOS", "Deepzora"), []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RollbackPendingUpdate(); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("rollback error = %v, want backup digest mismatch", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("changed backup was removed: %v", err)
	}
	if HasPendingUpdate() == false {
		t.Fatal("pending update was lost after rejected rollback")
	}
}

func TestAppBundleRollbackCompensatesBackupChangedDuringPublish(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}

	originalSwap := rollbackSwapRename
	rollbackSwapRename = func(oldPath, newPath string) error {
		if err := renameRepairNodeNoReplace(oldPath, newPath); err != nil {
			return err
		}
		if oldPath == backup && newPath == app {
			return os.WriteFile(filepath.Join(app, "Contents", "MacOS", "Deepzora"), []byte("tampered-during-publish"), 0o700)
		}
		return nil
	}
	t.Cleanup(func() { rollbackSwapRename = originalSwap })

	result, err := RollbackPendingUpdate()
	if err == nil || !strings.Contains(err.Error(), "restored bundle digest mismatch") {
		t.Fatalf("rollback error = %v, want post-publish digest rejection", err)
	}
	if result.MixedInstall {
		t.Fatalf("rollback reported mixed install after successful compensation: %+v", result)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "new" {
		t.Fatalf("prior live bundle was not restored: %q, %v", got, err)
	}
	rejected, err := filepath.Glob(app + ".deepzora-cleanup-*")
	if err != nil || len(rejected) != 1 {
		t.Fatalf("rejected backup bundle = %v, %v", rejected, err)
	}
	rejectedExe := filepath.Join(rejected[0], "Contents", "MacOS", "Deepzora")
	if got, err := os.ReadFile(rejectedExe); err != nil || string(got) != "tampered-during-publish" {
		t.Fatalf("rejected bundle was not preserved: %q, %v", got, err)
	}
	if !HasPendingUpdate() {
		t.Fatal("failed rollback removed pending recovery state")
	}
}

func TestLegacyAppBundleRollbackWithoutTreeDigest(t *testing.T) {
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
	tx, err := PrepareAppBundleUpdate("v1", "v2", app, backup)
	if err != nil {
		t.Fatal(err)
	}
	tx.BackupTreeID = ""
	if err := overwritePendingUpdateForTest(tx); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	result, err := RollbackPendingUpdate()
	if err == nil || result.RolledBack ||
		!strings.Contains(err.Error(), "backup bundle digest is missing or invalid") {
		t.Fatalf("legacy rollback result = %+v, %v", result, err)
	}
	if _, err := os.Lstat(app); !os.IsNotExist(err) {
		t.Fatalf("legacy rollback recreated target without authorization: %v", err)
	}
	backupExe := filepath.Join(backup, "Contents", "MacOS", "Deepzora")
	if got, err := os.ReadFile(backupExe); err != nil || string(got) != "old" {
		t.Fatalf("legacy rollback changed backup bundle = %q, %v", got, err)
	}
	if _, err := os.Stat(PendingUpdatePath()); err != nil {
		t.Fatal("legacy rollback removed pending recovery state")
	}
}

func TestRepairPlanRejectsAppBundleSymlinkBackupBeforeMutation(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
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
	tx, err := PrepareAppBundleUpdate("v1", "v2", app, backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := overwritePendingUpdateForTest(tx); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(dir, "external-backup")
	if err := os.Rename(app, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, backup); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	plan := RepairPlan{
		SchemaVersion: RepairPlanSchemaVersion,
		Summary:       "restore legacy app backup",
		Actions: []RepairPlanAction{{
			Type:   "rollback_update",
			Reason: "explicitly confirmed recovery",
		}},
	}
	preview, err := PreviewRepairPlan(plan, ApplyPlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyRepairPlan(plan, ApplyPlanOptions{
		ExpectedPreviewID: RepairPlanPreviewID(plan, preview),
	})
	if err == nil || !strings.Contains(err.Error(), "backup bundle is not a directory") {
		t.Fatalf("legacy symlink backup rollback = %v", err)
	}
	if got, err := os.Readlink(backup); err != nil || got != external {
		t.Fatalf("backup symlink changed before rejection: %q, %v", got, err)
	}
	if _, err := os.Lstat(app); !os.IsNotExist(err) {
		t.Fatalf("live app path changed before rejection: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(external, "Contents", "MacOS", "Deepzora")); err != nil || string(got) != "old" {
		t.Fatalf("external backup changed: %q, %v", got, err)
	}
	if !HasPendingUpdate() {
		t.Fatal("rejected legacy symlink backup removed pending state")
	}
}

// A stale marker with nothing to roll back must be cleared, not retried
// forever.
func TestRecoverFailedInstallClearsStaleMarker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	if err := MarkUpdateApplyFailed("v2", "installer exited with 1"); err != nil {
		t.Fatal(err)
	}
	result, failure, err := RecoverFailedInstall()
	if err != nil || failure == nil || result.RolledBack {
		t.Fatalf("recover = %+v %+v %v", result, failure, err)
	}
	if _, ok := ReadUpdateApplyFailure(); ok {
		t.Fatal("stale marker was not cleared")
	}
}

func TestRecoverFailedInstallPreservesMarkerRecreatedDuringCleanup(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	if err := MarkUpdateApplyFailed("v2", "old failure"); err != nil {
		t.Fatal(err)
	}
	originalHook := updateCleanupAfterRename
	injected := false
	updateCleanupAfterRename = func(oldpath, _ string) {
		if !injected && oldpath == updateApplyFailurePath() {
			injected = true
			if err := markUpdateApplyFailed("v3", "", "", "new failure"); err != nil {
				t.Errorf("recreate failure marker: %v", err)
			}
		}
	}
	t.Cleanup(func() { updateCleanupAfterRename = originalHook })

	if _, failure, err := RecoverFailedInstall(); err != nil || failure == nil {
		t.Fatalf("recover = %+v, %v", failure, err)
	}
	current, ok := ReadUpdateApplyFailure()
	if !ok || current.ToVersion != "v3" || current.Reason != "new failure" {
		t.Fatalf("concurrent failure marker = %+v, present=%v", current, ok)
	}
}

func TestHealthyAppUpdateRejectsBackupDrift(t *testing.T) {
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
	tx, err := PrepareAppBundleUpdate("v1", "v2", app, backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "Contents", "MacOS", "Deepzora"), []byte("unrelated"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := MarkUpdateHealthyMatching(tx.ToVersion, tx.CreatedAt); err == nil {
		t.Fatal("healthy commit accepted a changed app backup")
	}
	if _, err := ReadPendingUpdate(); err != nil {
		t.Fatalf("rejected healthy commit removed pending transaction: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(backup, "Contents", "MacOS", "Deepzora")); err != nil || string(got) != "unrelated" {
		t.Fatalf("changed app backup was removed: %q, %v", got, err)
	}
}

func TestRollbackPendingAppBundleStateRejectsDriftInsideRetainRename(t *testing.T) {
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
	tx, err := PrepareAppBundleUpdate("v1", "v2", app, backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("confirmed-new"), 0o700); err != nil {
		t.Fatal(err)
	}
	expectedState, expectedFiles := pendingUpdateBoundPreview(tx)

	originalRename := rollbackSwapRename
	injected := false
	rollbackSwapRename = func(oldpath, newpath string) error {
		if !injected && oldpath == app && strings.Contains(newpath, ".deepzora-failed-") {
			injected = true
			if err := os.WriteFile(exe, []byte("concurrent-new"), 0o700); err != nil {
				return err
			}
		}
		return os.Rename(oldpath, newpath)
	}
	t.Cleanup(func() { rollbackSwapRename = originalRename })

	result, err := rollbackPendingUpdateState(expectedState, expectedFiles)
	if err == nil || result.RolledBack || !strings.Contains(err.Error(), "preview changed since confirmation") {
		t.Fatalf("bundle rollback after retain-window drift = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "concurrent-new" {
		t.Fatalf("bundle rollback lost concurrent live tree: %q, %v", got, err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("rejected bundle rollback lost backup: %v", err)
	}
	if !HasPendingUpdate() {
		t.Fatal("rejected bundle retain-window drift removed pending transaction")
	}
}

func TestRetainUpdateRollbackNodeRetriesNameCollision(t *testing.T) {
	target := filepath.Join(t.TempDir(), "Deepzora.app")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	originalRename := rollbackSwapRename
	calls := 0
	rollbackSwapRename = func(oldPath, newPath string) error {
		calls++
		if calls == 1 {
			return os.ErrExist
		}
		return os.Rename(oldPath, newPath)
	}
	t.Cleanup(func() { rollbackSwapRename = originalRename })

	retained, err := retainUpdateRollbackNode(target, "deepzora-failed")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(retained, ".deepzora-failed-") {
		t.Fatalf("retain calls=%d path=%q", calls, retained)
	}
	if _, err := os.Stat(retained); err != nil {
		t.Fatalf("retained app bundle: %v", err)
	}
}

func TestAppBundleRollbackPreservesLiveBundleWithoutReplacementOwnership(t *testing.T) {
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
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("unbound-live"), 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := RollbackPendingUpdate()
	if err != nil || !result.RolledBack {
		t.Fatalf("rollback = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "old" {
		t.Fatalf("restored bundle = %q, %v", got, err)
	}
	failed, err := filepath.Glob(app + ".deepzora-failed-*")
	if err != nil || len(failed) != 1 {
		t.Fatalf("retained unbound bundle = %v, %v", failed, err)
	}
	if got, err := os.ReadFile(filepath.Join(failed[0], "Contents", "MacOS", "Deepzora")); err != nil || string(got) != "unbound-live" {
		t.Fatalf("unbound bundle was not preserved: %q, %v", got, err)
	}
}

func TestAppBundleRollbackCleansReplacementBoundToStagingTree(t *testing.T) {
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
	staging, err := os.MkdirTemp("", "deepzora-mac-update-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(staging) })
	stagedApp := filepath.Join(staging, "Deepzora.app")
	stagedExe := filepath.Join(stagedApp, "Contents", "MacOS", "Deepzora")
	if err := os.MkdirAll(filepath.Dir(stagedExe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedExe, []byte("installed"), 0o700); err != nil {
		t.Fatal(err)
	}
	backup := app + ".deepzora-update-backup"
	if _, err := PrepareAppBundleUpdateHandoff("v1", "v2", app, backup, stagedApp, staging, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(app, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("installed"), 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := RollbackPendingUpdate()
	if err != nil || !result.RolledBack {
		t.Fatalf("rollback = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "old" {
		t.Fatalf("restored bundle = %q, %v", got, err)
	}
	failed, err := filepath.Glob(app + ".deepzora-failed-*")
	if err != nil || len(failed) != 0 {
		t.Fatalf("transaction-owned replacement survived cleanup: %v, %v", failed, err)
	}
}
