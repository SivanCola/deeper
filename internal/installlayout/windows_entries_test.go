package installlayout

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/filelock"
)

func windowsEntryRequest(t *testing.T, root, version, body string) ActivationRequest {
	t.Helper()
	src := t.TempDir()
	req := ActivationRequest{InstallRoot: root, Version: version}
	for _, name := range AllowedVersionMembers() {
		req.Members = append(req.Members, Member{Name: name, Path: writeTempMember(t, src, name, body)})
	}
	req.WindowsRootEntries = &WindowsRootEntrySources{
		LauncherPath: writeTempMember(t, src, "payload-launcher", body+"-gui"),
		CLIEntryPath: writeTempMember(t, src, "payload-cli", body+"-cli"),
	}
	return req
}

func assertEntryBytes(t *testing.T, root, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q (%v), want %q", name, got, err, want)
	}
}

func TestWindowsEntriesFreshRepairAndUpgrade(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Deepzora 测试")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, version := range []string{"v1.38.9", "v1.38.9", "v1.39.0"} {
		body := []string{"install", "repair", "upgrade"}[i]
		if err := ActivateVersion(windowsEntryRequest(t, root, version, body)); err != nil {
			t.Fatal(err)
		}
		assertEntryBytes(t, root, "Deepzora.exe", body+"-gui")
		assertEntryBytes(t, root, "deepzora-cli.exe", body+"-cli")
		if _, err := os.Lstat(filepath.Join(root, "deepzora-launcher.exe")); !os.IsNotExist(err) {
			t.Fatalf("retired entry created: %v", err)
		}
		ptr, err := ReadCurrent(root)
		if err != nil || ptr.ActiveVersion != version {
			t.Fatalf("pointer=%+v err=%v", ptr, err)
		}
	}
}

func TestConcurrentActivationPublishesCurrentEntries(t *testing.T) {
	root := t.TempDir()
	first := windowsEntryRequest(t, root, "v1.38.9", "old")
	second := windowsEntryRequest(t, root, "v1.39.0", "new")
	firstLocked, releaseFirst, secondAcquiring := make(chan struct{}), make(chan struct{}), make(chan struct{})
	checks := 0
	first.CheckProcesses = func() error {
		checks++
		if checks == 1 {
			close(firstLocked)
			<-releaseFirst
		}
		return nil
	}
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- ActivateVersion(first) }()
	select {
	case <-firstLocked:
	case err := <-firstDone:
		t.Fatalf("first activation never reached publication: %v", err)
	}
	go func() {
		secondDone <- activateVersion(second, func(ctx context.Context, path string) (func(), error) {
			close(secondAcquiring)
			return filelock.Acquire(ctx, path)
		})
	}()
	select {
	case <-secondAcquiring:
	case err := <-secondDone:
		close(releaseFirst)
		<-firstDone
		t.Fatalf("second activation never acquired its lock: %v", err)
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	assertEntryBytes(t, root, "Deepzora.exe", "new-gui")
	assertEntryBytes(t, root, "deepzora-cli.exe", "new-cli")
	ptr, err := ReadCurrent(root)
	if err != nil || ptr.ActiveVersion != "v1.39.0" {
		t.Fatalf("pointer=%+v err=%v", ptr, err)
	}
}

func TestWindowsEntriesRollbackAllSelectedEntries(t *testing.T) {
	for _, failure := range []string{"process", "pointer", "root"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			if err := ActivateVersion(windowsEntryRequest(t, root, "v1.38.9", "old")); err != nil {
				t.Fatal(err)
			}
			req := windowsEntryRequest(t, root, "v1.39.0", "new")
			checks := 0
			switch failure {
			case "process":
				req.CheckProcesses = func() error {
					checks++
					if checks == 2 {
						return errors.New("process appeared")
					}
					return nil
				}
			case "pointer":
				if err := os.Remove(filepath.Join(root, CurrentFileName)); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, CurrentFileName), 0o755); err != nil {
					t.Fatal(err)
				}
			case "root":
				if err := os.Remove(filepath.Join(root, "deepzora-cli.exe")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "deepzora-cli.exe"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := ActivateVersion(req); err == nil {
				t.Fatal("activation should fail")
			}
			assertEntryBytes(t, root, "Deepzora.exe", "old-gui")
			if failure != "root" {
				assertEntryBytes(t, root, "deepzora-cli.exe", "old-cli")
			}
			if failure != "pointer" {
				ptr, err := ReadCurrent(root)
				if err != nil || ptr.ActiveVersion != "v1.38.9" {
					t.Fatalf("pointer=%+v err=%v", ptr, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "versions", "v1.39.0")); !os.IsNotExist(err) {
				t.Fatalf("uncommitted version survived: %v", err)
			}
		})
	}
}
