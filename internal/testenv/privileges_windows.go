//go:build windows

package testenv

import (
	"errors"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

// WithoutWindowsBackupPrivileges keeps file-access tests governed by the DACL even
// when the runner can bypass it with backup privileges. The process token stays
// unchanged; the returned function must be deferred on the calling goroutine.
func WithoutWindowsBackupPrivileges(t testing.TB) func() {
	t.Helper()
	var process, token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &process); err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	access := uint32(windows.TOKEN_IMPERSONATE | windows.TOKEN_QUERY | windows.TOKEN_ADJUST_PRIVILEGES)
	if err := windows.DuplicateTokenEx(process, access, nil, windows.SecurityImpersonation, windows.TokenImpersonation, &token); err != nil {
		t.Fatal(err)
	}
	// Keep ordinary privileges such as SeChangeNotifyPrivilege: removing
	// traversal bypass would make normal profile-path canonicalization fail.
	for _, name := range []string{"SeBackupPrivilege", "SeRestorePrivilege"} {
		ptr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			token.Close()
			t.Fatal(err)
		}
		state := windows.Tokenprivileges{PrivilegeCount: 1}
		if err := windows.LookupPrivilegeValue(nil, ptr, &state.Privileges[0].Luid); err != nil {
			token.Close()
			t.Fatal(err)
		}
		if err := windows.AdjustTokenPrivileges(token, false, &state, 0, nil, nil); err != nil && !errors.Is(err, windows.ERROR_NOT_ALL_ASSIGNED) {
			token.Close()
			t.Fatal(err)
		}
	}
	runtime.LockOSThread()
	var previous windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_IMPERSONATE, true, &previous); err != nil && !errors.Is(err, windows.ERROR_NO_TOKEN) {
		runtime.UnlockOSThread()
		token.Close()
		t.Fatal(err)
	}
	if err := windows.SetThreadToken(nil, token); err != nil {
		previous.Close()
		runtime.UnlockOSThread()
		token.Close()
		t.Fatal(err)
	}
	return func() {
		if err := windows.SetThreadToken(nil, previous); err != nil {
			t.Errorf("restore test thread token: %v", err)
		}
		if previous != 0 {
			previous.Close()
		}
		token.Close()
		runtime.UnlockOSThread()
	}
}
