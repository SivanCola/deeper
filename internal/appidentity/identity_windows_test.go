//go:build windows

package appidentity

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestOwnedShortcutTargetCoversStableAndVersionedEntries(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		target string
		want   bool
	}{
		{filepath.Join(root, "deepzora-launcher.exe"), false},
		{filepath.Join(root, "Deepzora.exe"), true},
		{filepath.Join(root, "deepzora-desktop.exe"), false},
		{filepath.Join(root, "versions", "v1.20.0", "deepzora-desktop.exe"), false},
		{filepath.Join(root, "versions", "v1.20.0", "deepzora-cli.exe"), false},
		{filepath.Join(`D:\Apps`, "Deepzora", "deepzora-launcher.exe"), false},
	}
	for _, test := range tests {
		if got := ownedShortcutTarget(test.target, root); got != test.want {
			t.Errorf("ownedShortcutTarget(%q, %q) = %v, want %v", test.target, root, got, test.want)
		}
	}
}

func TestDeepzoraShortcutName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Deepzora.lnk", true},
		{"deepzora launcher.LNK", true},
		{"Deepzora (2).lnk", true},
		{"Other.lnk", false},
		{"Deepzora.exe", false},
	}
	for _, test := range tests {
		if got := deepzoraShortcutName(test.name); got != test.want {
			t.Errorf("deepzoraShortcutName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestRepairOwnedShortcutPersistsAppUserModelID(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Deepzora.exe")
	if err := os.WriteFile(target, []byte("launcher"), 0o600); err != nil {
		t.Fatal(err)
	}
	shortcutPath := filepath.Join(root, "Deepzora.lnk")

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	uninitialize, err := initializeCOM()
	if err != nil {
		t.Fatal(err)
	}
	defer uninitialize()
	createTestShortcut(t, shortcutPath, target)

	changed, err := repairOwnedShortcut(shortcutPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first repair did not report a change")
	}
	shortcut, err := loadShortcut(shortcutPath, stgmReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	got, err := shortcut.appUserModelID()
	shortcut.release()
	if err != nil {
		t.Fatal(err)
	}
	if got != AppUserModelID {
		t.Fatalf("shortcut AppUserModelID = %q, want %q", got, AppUserModelID)
	}
	changed, err = repairOwnedShortcut(shortcutPath, root)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second repair rewrote an already healthy shortcut")
	}
}

func TestRepairOwnedShortcutLeavesSeparateDeepzora053InstallUntouched(t *testing.T) {
	currentRoot := t.TempDir()
	legacyRoot := t.TempDir()
	legacyTarget := filepath.Join(legacyRoot, "deepzora-desktop.exe")
	if err := os.WriteFile(legacyTarget, []byte("legacy tauri desktop"), 0o600); err != nil {
		t.Fatal(err)
	}
	shortcutPath := filepath.Join(t.TempDir(), "Deepzora.lnk")

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	uninitialize, err := initializeCOM()
	if err != nil {
		t.Fatal(err)
	}
	defer uninitialize()
	createTestShortcut(t, shortcutPath, legacyTarget)

	shortcut, err := loadShortcut(shortcutPath, stgmReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := shortcut.setAppUserModelID("com.deepzora.desktop.dev"); err != nil {
		shortcut.release()
		t.Fatal(err)
	}
	shortcut.release()

	changed, err := repairOwnedShortcut(shortcutPath, currentRoot)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("current install rewrote a shortcut owned by a separate Deepzora 0.53 installation")
	}

	shortcut, err = loadShortcut(shortcutPath, stgmReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	gotTarget, targetErr := shortcut.targetPath()
	gotID, idErr := shortcut.appUserModelID()
	shortcut.release()
	if targetErr != nil {
		t.Fatal(targetErr)
	}
	if idErr != nil {
		t.Fatal(idErr)
	}
	if !migrationSamePath(gotTarget, legacyTarget) {
		t.Fatalf("legacy shortcut target = %q, want %q", gotTarget, legacyTarget)
	}
	if gotID != "com.deepzora.desktop.dev" {
		t.Fatalf("legacy shortcut AppUserModelID = %q, want %q", gotID, "com.deepzora.desktop.dev")
	}
}

func createTestShortcut(t *testing.T, shortcutPath, target string) {
	t.Helper()
	var link *shellLinkW
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if err := checkHRESULT("CoCreateInstance(CLSID_ShellLink)", hr); err != nil {
		t.Fatal(err)
	}
	defer releaseInterface(unsafe.Pointer(link))
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	hr, _, _ = syscall.SyscallN(
		link.VTable.SetPath,
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(targetPtr)),
	)
	if err := checkHRESULT("IShellLinkW.SetPath", hr); err != nil {
		t.Fatal(err)
	}
	var persist *persistFile
	if err := queryInterface(unsafe.Pointer(link), &iidIPersistFile, unsafe.Pointer(&persist)); err != nil {
		t.Fatal(err)
	}
	defer releaseInterface(unsafe.Pointer(persist))
	shortcutPtr, err := windows.UTF16PtrFromString(shortcutPath)
	if err != nil {
		t.Fatal(err)
	}
	hr, _, _ = syscall.SyscallN(
		persist.VTable.Save,
		uintptr(unsafe.Pointer(persist)),
		uintptr(unsafe.Pointer(shortcutPtr)),
		1,
	)
	if err := checkHRESULT("IPersistFile.Save", hr); err != nil {
		t.Fatal(err)
	}
}
