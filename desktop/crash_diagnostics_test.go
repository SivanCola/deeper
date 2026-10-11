package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDesktopLifecycleReportUsesCurrentLifecycleNamespace(t *testing.T) {
	report := desktopLifecycleReport(desktopLifecycleObservation{
		Version: "v1.23.0", Channel: "stable", Phase: "healthy",
		StartedAt: "2026-08-10T01:00:00Z", UpdatedAt: "2026-08-10T02:00:00Z",
	})
	if report.Source != "native.lifecycle" || report.Label != "desktop.abnormal_exit.v2" {
		t.Fatalf("report = %+v", report)
	}
	if report.FingerprintHint != "desktop.abnormal_exit.v2."+runtime.GOOS+".healthy" {
		t.Fatalf("fingerprint = %q", report.FingerprintHint)
	}
}

func TestCapturePreviousFatalCrashQueuesAndRemovesRawDump(t *testing.T) {
	resetFatalCrashArtifacts(t)
	const crashedPID = 424242
	raw := "fatal error: concurrent map writes\n\ngoroutine 1 [running]:\nmain.run()\n\t/home/alice/project/main.go:12\n"
	path := fatalCrashPathForPID(crashedPID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	capturePreviousFatalCrash()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("raw fatal dump was not removed: %v", err)
	}
	report, ok := readPending(t)
	if !ok || report.Label != "go.fatal" || report.Source != "go.runtime" {
		t.Fatalf("queued report = %+v ok=%v", report, ok)
	}
	if strings.Contains(report.Stack, "/home/alice") {
		t.Fatalf("fatal stack leaked home path: %q", report.Stack)
	}
}

func TestSanitizeFatalRuntimeDumpRemovesPanicValue(t *testing.T) {
	got := sanitizeFatalRuntimeDump("panic: private prompt text\n\ngoroutine 1 [running]:\nmain.run()\n\t/home/alice/project/main.go:12\n")
	if strings.Contains(got, "private prompt text") || strings.Contains(got, "/home/alice") {
		t.Fatalf("fatal dump leaked user-controlled text: %q", got)
	}
	if !strings.Contains(got, "panic: [redacted panic value]") || !strings.Contains(got, "main.go:12") {
		t.Fatalf("fatal dump lost diagnostic structure: %q", got)
	}
}

func TestCapturePreviousFatalCrashSkipsRuntimeDuplicateOfStructuredPanic(t *testing.T) {
	resetFatalCrashArtifacts(t)
	const crashedPID = 424243
	path := fatalCrashPathForPID(crashedPID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("panic: duplicate\n\ngoroutine 1 [running]:\nmain.run()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	markFatalCrashCoveredForPID(crashedPID)
	capturePreviousFatalCrash()
	if _, ok := readPending(t); ok {
		t.Fatal("runtime duplicate was queued despite structured panic marker")
	}
}

func TestCapturePreviousFatalCrashDoesNotTouchLiveOwner(t *testing.T) {
	resetFatalCrashArtifacts(t)
	const livePID = 424244
	path := fatalCrashPathForPID(livePID)
	raw := []byte("fatal error: still being written\n\ngoroutine 1 [running]:\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	oldProcessAlive := fatalCrashProcessAlive
	fatalCrashProcessAlive = func(pid int) bool { return pid == livePID }
	t.Cleanup(func() { fatalCrashProcessAlive = oldProcessAlive })

	capturePreviousFatalCrash()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("live owner's fatal file was removed: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("live owner's fatal file changed: got %q want %q", got, raw)
	}
	if _, ok := readPending(t); ok {
		t.Fatal("live owner's partial fatal output was queued")
	}
}

func resetFatalCrashArtifacts(t *testing.T) {
	t.Helper()
	oldProcessAlive := fatalCrashProcessAlive
	fatalCrashProcessAlive = func(int) bool { return false }
	removeAllPendingCrashes()
	_ = os.RemoveAll(fatalCrashDir())
	t.Cleanup(func() {
		removeAllPendingCrashes()
		_ = os.RemoveAll(fatalCrashDir())
		fatalCrashProcessAlive = oldProcessAlive
	})
}
