//go:build windows

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"deepzora/internal/sandbox"
)

func TestBashCancelKillsWindowsChildProcessTree(t *testing.T) {
	powershell, err := exec.LookPath("powershell")
	if err != nil {
		t.Skip("powershell not found")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "child.pid")
	quotedPIDFile := strings.ReplaceAll(pidFile, "'", "''")
	command := fmt.Sprintf(
		"$p = Start-Process -FilePath powershell -ArgumentList '-NoProfile','-NonInteractive','-Command','Start-Sleep -Seconds 120' -PassThru; "+
			"Set-Content -LiteralPath '%s' -Value $p.Id; "+
			"Start-Sleep -Seconds 120",
		quotedPIDFile,
	)
	args, _ := json.Marshal(map[string]any{"command": command})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = fullAccessBashTestContext(ctx)

	done := make(chan error, 1)
	go func() {
		_, runErr := (bash{
			shell: sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershell},
		}).Execute(ctx, args)
		done <- runErr
	}()

	childPID := waitForWindowsPIDFile(t, pidFile)
	cancel()
	select {
	case err = <-done:
	case <-time.After(40 * time.Second):
		killWindowsPID(childPID)
		t.Fatal("cancel did not interrupt bash within 40s")
	}
	if err == nil {
		t.Fatal("expected cancel to return an error")
	}
	waitForWindowsProcessExit(t, childPID, "bash cancel")
}

func TestBashWindowsReapsChildAfterForegroundShellExit(t *testing.T) {
	powershell, err := exec.LookPath("powershell")
	if err != nil {
		t.Skip("powershell not found")
	}
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "child.pid")
	quotedPIDFile := strings.ReplaceAll(pidFile, "'", "''")
	command := fmt.Sprintf(
		"$p = Start-Process -FilePath powershell -ArgumentList '-NoProfile','-NonInteractive','-Command','Start-Sleep -Seconds 120' -PassThru; "+
			"Set-Content -LiteralPath '%s' -Value $p.Id",
		quotedPIDFile,
	)
	args, _ := json.Marshal(map[string]any{"command": command})

	out, err := (bash{
		shell: sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: powershell},
	}).Execute(fullAccessBashTestContext(t.Context()), args)
	childPID := waitForWindowsPIDFile(t, pidFile)
	if err != nil {
		killWindowsPID(childPID)
		t.Fatalf("foreground command failed: %v (out=%q)", err, out)
	}
	waitForWindowsProcessExit(t, childPID, "foreground bash cleanup")
}

func TestBashCancelKillsGitBashHereDocPython(t *testing.T) {
	sh := sandbox.ResolveShell("bash", "", nil)
	if sh.Kind != sandbox.ShellBash || sh.Path == "" {
		t.Skip("Git Bash not found")
	}
	python := gitBashPython(t, sh.Path)
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "python.pid")
	pythonPIDFile := filepath.ToSlash(pidFile)
	command := fmt.Sprintf("%s - <<'PYEOF'\nimport os, time\nwith open(%q, 'w') as f:\n    f.write(str(os.getpid()))\n    f.flush()\ntime.sleep(120)\nPYEOF\n", shellQuote(python), pythonPIDFile)
	args, _ := json.Marshal(map[string]any{"command": command})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = fullAccessBashTestContext(ctx)

	done := make(chan error, 1)
	go func() {
		_, runErr := (bash{shell: sh}).Execute(ctx, args)
		done <- runErr
	}()

	childPID := waitForWindowsPIDFile(t, pidFile)
	time.Sleep(300 * time.Millisecond) // let the tracker observe the Git Bash child tree.
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancel to return an error")
		}
	case <-time.After(20 * time.Second):
		killWindowsPID(childPID)
		t.Fatal("cancel did not interrupt Git Bash here-doc python within 20s")
	}
	waitForWindowsProcessExit(t, childPID, "Git Bash here-doc python cancel")
}

// windowsPIDFileWait and windowsProcessExitWait are liveness watchdogs rather
// than correctness deadlines. Publishing the pid file costs two PowerShell
// launches plus a disk write, and observing a killed child costs one PowerShell
// probe per poll; both can stall past a small bound on a runner executing the
// whole suite in parallel. Windows CI has failed this file at 10s waiting for
// the pid file, so keep the bounds generous and let the assertions decide
// whether the child tree was actually killed.
const (
	windowsPIDFileWait     = 60 * time.Second
	windowsProcessExitWait = 60 * time.Second
)

func waitForWindowsPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(windowsPIDFileWait)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for child pid file %s", path)
	return 0
}

// waitForWindowsProcessExit waits for the caller's already-requested kill to
// land. Only the observation is bounded, so a slow probe cannot turn a correct
// kill into a failure.
func waitForWindowsProcessExit(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(windowsProcessExitWait)
	for time.Now().Before(deadline) {
		if !windowsProcessAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	killWindowsPID(pid)
	t.Fatalf("child process %d survived %s", pid, what)
}

func gitBashPython(t *testing.T, bashPath string) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		python := gitBashPath(t, bashPath, path)
		out, err := exec.Command(bashPath, "-lc", fmt.Sprintf("%s - <<'PYEOF'\nprint('ok')\nPYEOF\n", shellQuote(python))).CombinedOutput()
		if err == nil {
			return python
		}
		t.Logf("python candidate %s is not usable from Git Bash: %v: %s", python, err, strings.TrimSpace(string(out)))
	}
	t.Skip("python not found or not usable from Git Bash")
	return ""
}

func gitBashPath(t *testing.T, bashPath, path string) string {
	t.Helper()
	out, err := exec.Command(bashPath, "-lc", fmt.Sprintf("cygpath -u %s", shellQuote(path))).Output()
	if err == nil {
		converted := strings.TrimSpace(string(out))
		if converted != "" {
			return strings.Split(converted, "\n")[0]
		}
	}
	return filepath.ToSlash(path)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func windowsProcessAlive(pid int) bool {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", fmt.Sprintf("if (Get-Process -Id %d -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }", pid))
	return cmd.Run() == nil
}

func killWindowsPID(pid int) {
	_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}
