// Package desktoplauncher implements the permanent Deepzora desktop entry
// point. It deliberately owns no crash-loop, rollback, or safe-mode policy.
package desktoplauncher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"deepzora/internal/appidentity"
	"deepzora/internal/installlayout"
	"deepzora/internal/proc"
)

// Run resolves and starts the current desktop process.
func Run(args []string, buildVersion string) int {
	if len(args) > 0 && args[0] == "--repair-shortcuts" {
		if err := repairInstallerShortcuts(args[1:], ResolveInstallRoot, appidentity.RepairShortcuts); err != nil {
			fmt.Fprintln(os.Stderr, "error: repair Windows shortcut integration:", err)
			return 1
		}
		return 0
	}
	if len(args) == 1 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Println("deepzora-launcher", buildVersion)
			return 0
		case "help", "--help", "-h":
			usage()
			return 0
		}
	}
	if err := appidentity.ApplyToCurrentProcess(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: apply Windows app identity:", err)
	}

	installRoot, err := ResolveInstallRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := appidentity.RepairOwnedShortcuts(installRoot); err != nil {
		fmt.Fprintln(os.Stderr, "warning: repair Windows shortcut identity:", err)
	}

	desktopPath, err := ResolveDesktopPath(installRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	if handled, code := coordinatedLaunch(installRoot, args); handled {
		return code
	}

	cmd := exec.Command(desktopPath, args...)
	proc.HideConsole(cmd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Dir = installRoot
	if DetachByDefault() {
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// ResolveInstallRoot returns the directory containing the launcher.
func ResolveInstallRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve launcher path: %w", err)
	}
	return resolveInstallRoot(exe)
}

func resolveInstallRoot(exe string) (string, error) {
	resolved, err := resolveExecutablePath(exe)
	if err != nil {
		return "", fmt.Errorf("resolve launcher path: %w", err)
	}
	return filepath.Clean(filepath.Dir(resolved)), nil
}

// ResolveDesktopPath resolves current.json when present. A present but invalid
// pointer is always fatal; only a genuinely absent pointer may use the flat
// sibling fallback needed by package-managed installs.
func ResolveDesktopPath(installRoot string) (string, error) {
	currentPath := filepath.Join(installRoot, installlayout.CurrentFileName)
	if _, err := os.Lstat(currentPath); err == nil {
		return installlayout.ActiveDesktopPath(installRoot)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect current.json: %w", err)
	}
	if path := siblingDesktop(installRoot); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("cannot locate deepzora-desktop under %s (missing current.json)", installRoot)
}

func siblingDesktop(installRoot string) string {
	path := filepath.Join(installRoot, installlayout.DesktopBinaryName())
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return path
}

// DetachByDefault reports whether the Windows packaged entry should start the
// desktop and exit immediately.
func DetachByDefault() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	name := strings.ToLower(filepath.Base(exe))
	return name == "deepzora-launcher.exe" || name == "deepzora.exe"
}

func usage() {
	fmt.Println("usage: deepzora-launcher [args...]")
	fmt.Println("  Starts the active Deepzora desktop from current.json.")
	fmt.Println("  --repair-shortcuts <absolute.lnk...> repairs owned installer shortcuts without launching.")
}
