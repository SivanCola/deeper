//go:build windows

package winaclresidue

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func installReadDeny(t *testing.T, path, userSID string) {
	t.Helper()
	if err := icacls(path, "/deny", "*"+userSID+":(RX)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = icacls(path, "/remove:d", "*"+userSID, "/C") })
}

func TestResetCredentialDACLRestoresAccessWithoutReadingACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userSID, err := currentProcessUserSIDString()
	if err != nil {
		t.Fatal(err)
	}
	installReadDeny(t, path, userSID)
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("deny did not block reads")
	}
	if err := ResetCredentialDACL(path); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "KEY=value\n" {
		t.Fatalf("credential after reset = %q, %v", data, err)
	}
	// SDDL renders well-known accounts as aliases (the CI runner's admin is
	// "LA"), so inspect the ACEs instead of matching the SID string.
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("reset DACL control = %#x err=%v, want SE_DACL_PROTECTED", control, err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		t.Fatalf("reset DACL = %s, want exactly one ACE", sd.String())
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(acl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	want, err := windows.StringToSid(userSID)
	if err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !windows.EqualSid((*windows.SID)(unsafe.Pointer(&ace.SidStart)), want) {
		t.Fatalf("reset DACL = %s, want an allow entry for the current user", sd.String())
	}
	runtime.KeepAlive(sd)
}

func TestRenameLockedFileMovesDeniedStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userSID, err := currentProcessUserSIDString()
	if err != nil {
		t.Fatal(err)
	}
	installReadDeny(t, path, userSID)
	target := path + ".locked-test"
	if err := RenameLockedFile(path, target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = icacls(target, "/remove:d", "*"+userSID, "/C") })
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("source still present after rename: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target missing after rename: %v", err)
	}
	if err := RenameLockedFile(filepath.Join(dir, "missing"), target); err == nil {
		t.Fatal("rename of a missing file must fail")
	}
}
