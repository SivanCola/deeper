package installlayout

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeMembers(t *testing.T, dir string, body string) []Member {
	t.Helper()
	members := make([]Member, 0, 3)
	for _, name := range AllowedVersionMembers() {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body+"-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
		members = append(members, Member{Name: name, Path: path})
	}
	return members
}

func TestActivateVersionFaultInjectionKeepsOldPointer(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	// Seed v1.19.1 as active.
	if err := ActivateVersion(ActivationRequest{
		InstallRoot: root,
		Version:     "v1.19.1",
		RequestID:   "seed",
		Members:     writeMembers(t, src, "old"),
	}); err != nil {
		t.Fatal(err)
	}

	// Interrupt before rename by using a source that disappears mid-activation
	// is hard without hooks; instead inject a bad member set after staging prep
	// via duplicate-name rejection and verify pointer unchanged.
	badSrc := t.TempDir()
	members := writeMembers(t, badSrc, "new")
	// Corrupt one source path after validation list is built by emptying file content
	// and making the desktop source a directory (not a regular file).
	desktop := filepath.Join(badSrc, DesktopBinaryName())
	_ = os.Remove(desktop)
	if err := os.Mkdir(desktop, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ActivateVersion(ActivationRequest{
		InstallRoot: root,
		Version:     "v1.20.0",
		RequestID:   "fault",
		Members:     members,
	}); err == nil {
		t.Fatal("expected activation failure")
	}
	ptr, err := ReadCurrent(root)
	if err != nil || ptr.ActiveVersion != "v1.19.1" {
		t.Fatalf("pointer changed under failure: %+v err=%v", ptr, err)
	}
}

func TestActivateVersionRejectsPathTraversalAndSymlinkTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege varies on Windows CI")
	}
	root := t.TempDir()
	src := t.TempDir()
	members := writeMembers(t, src, "x")
	// Absolute activeDir is rejected by Decode/WriteCurrent.
	if err := WriteCurrent(root, CurrentPointer{
		SchemaVersion: 1,
		ActiveVersion: "v1.20.0",
		ActiveDir:     "/etc/passwd",
	}); err == nil {
		t.Fatal("absolute activeDir must fail")
	}
	// Symlink inside versions/ is rejected on read.
	if err := ActivateVersion(ActivationRequest{
		InstallRoot: root,
		Version:     "v1.20.0",
		RequestID:   "ok",
		Members:     members,
	}); err != nil {
		t.Fatal(err)
	}
	// Replace version dir with symlink and ensure ReadCurrent fails closed.
	ver := filepath.Join(root, "versions", "v1.20.0")
	outside := t.TempDir()
	_ = os.RemoveAll(ver)
	if err := os.Symlink(outside, ver); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCurrent(root); err == nil {
		t.Fatal("symlink version dir must be rejected")
	}
}
