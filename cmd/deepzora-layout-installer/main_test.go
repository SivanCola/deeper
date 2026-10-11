package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"deepzora/internal/installlayout"
	"deepzora/internal/testenv"
)

func TestMain(m *testing.M) { testenv.RunWithIsolatedUserState(m) }

func installerVersionNames() []string {
	names := []string{installlayout.DesktopBinaryName(), installlayout.CLIBinaryName()}
	if runtime.GOOS == "windows" {
		names = append(names, installlayout.UpdateHelperBinaryName())
	}
	return names
}

func writeInstallerStaging(t *testing.T, root, label string, includeLauncher bool) string {
	t.Helper()
	staging := filepath.Join(root, "versions", ".installer-"+label)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range installerVersionNames() {
		if err := os.WriteFile(filepath.Join(staging, name), []byte(label+"-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range installlayout.ShellRequiredNames(runtime.GOOS) {
		p := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(label+"-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if includeLauncher {
		name := installlayout.LauncherBinaryName()
		if err := os.WriteFile(filepath.Join(staging, name), []byte(label+"-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(staging, "app", "resources", "bin", "deepzora-cli-launcher.exe")
	if err := os.MkdirAll(filepath.Dir(entry), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(label+"-entry"), 0755); err != nil {
		t.Fatal(err)
	}
	return staging
}

func TestActivateInstallerStagingPublishesVersionAndRootEntries(t *testing.T) {
	root := t.TempDir()
	staging := writeInstallerStaging(t, root, "new", true)
	if err := activateInstallerStaging(root, "v1.20.0", staging); err != nil {
		t.Fatal(err)
	}
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil || ptr.ActiveVersion != "v1.20.0" {
		t.Fatalf("pointer=%+v err=%v", ptr, err)
	}
	activeDesktop, err := installlayout.ActiveDesktopPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(activeDesktop); err != nil || string(got) != "new-"+installlayout.DesktopBinaryName() {
		t.Fatalf("active desktop=%q err=%v", got, err)
	}
	for _, name := range []string{installlayout.CanonicalLauncherBinaryName(), installlayout.CLIBinaryName()} {
		if info, err := os.Lstat(filepath.Join(root, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("root entry %s missing or invalid: %v", name, err)
		}
	}
	if alias := installlayout.PortableAliasName(); alias != "" {
		if got, err := os.ReadFile(filepath.Join(root, alias)); err != nil || string(got) != "new-"+installlayout.LauncherBinaryName() {
			t.Fatalf("portable alias=%q err=%v", got, err)
		}
		if _, err := os.Lstat(filepath.Join(root, installlayout.LauncherBinaryName())); !os.IsNotExist(err) {
			t.Fatalf("fresh installation created a legacy entry: %v", err)
		}
	}
}

func TestActivateInstallerStagingPrefersThinCLIEntryAndRejectsInvalidEntry(t *testing.T) {
	root := t.TempDir()
	staging := writeInstallerStaging(t, root, "new", true)
	entry := filepath.Join(staging, "app", "resources", "bin", "deepzora-cli-launcher.exe")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("thin-entry"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := activateInstallerStaging(root, "v1.39.0", staging); err != nil {
		t.Fatal(err)
	}
	rootCLI, err := os.ReadFile(filepath.Join(root, installlayout.CLIBinaryName()))
	if err != nil || string(rootCLI) != "thin-entry" {
		t.Fatalf("root CLI=%q err=%v, want thin entry", rootCLI, err)
	}
	activeCLI, err := installlayout.ActiveCLIPath(root)
	if err != nil {
		t.Fatal(err)
	}
	fullCLI, err := os.ReadFile(activeCLI)
	if err != nil || string(fullCLI) != "new-"+installlayout.CLIBinaryName() {
		t.Fatalf("active CLI=%q err=%v, want full CLI", fullCLI, err)
	}

	badRoot := t.TempDir()
	badStaging := writeInstallerStaging(t, badRoot, "bad", true)
	badEntry := filepath.Join(badStaging, "app", "resources", "bin", "deepzora-cli-launcher.exe")
	if err := os.MkdirAll(filepath.Dir(badEntry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(badEntry); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(badStaging, installlayout.CLIBinaryName()), badEntry); err != nil {
		t.Fatal(err)
	}
	if err := activateInstallerStaging(badRoot, "v1.39.0", badStaging); err == nil {
		t.Fatal("installer accepted a symlink CLI entry")
	}
	if installlayout.HasCurrent(badRoot) {
		t.Fatal("invalid CLI entry committed current.json")
	}
}

func TestActivateInstallerStagingFailureKeepsPreviousPointer(t *testing.T) {
	root := t.TempDir()
	oldStaging := writeInstallerStaging(t, root, "old", true)
	if err := activateInstallerStaging(root, "v1.19.1", oldStaging); err != nil {
		t.Fatal(err)
	}
	broken := writeInstallerStaging(t, root, "broken", false)
	if err := activateInstallerStaging(root, "v1.20.0", broken); err == nil {
		t.Fatal("staging without a launcher was activated")
	}
	ptr, err := installlayout.ReadCurrent(root)
	if err != nil || ptr.ActiveVersion != "v1.19.1" {
		t.Fatalf("previous pointer changed after failed activation: %+v err=%v", ptr, err)
	}
	if err := activateInstallerStaging(root, "v1.20.0", t.TempDir()); err == nil {
		t.Fatal("staging outside the install root was accepted")
	}
}
