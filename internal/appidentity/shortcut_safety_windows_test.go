//go:build windows

package appidentity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestRepairOwnedShortcutLeavesSeparateStudioInstallationsByteIdentical(t *testing.T) {
	for _, test := range []struct{ name, executable, id string }{
		{"legacy_studio", "deepzora-studio.exe", "Deepzora"},
		{"electron_studio", "Deepzora Studio.exe", "com.deepzora.desktop.studio"},
	} {
		t.Run(test.name, func(t *testing.T) {
			migrationTestCOM(t)
			root, studioRoot := t.TempDir(), t.TempDir()
			migrationTestFile(t, filepath.Join(root, "deepzora-launcher.exe"))
			target := filepath.Join(studioRoot, test.executable)
			migrationTestFile(t, target)
			path := filepath.Join(t.TempDir(), "Deepzora Studio.lnk")
			migrationTestShortcut(t, path, migrationShortcutState{
				target: target, id: test.id, arguments: "--workspace studio",
				description: "Studio", workingDirectory: studioRoot, icon: target, showCmd: 1,
			})
			migrationAssertUnchanged(t, path, root)
		})
	}
}

func TestRepairOwnedShortcutPreservesExplicitForeignIdentityInsideCurrentInstall(t *testing.T) {
	for _, id := range []string{"com.deepzora.desktop.studio", "com.deepzora.desktop.dev", "com.example.custom"} {
		t.Run(id, func(t *testing.T) {
			migrationTestCOM(t)
			root := t.TempDir()
			target := filepath.Join(root, "versions", "v1.20.0", "app", "Deepzora.exe")
			migrationTestFile(t, target)
			migrationTestFile(t, filepath.Join(root, "deepzora-launcher.exe"))
			path := filepath.Join(root, "Deepzora.lnk")
			migrationTestShortcut(t, path, migrationShortcutState{
				target: target, id: id, workingDirectory: filepath.Dir(target), icon: target, showCmd: 1,
			})
			migrationAssertUnchanged(t, path, root)
		})
	}
}

func TestRepairOwnedShortcutRejectsVersionJunctionEscapingCurrentInstall(t *testing.T) {
	for _, executable := range []string{"deepzora-desktop.exe", filepath.Join("app", "Deepzora.exe")} {
		t.Run(executable, func(t *testing.T) {
			migrationTestCOM(t)
			root, externalRoot := t.TempDir(), t.TempDir()
			migrationTestFile(t, filepath.Join(root, "deepzora-launcher.exe"))
			migrationTestFile(t, filepath.Join(externalRoot, executable))
			versions := filepath.Join(root, "versions")
			if err := os.MkdirAll(versions, 0o755); err != nil {
				t.Fatal(err)
			}
			junction := filepath.Join(versions, "v1.20.0")
			if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, externalRoot).CombinedOutput(); err != nil {
				t.Fatalf("create directory junction: %v: %s", err, output)
			}
			target := filepath.Join(junction, executable)
			path := filepath.Join(root, "Deepzora.lnk")
			migrationTestShortcut(t, path, migrationShortcutState{
				target: target, id: "Deepzora", workingDirectory: filepath.Dir(target), icon: target, showCmd: 1,
			})
			migrationAssertUnchanged(t, path, root)
		})
	}
}

type migrationShortcutState struct {
	target, id, arguments, description, workingDirectory, icon string
	iconIndex, showCmd                                         int32
}

func TestRepairShortcutsPreservesTargetAndArguments(t *testing.T) {
	migrationTestCOM(t)
	root := t.TempDir()
	target := filepath.Join(root, "Deepzora.exe")
	// A non-executable target also proves repair never starts the desktop.
	migrationTestFile(t, target)
	path := filepath.Join(root, "Deepzora.lnk")
	const arguments = `--session "workspace name"`
	migrationTestShortcut(t, path, migrationShortcutState{target: target, arguments: arguments})
	if err := RepairShortcuts(root, []string{path}); err != nil {
		t.Fatal(err)
	}
	got := migrationReadShortcut(t, path)
	if !migrationSamePath(got.target, target) || got.arguments != arguments || got.id != AppUserModelID {
		t.Fatalf("repaired shortcut = %+v", got)
	}
}

func migrationTestCOM(t *testing.T) {
	t.Helper()
	runtime.LockOSThread()
	uninitialize, err := initializeCOM()
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	t.Cleanup(func() { uninitialize(); runtime.UnlockOSThread() })
}

func migrationTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func migrationTestShortcut(t *testing.T, path string, state migrationShortcutState) {
	t.Helper()
	createTestShortcut(t, path, state.target)
	s, err := loadShortcut(path, stgmReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.release()
	for _, field := range []struct {
		method uintptr
		value  string
	}{
		{s.link.VTable.SetArguments, state.arguments},
		{s.link.VTable.SetDescription, state.description},
		{s.link.VTable.SetWorkingDirectory, state.workingDirectory},
	} {
		value, err := windows.UTF16PtrFromString(field.value)
		if err != nil {
			t.Fatal(err)
		}
		hr, _, _ := syscall.SyscallN(field.method, uintptr(unsafe.Pointer(s.link)), uintptr(unsafe.Pointer(value)))
		if err := checkHRESULT("set shortcut fixture property", hr); err != nil {
			t.Fatal(err)
		}
	}
	icon, err := windows.UTF16PtrFromString(state.icon)
	if err != nil {
		t.Fatal(err)
	}
	hr, _, _ := syscall.SyscallN(s.link.VTable.SetIconLocation, uintptr(unsafe.Pointer(s.link)), uintptr(unsafe.Pointer(icon)), uintptr(state.iconIndex))
	if err := checkHRESULT("SetIconLocation", hr); err != nil {
		t.Fatal(err)
	}
	hr, _, _ = syscall.SyscallN(s.link.VTable.SetShowCmd, uintptr(unsafe.Pointer(s.link)), uintptr(state.showCmd))
	if err := checkHRESULT("SetShowCmd", hr); err != nil {
		t.Fatal(err)
	}
	// The existing setter commits and persists all Shell Link properties.
	if err := s.setAppUserModelID(state.id); err != nil {
		t.Fatal(err)
	}
}

func migrationReadShortcut(t *testing.T, path string) migrationShortcutState {
	t.Helper()
	s, err := loadShortcut(path, stgmReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.release()
	var state migrationShortcutState
	state.target, err = s.targetPath()
	if err != nil {
		t.Fatal(err)
	}
	state.id, err = s.appUserModelID()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct {
		method uintptr
		value  *string
	}{
		{s.link.VTable.GetArguments, &state.arguments},
		{s.link.VTable.GetDescription, &state.description},
		{s.link.VTable.GetWorkingDirectory, &state.workingDirectory},
	} {
		buffer := make([]uint16, windowsPathBuffer)
		hr, _, _ := syscall.SyscallN(field.method, uintptr(unsafe.Pointer(s.link)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
		if err := checkHRESULT("get shortcut property", hr); err != nil {
			t.Fatal(err)
		}
		*field.value = windows.UTF16ToString(buffer)
	}
	buffer := make([]uint16, windowsPathBuffer)
	hr, _, _ := syscall.SyscallN(s.link.VTable.GetIconLocation, uintptr(unsafe.Pointer(s.link)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&state.iconIndex)))
	if err := checkHRESULT("GetIconLocation", hr); err != nil {
		t.Fatal(err)
	}
	state.icon = windows.UTF16ToString(buffer)
	hr, _, _ = syscall.SyscallN(s.link.VTable.GetShowCmd, uintptr(unsafe.Pointer(s.link)), uintptr(unsafe.Pointer(&state.showCmd)))
	if err := checkHRESULT("GetShowCmd", hr); err != nil {
		t.Fatal(err)
	}
	return state
}

func migrationReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func migrationAssertUnchanged(t *testing.T, path, root string) {
	t.Helper()
	before := migrationReadBytes(t, path)
	changed, err := repairOwnedShortcut(path, root)
	if err != nil || changed {
		t.Fatalf("foreign shortcut repair = (%v, %v), want (false, nil)", changed, err)
	}
	if !bytes.Equal(before, migrationReadBytes(t, path)) {
		t.Fatal("foreign shortcut bytes changed")
	}
}

func migrationSamePath(left, right string) bool {
	if strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func TestRepairShortcutsLeavesReadOnlyShortcutByteIdentical(t *testing.T) {
	migrationTestCOM(t)
	root := t.TempDir()
	launcher := filepath.Join(root, "deepzora-launcher.exe")
	target := filepath.Join(root, "versions", "v1.20.0", "app", "Deepzora.exe")
	migrationTestFile(t, launcher)
	migrationTestFile(t, target)
	path := filepath.Join(root, "Deepzora.lnk")
	migrationTestShortcut(t, path, migrationShortcutState{
		target: target, id: "Deepzora", icon: target, workingDirectory: filepath.Dir(target), showCmd: 1,
	})
	before := migrationReadBytes(t, path)
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		t.Fatal(err)
	}
	// Registered after TempDir so the read-only flag is removed before its cleanup.
	t.Cleanup(func() {
		if err := windows.SetFileAttributes(pathPtr, attributes); err != nil {
			t.Errorf("restore shortcut file attributes: %v", err)
		}
	})
	if err := windows.SetFileAttributes(pathPtr, attributes|windows.FILE_ATTRIBUTE_READONLY); err != nil {
		t.Fatal(err)
	}
	if err := RepairShortcuts(root, []string{path}); err == nil {
		t.Fatal("repair of a read-only shortcut must return an error")
	}
	if !bytes.Equal(before, migrationReadBytes(t, path)) {
		t.Fatal("failed repair changed the read-only shortcut bytes")
	}
}

func TestRepairShortcutsValidatesAllPathsBeforeWritingAnyShortcut(t *testing.T) {
	migrationTestCOM(t)
	root := t.TempDir()
	launcher := filepath.Join(root, "deepzora-launcher.exe")
	target := filepath.Join(root, "versions", "v1.20.0", "app", "Deepzora.exe")
	migrationTestFile(t, launcher)
	migrationTestFile(t, target)
	path := filepath.Join(root, "Deepzora.lnk")
	migrationTestShortcut(t, path, migrationShortcutState{
		target: target, id: "Deepzora", icon: target, workingDirectory: filepath.Dir(target), showCmd: 1,
	})
	before := migrationReadBytes(t, path)
	if err := RepairShortcuts(root, []string{path, "relative.lnk"}); err == nil {
		t.Fatal("repair must reject the later relative path")
	}
	if !bytes.Equal(before, migrationReadBytes(t, path)) {
		t.Fatal("repair wrote the first shortcut before validating the full path list")
	}
}
