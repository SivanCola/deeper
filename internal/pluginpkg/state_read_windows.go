//go:build windows

package pluginpkg

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openStateFile(path string) (*os.File, error) {
	readPath := path
	// Keep os.Open's long-path support when calling CreateFile directly.
	if len(path) >= 248 && !strings.HasPrefix(path, `\\?\`) && !strings.HasPrefix(path, `\\.\`) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		if strings.HasPrefix(absolute, `\\`) {
			readPath = `\\?\UNC\` + absolute[2:]
		} else {
			readPath = `\\?\` + absolute
		}
	}
	name, err := windows.UTF16PtrFromString(readPath)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// Delete sharing lets SaveState publish atomically while readers hold the
	// old snapshot, without exhausting writer retries. Follow reparse points
	// to preserve the existing os.ReadFile behavior.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(handle), path)
	if f == nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	return f, nil
}
