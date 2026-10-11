// Command deepzora-layout-installer activates a verified current release.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"deepzora/internal/config"
	"deepzora/internal/desktopinstance"
	"deepzora/internal/installlayout"
)

var version = "dev"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("deepzora-layout-installer", version)
		return 0
	}
	installRoot, stagingRoot, activeVersion := "", "", version
	interactive := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--interactive-recovery":
			interactive = true
		case "--install-root", "--activate-staging", "--version":
			option := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "error:", option, "requires a value")
				return 2
			}
			switch option {
			case "--install-root":
				installRoot = args[i]
			case "--activate-staging":
				stagingRoot = args[i]
			case "--version":
				activeVersion = args[i]
			}
		default:
			fmt.Fprintln(os.Stderr, "error: unknown option", args[i])
			return 2
		}
	}
	if strings.TrimSpace(installRoot) == "" || strings.TrimSpace(stagingRoot) == "" {
		fmt.Fprintln(os.Stderr, "error: --install-root and --activate-staging are required")
		return 2
	}
	normalized, err := normalizeActiveVersion(activeVersion)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if err = activateInstallerStagingWithRecovery(installRoot, normalized, stagingRoot, interactive); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return desktopinstance.ExitCode(err)
	}
	return 0
}

func normalizeActiveVersion(activeVersion string) (string, error) {
	if err := installlayout.ValidateVersionName(activeVersion); err != nil {
		// Accept bare "dev" builds in development only.
		if activeVersion == "" || activeVersion == "dev" {
			activeVersion = "v0.0.0-dev"
			if err := installlayout.ValidateVersionName(activeVersion); err != nil {
				return "", err
			}
		} else if !strings.HasPrefix(activeVersion, "v") {
			activeVersion = "v" + activeVersion
			if err := installlayout.ValidateVersionName(activeVersion); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	return activeVersion, nil
}

func activateInstallerStaging(installRoot, activeVersion, stagingRoot string) error {
	return activateInstallerStagingWithRecovery(installRoot, activeVersion, stagingRoot, false)
}

func activateInstallerStagingWithRecovery(installRoot, activeVersion, stagingRoot string, interactive bool) error {
	installRoot = filepath.Clean(strings.TrimSpace(installRoot))
	stagingRoot = filepath.Clean(strings.TrimSpace(stagingRoot))
	if !pathWithinInstallRoot(installRoot, stagingRoot) || stagingRoot == installRoot {
		return fmt.Errorf("installer staging must be inside the install root")
	}
	info, err := os.Lstat(stagingRoot)
	if err != nil {
		return fmt.Errorf("inspect installer staging: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("installer staging must be a real directory")
	}

	desktopName := installlayout.DesktopBinaryName()
	cliName := installlayout.CLIBinaryName()
	requiredNames := []string{desktopName, cliName}
	if runtime.GOOS == "windows" {
		requiredNames = append(requiredNames, installlayout.UpdateHelperBinaryName())
	}
	members := make([]installlayout.Member, 0, len(requiredNames))
	for _, name := range requiredNames {
		members = append(members, installlayout.Member{Name: name, Path: filepath.Join(stagingRoot, name)})
	}
	shellMembers, err := installlayout.ShellMembers(stagingRoot, runtime.GOOS)
	if err != nil {
		return fmt.Errorf("installer shell: %w", err)
	}
	if len(shellMembers) == 0 {
		return fmt.Errorf("installer shell is missing; use the complete installer")
	}
	for _, member := range shellMembers {
		members = append(members, member)
		requiredNames = append(requiredNames, member.Name)
	}

	launcherName := installlayout.LauncherBinaryName()
	launcherSource := filepath.Join(stagingRoot, launcherName)
	cliEntrySource := filepath.Join(stagingRoot, "app", "resources", "bin", "deepzora-cli-launcher.exe")
	if info, entryErr := os.Lstat(cliEntrySource); entryErr != nil {
		return fmt.Errorf("inspect CLI entry: %w", entryErr)
	} else if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("CLI entry is not a regular file")
	}
	rootMembers := []installlayout.Member{
		{Name: launcherName, Path: launcherSource},
		{Name: cliName, Path: cliEntrySource},
	}
	requiredRootNames := []string{launcherName, cliName}

	home := config.DeepzoraHomeDir()
	release, err := desktopinstance.PrepareInstall(installRoot, home, interactive)
	if err != nil {
		return err
	}
	defer release()
	// The installer only shows an exit code; the recovery log keeps the reason.
	finish := desktopinstance.AttemptLog(home, "activate-staging", installRoot)
	request := installlayout.ActivationRequest{
		InstallRoot:       installRoot,
		Version:           activeVersion,
		RequestID:         "signed-installer-" + activeVersion,
		CheckProcesses:    func() error { return desktopinstance.CheckInstallVacant(installRoot, home) },
		Members:           members,
		RequiredNames:     requiredNames,
		RootMembers:       rootMembers,
		RequiredRootNames: requiredRootNames,
	}
	if runtime.GOOS == "windows" {
		request.RootMembers, request.RequiredRootNames = nil, nil
		request.WindowsRootEntries = &installlayout.WindowsRootEntrySources{
			LauncherPath: launcherSource,
			CLIEntryPath: cliEntrySource,
		}
	}
	err = installlayout.ActivateVersion(request)
	finish(err)
	if err != nil {
		return fmt.Errorf("activate signed installer staging: %w", err)
	}
	return nil
}

func pathWithinInstallRoot(installRoot, target string) bool {
	root := filepath.Clean(strings.TrimSpace(installRoot))
	target = filepath.Clean(strings.TrimSpace(target))
	if root == "" || target == "" || !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
