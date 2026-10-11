//go:build windows

package winaclresidue

import (
	"bytes"
	"context"
	"deepzora/internal/proc"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const icaclsTimeout = 30 * time.Second

func currentProcessUserSIDString() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	if user == nil || user.User.Sid == nil {
		return "", fmt.Errorf("current process token has no user SID")
	}
	return user.User.Sid.String(), nil
}

// systemRootTool resolves a Windows system tool below %SystemRoot%\System32
// so a PATH-shadowed binary can never run in its place.
func systemRootTool(name string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = os.Getenv("windir")
	}
	if root == "" {
		root = `C:\Windows`
	}
	full := filepath.Join(root, "System32", name)
	if _, err := os.Stat(full); err == nil {
		return full
	}
	return name
}

func icacls(path string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), icaclsTimeout)
	defer cancel()
	cmd := proc.CommandContext(ctx, systemRootTool("icacls.exe"), append([]string{path}, args...)...)
	proc.HideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("icacls %q %s: %w: %s", path, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}
