package cli

import (
	"fmt"
	"os"

	"deepzora/internal/i18n"
)

// cliFailure reports err on stderr and yields the process exit code, so a
// command can end a failed step in one statement.
func cliFailure(err error) int {
	fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
	return 1
}
