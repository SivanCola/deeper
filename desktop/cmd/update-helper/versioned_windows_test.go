//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/installlayout"
	"deepzora/internal/repair"
)

func writeVersionedWindowsStaging(t *testing.T, dir, prefix, version string, names ...string) {
	t.Helper()
	if len(names) == 0 {
		names = []string{"deepzora-desktop.exe", "deepzora-cli.exe", "deepzora-update-helper.exe", "deepzora-launcher.exe"}
	}
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(prefix+name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeWindowsPayloadManifestForTest(t, dir, version)
}

func versionedWindowsTransaction(installDir, version, createdAt string) *repair.UpdateTransaction {
	return &repair.UpdateTransaction{
		SchemaVersion: 1,
		ToVersion:     version,
		TargetKind:    "file",
		TargetPath:    filepath.Join(installDir, "deepzora-desktop.exe"),
		CreatedAt:     createdAt,
	}
}

func TestPreferVersionedWindowsActivation(t *testing.T) {
	staging := t.TempDir()
	if preferVersionedWindowsActivation(staging) {
		t.Fatal("empty staging must not prefer versioned")
	}
	for _, name := range []string{
		"deepzora-desktop.exe",
		"deepzora-cli.exe",
		"deepzora-update-helper.exe",
		"deepzora-launcher.exe",
	} {
		if err := os.WriteFile(filepath.Join(staging, name), []byte(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if !preferVersionedWindowsActivation(staging) {
		t.Fatal("complete staging must prefer versioned")
	}
}

func TestActivateVersionedWindowsFromStaging(t *testing.T) {
	acceptWindowsPayloadManifestForTest(t)
	installDir := t.TempDir()
	staging := t.TempDir()
	writeVersionedWindowsStaging(t, staging, "payload:", "v1.20.0",
		"deepzora-desktop.exe",
		"deepzora-cli.exe",
		"deepzora-update-helper.exe",
		"deepzora-launcher.exe",
		"deepzora-layout-installer.exe",
	)

	claimed := versionedWindowsTransaction(installDir, "v1.20.0", "2026-01-01T00:00:00Z")
	if err := activateVersionedWindowsFromStaging(claimed, staging); err != nil {
		t.Fatal(err)
	}
	if !installlayout.HasCurrent(installDir) {
		t.Fatal("current.json missing")
	}
	ptr, err := installlayout.ReadCurrent(installDir)
	if err != nil || ptr.ActiveVersion != "v1.20.0" {
		t.Fatalf("pointer=%+v err=%v", ptr, err)
	}
	desktop, err := installlayout.ActiveDesktopPath(installDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(desktop)
	if err != nil || string(raw) != "payload:deepzora-desktop.exe" {
		t.Fatalf("desktop payload = %q err=%v", raw, err)
	}
	for _, name := range []string{"Deepzora.exe", "deepzora-cli.exe"} {
		if _, err := os.Stat(filepath.Join(installDir, name)); err != nil {
			t.Fatalf("root entry %s: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(installDir, "deepzora-launcher.exe")); !os.IsNotExist(err) {
		t.Fatalf("new versioned root created a legacy entry: %v", err)
	}
	if prefer := preferRelaunchPath("", installDir); filepath.Base(prefer) != "Deepzora.exe" {
		t.Fatalf("prefer relaunch = %s", prefer)
	}
}

func TestActivateVersionedWindowsFromStagingPublishesShellTree(t *testing.T) {
	acceptWindowsPayloadManifestForTest(t)
	installDir := t.TempDir()
	staging := t.TempDir()
	tree := []string{"app/Deepzora.exe", "app/resources/app.asar", "app/locales/en-US.pak"}
	writeVersionedWindowsStaging(t, staging, "payload:", "v1.30.0", append([]string{
		"deepzora-desktop.exe",
		"deepzora-cli.exe",
		"deepzora-update-helper.exe",
		"deepzora-launcher.exe",
	}, tree...)...)
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.30.0", "2026-01-01T00:00:00Z"), staging); err != nil {
		t.Fatal(err)
	}
	versionDir := filepath.Join(installDir, installlayout.VersionsDirName, "v1.30.0")
	for _, name := range tree {
		raw, err := os.ReadFile(filepath.Join(versionDir, filepath.FromSlash(name)))
		if err != nil || string(raw) != "payload:"+name {
			t.Fatalf("tree member %s = %q err=%v", name, raw, err)
		}
	}
	if _, err := os.Stat(filepath.Join(installDir, "app")); !os.IsNotExist(err) {
		t.Fatalf("shell tree leaked into the install root: %v", err)
	}
}

func TestActivateVersionedWindowsUsesVerifiedThinCLIEntry(t *testing.T) {
	acceptWindowsPayloadManifestForTest(t)
	installDir := t.TempDir()
	staging := t.TempDir()
	const entry = "app/resources/bin/deepzora-cli-launcher.exe"
	writeVersionedWindowsStaging(t, staging, "payload:", "v1.39.0",
		"deepzora-desktop.exe", "deepzora-cli.exe", "deepzora-update-helper.exe",
		"deepzora-launcher.exe", "app/Deepzora.exe", entry)
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.39.0", "2026-01-01T00:00:00Z"), staging); err != nil {
		t.Fatal(err)
	}
	rootCLI, err := os.ReadFile(filepath.Join(installDir, "deepzora-cli.exe"))
	if err != nil || string(rootCLI) != "payload:"+entry {
		t.Fatalf("root CLI=%q err=%v, want verified thin entry", rootCLI, err)
	}
	fullCLI, err := os.ReadFile(filepath.Join(installDir, "versions", "v1.39.0", "deepzora-cli.exe"))
	if err != nil || string(fullCLI) != "payload:deepzora-cli.exe" {
		t.Fatalf("version CLI=%q err=%v, want full CLI", fullCLI, err)
	}
}

func TestActivateVersionedWindowsFromStagingRejectsManifestDrift(t *testing.T) {
	acceptWindowsPayloadManifestForTest(t)
	installDir := t.TempDir()
	staging := t.TempDir()
	writeVersionedWindowsStaging(t, staging, "payload:", "v1.30.0",
		"deepzora-desktop.exe",
		"deepzora-cli.exe",
		"deepzora-update-helper.exe",
		"deepzora-launcher.exe",
		"app/Deepzora.exe",
	)
	if err := os.WriteFile(filepath.Join(staging, "app", "Deepzora.exe"), []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.30.0", "2026-01-01T00:00:00Z"), staging); err == nil {
		t.Fatal("staged tree member that differs from the signed manifest was activated")
	}
	if installlayout.HasCurrent(installDir) {
		t.Fatal("current.json was written after a rejected payload")
	}
	if err := os.Remove(filepath.Join(staging, "deepzora-payload.json")); err != nil {
		t.Fatal(err)
	}
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.30.0", "2026-01-01T00:00:00Z"), staging); err == nil {
		t.Fatal("staged payload without a signed manifest was activated")
	}
}

func TestPreferRelaunchPathIgnoresStaleVersionedDesktop(t *testing.T) {
	acceptWindowsPayloadManifestForTest(t)
	installDir := t.TempDir()
	oldSrc := t.TempDir()
	newSrc := t.TempDir()
	writeVersionedWindowsStaging(t, oldSrc, "old-", "v1.24.0")
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.24.0", "2026-01-01T00:00:00Z"), oldSrc); err != nil {
		t.Fatal(err)
	}
	oldDesktop, err := installlayout.ActiveDesktopPath(installDir)
	if err != nil {
		t.Fatal(err)
	}
	writeVersionedWindowsStaging(t, newSrc, "new-", "v1.24.1")
	if err := activateVersionedWindowsFromStaging(versionedWindowsTransaction(installDir, "v1.24.1", "2026-01-01T00:00:01Z"), newSrc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldDesktop); err != nil {
		t.Fatalf("previous desktop should remain: %v", err)
	}
	got := preferRelaunchPath(oldDesktop, installDir)
	if filepath.Base(got) != "Deepzora.exe" {
		t.Fatalf("preferRelaunchPath(%s) = %s, want install-root launcher", oldDesktop, got)
	}
}
