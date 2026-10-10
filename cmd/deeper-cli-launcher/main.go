// Command deeper-cli-launcher is the stable Windows console entry point for
// versioned desktop installations. It delegates to the active full CLI and
// deliberately contains no Deeper engine code.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"deeper/internal/installlayout"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deeper: locate CLI launcher:", err)
		os.Exit(1)
	}
	os.Exit(runCLI(exe, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runCLI(executable string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := filepath.Dir(filepath.Clean(executable))
	target, err := installlayout.ActiveCLIPathFor(root, "windows")
	if err != nil {
		fmt.Fprintln(stderr, "deeper: resolve active CLI:", err)
		return 1
	}
	if same, err := installlayout.SameRegularFile(executable, target); err != nil {
		fmt.Fprintln(stderr, "deeper: validate active CLI:", err)
		return 1
	} else if same {
		fmt.Fprintln(stderr, "deeper: active CLI resolves to the launcher itself")
		return 1
	}
	cmd := exec.Command(target, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(stderr, "deeper: start active CLI:", err)
		return 1
	}
	return 0
}
