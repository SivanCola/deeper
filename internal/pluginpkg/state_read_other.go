//go:build !windows

package pluginpkg

import "os"

func openStateFile(path string) (*os.File, error) {
	return os.Open(path)
}
