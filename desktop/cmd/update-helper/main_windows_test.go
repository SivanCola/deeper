//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/installlayout"
)

const testInstallerSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestStageVerifiedInstallerFreezesExpectedBytes(t *testing.T) {
	source := filepath.Join(t.TempDir(), "installer.exe")
	content := []byte("verified-installer")
	if err := os.WriteFile(source, content, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])
	staged, cleanup, err := stageVerifiedInstaller(source, expected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if staged == source {
		t.Fatal("installer was not isolated from the mutable cache path")
	}
	if err := os.WriteFile(source, []byte("changed-after-handoff"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstallerSHA256(staged, expected); err != nil {
		t.Fatalf("isolated installer changed with source: %v", err)
	}
	if got, err := os.ReadFile(staged); err != nil || string(got) != string(content) {
		t.Fatalf("staged installer = %q, %v", got, err)
	}
	if err := os.WriteFile(staged, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstallerSHA256(staged, expected); err == nil {
		t.Fatal("tampered staged installer passed final verification")
	}
}

func TestStageVerifiedInstallerRejectsSourceHashDrift(t *testing.T) {
	source := filepath.Join(t.TempDir(), "installer.exe")
	if err := os.WriteFile(source, []byte("different"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := stageVerifiedInstaller(source, testInstallerSHA256); err == nil {
		if cleanup != nil {
			_ = cleanup()
		}
		t.Fatal("installer bytes different from desktop verification were accepted")
	}
}

func TestRunRequiresTargetVersionBeforeStartingInstaller(t *testing.T) {
	stubDesktopHandoff(t)
	if code := run([]string{
		"--installer", `C:\Temp\Deepzora-installer.exe`,
		"--installer-sha256", testInstallerSHA256,
	}); code != 2 {
		t.Fatalf("run without --to-version = %d, want 2", code)
	}
}

func TestRunVersionedLayoutDoesNotReadOrClaimLegacyPending(t *testing.T) {
	stubDesktopHandoff(t)
	installDir := t.TempDir()
	seed := t.TempDir()
	for _, name := range []string{"deepzora-desktop.exe", "deepzora-cli.exe", "deepzora-update-helper.exe"} {
		if err := os.WriteFile(filepath.Join(seed, name), []byte("old-"+name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := installlayout.ActivateVersion(installlayout.ActivationRequest{
		InstallRoot: installDir,
		Version:     "v1.20.0",
		RequestID:   "seed-windows-helper",
		Members: []installlayout.Member{
			{Name: "deepzora-desktop.exe", Path: filepath.Join(seed, "deepzora-desktop.exe")},
			{Name: "deepzora-cli.exe", Path: filepath.Join(seed, "deepzora-cli.exe")},
			{Name: "deepzora-update-helper.exe", Path: filepath.Join(seed, "deepzora-update-helper.exe")},
		},
		RequiredNames: []string{"deepzora-desktop.exe", "deepzora-cli.exe", "deepzora-update-helper.exe"},
	}); err != nil {
		t.Fatal(err)
	}

	originalInstaller := runInstallerFn
	originalRelaunch := startRelaunchFn
	originalStageInstaller := stageInstallerFn
	originalClaimInstallerExecution := claimInstallerExecutionFn
	originalReconcileUninstall := reconcileWindowsUninstallRegistrationFn
	originalVerify := verifyDesktopHandoffFn
	t.Cleanup(func() {
		runInstallerFn = originalInstaller
		startRelaunchFn = originalRelaunch
		stageInstallerFn = originalStageInstaller
		claimInstallerExecutionFn = originalClaimInstallerExecution
		reconcileWindowsUninstallRegistrationFn = originalReconcileUninstall
		verifyDesktopHandoffFn = originalVerify
	})
	verifyDesktopHandoffFn = func(string, bool) error { return nil }
	stageInstallerFn = func(path, _ string) (string, func() error, error) {
		return path, func() error { return nil }, nil
	}
	claimInstallerExecutionFn = func(string, string) (func(), error) { return func() {}, nil }
	reconciledVersion := ""
	reconcileWindowsUninstallRegistrationFn = func(root, targetVersion string) (bool, error) {
		if root != installDir {
			t.Fatalf("reconcile root = %q, want %q", root, installDir)
		}
		reconciledVersion = targetVersion
		return true, nil
	}
	acceptWindowsPayloadManifestForTest(t)
	runInstallerFn = func(_ string, staging string) error {
		writeVersionedWindowsStaging(t, staging, "new-", "v1.20.1")
		return nil
	}
	relaunched := false
	startRelaunchFn = func(string, string) error { relaunched = true; return nil }

	if code := run([]string{
		"--installer", filepath.Join(installDir, "installer.exe"),
		"--installer-sha256", testInstallerSHA256,
		"--install-dir", installDir,
		"--relaunch", filepath.Join(installDir, "deepzora-launcher.exe"),
		"--to-version", "v1.20.1",
		"--install-layout", "versioned-v1",
	}); code != 0 {
		t.Fatalf("run exit code = %d", code)
	}
	if !relaunched {
		t.Fatal("versioned update did not relaunch")
	}
	if reconciledVersion != "v1.20.1" {
		t.Fatalf("reconciled version = %q, want v1.20.1", reconciledVersion)
	}
	ptr, err := installlayout.ReadCurrent(installDir)
	if err != nil || ptr.ActiveVersion != "v1.20.1" {
		t.Fatalf("pointer=%+v err=%v", ptr, err)
	}
}

func TestRunVersionedUpdateRelaunchesLauncherNotOldDesktop(t *testing.T) {
	stubDesktopHandoff(t)
	installDir := t.TempDir()
	seed := t.TempDir()
	acceptWindowsPayloadManifestForTest(t)
	writeVersionedWindowsStaging(t, seed, "old-", "v1.24.0")
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.24.0", "2026-01-01T00:00:00Z"), seed); err != nil {
		t.Fatal(err)
	}
	oldDesktop, err := installlayout.ActiveDesktopPath(installDir)
	if err != nil {
		t.Fatal(err)
	}

	originalInstaller := runInstallerFn
	originalRelaunch := startRelaunchFn
	originalStageInstaller := stageInstallerFn
	originalClaimInstallerExecution := claimInstallerExecutionFn
	originalReconcileUninstall := reconcileWindowsUninstallRegistrationFn
	originalVerify := verifyDesktopHandoffFn
	t.Cleanup(func() {
		runInstallerFn = originalInstaller
		startRelaunchFn = originalRelaunch
		stageInstallerFn = originalStageInstaller
		claimInstallerExecutionFn = originalClaimInstallerExecution
		reconcileWindowsUninstallRegistrationFn = originalReconcileUninstall
		verifyDesktopHandoffFn = originalVerify
	})
	stageInstallerFn = func(path, _ string) (string, func() error, error) {
		return path, func() error { return nil }, nil
	}
	claimInstallerExecutionFn = func(string, string) (func(), error) { return func() {}, nil }
	reconcileWindowsUninstallRegistrationFn = func(string, string) (bool, error) { return true, nil }
	runInstallerFn = func(_ string, staging string) error {
		writeVersionedWindowsStaging(t, staging, "new-", "v1.24.1")
		return nil
	}
	verified := false
	verifyDesktopHandoffFn = func(root string, _ bool) error {
		if root != installDir {
			t.Fatalf("verify root = %q", root)
		}
		verified = true
		return nil
	}
	var relaunchPath string
	startRelaunchFn = func(path, dir string) error {
		relaunchPath = path
		if dir != installDir {
			t.Fatalf("relaunch dir = %q", dir)
		}
		return nil
	}

	if code := run([]string{
		"--installer", filepath.Join(installDir, "installer.exe"),
		"--installer-sha256", testInstallerSHA256,
		"--install-dir", installDir,
		"--relaunch", oldDesktop,
		"--to-version", "v1.24.1",
		"--install-layout", "versioned-v1",
	}); code != 0 {
		t.Fatalf("run exit code = %d", code)
	}
	if !verified {
		t.Fatal("versioned update did not verify its desktop owner")
	}
	if filepath.Base(relaunchPath) != "Deepzora.exe" {
		t.Fatalf("relaunch path = %s, want install-root launcher (not %s)", relaunchPath, oldDesktop)
	}
	if relaunchPath == oldDesktop {
		t.Fatal("relaunch used the retained previous desktop")
	}
}

func TestRelaunchPublishedInstallBlockedOwnerPreventsRelaunch(t *testing.T) {
	stubDesktopHandoff(t)
	originalVerify := verifyDesktopHandoffFn
	originalRelaunch := startRelaunchFn
	t.Cleanup(func() {
		verifyDesktopHandoffFn = originalVerify
		startRelaunchFn = originalRelaunch
	})
	verifyDesktopHandoffFn = func(string, bool) error {
		return errors.New("old instance remains")
	}
	relaunched := false
	startRelaunchFn = func(string, string) error {
		relaunched = true
		return nil
	}
	if code := relaunchPublishedInstall(
		log.New(io.Discard, "", 0),
		filepath.Join(t.TempDir(), "deepzora-desktop.exe"),
		t.TempDir(),
		"relaunch",
	); code != 1 {
		t.Fatalf("relaunchPublishedInstall = %d, want 1", code)
	}
	if relaunched {
		t.Fatal("relaunch started before the superseded desktop exit was confirmed")
	}
}

func TestClaimVerifiedInstallerForExecutionFreezesPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installer.exe")
	content := []byte("verified-installer")
	if err := os.WriteFile(path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	release, err := claimVerifiedInstallerForExecution(path, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o700); err == nil {
		release()
		t.Fatal("installer remained writable while execution claim was held")
	}
	if err := os.Rename(path, path+".replaced"); err == nil {
		release()
		t.Fatal("installer remained renameable while execution claim was held")
	}
	release()
	release()
	if err := os.WriteFile(path, []byte("replacement"), 0o700); err != nil {
		t.Fatalf("installer remained frozen after claim release: %v", err)
	}
}

func TestClaimVerifiedInstallerForExecutionRejectsHashDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installer.exe")
	if err := os.WriteFile(path, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if release, err := claimVerifiedInstallerForExecution(path, testInstallerSHA256); err == nil {
		release()
		t.Fatal("installer with the wrong SHA-256 received an execution claim")
	}
}

func TestRunRejectsHistoricalUpdateArguments(t *testing.T) {
	for _, extra := range [][]string{{"--created-at", "2026-01-01"}, {"--transaction-id", "old"}, {"--install-layout", "flat"}, {}} {
		args := append([]string{"--installer", "installer.exe", "--installer-sha256", testInstallerSHA256, "--install-dir", t.TempDir(), "--to-version", "v1.0.0"}, extra...)
		if code := run(args); code != 2 {
			t.Fatalf("historical arguments %v returned %d", extra, code)
		}
	}
}
