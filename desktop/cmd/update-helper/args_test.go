package main

import (
	"strings"
	"testing"
)

func TestInstallerCommandLineUsesVisibleUpdateModeAndLeavesDFlagLast(t *testing.T) {
	got := installerCommandLine(`C:\Temp\Deepzora Installer.exe`, `D:\Tools\Deepzora App`)
	want := `"C:\Temp\Deepzora Installer.exe" /DEEPZORAUPDATE=1 /DEEPZORASTAGE=1 /D=D:\Tools\Deepzora App`
	if got != want {
		t.Fatalf("installerCommandLine = %q, want %q", got, want)
	}
	if strings.Contains(got, " /S") {
		t.Fatalf("auto-update must expose progress instead of using silent mode, got %q", got)
	}
	if !strings.HasSuffix(got, `/D=D:\Tools\Deepzora App`) {
		t.Fatalf("/D= must be the final unquoted NSIS token, got %q", got)
	}
	if !strings.Contains(got, " /DEEPZORASTAGE=1") {
		t.Fatalf("auto-update must extract away from the live install, got %q", got)
	}
}
