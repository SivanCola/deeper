//go:build windows

package builtin

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func advanceObservationChangeTime(t *testing.T, path string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES|windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		Attributes, Reserved                                    uint32
	}
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		t.Fatal(err)
	}
	// Equal-size writes can share one Windows timestamp tick. Establish an
	// explicit native change without changing identity, size, mtime or content.
	// This tests stale-version enforcement, not how quickly the clock advances.
	change := basic.ChangeTime + 20_000_000
	basic.CreationTime, basic.LastAccessTime, basic.LastWriteTime = 0, 0, 0
	basic.Attributes, basic.Reserved, basic.ChangeTime = 0, 0, change
	if err := windows.SetFileInformationByHandle(handle, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		t.Fatal(err)
	}
}
