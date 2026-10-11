//go:build windows

package desktoplauncher

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestResolveInstallRootThroughDirectoryJunction(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "deepzora-launcher.exe")
	if err := os.WriteFile(launcher, []byte("launcher"), 0o755); err != nil {
		t.Fatal(err)
	}

	junction := filepath.Join(t.TempDir(), "current")
	output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, root).CombinedOutput()
	if err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}

	got, err := resolveInstallRoot(filepath.Join(junction, "deepzora-launcher.exe"))
	if err != nil {
		t.Fatal(err)
	}
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	wantInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("resolveInstallRoot() = %q, want %q", got, root)
	}
	location, err := classifyLaunchLocation(got)
	if err != nil {
		t.Fatal(err)
	}
	if location != launchLocationLocal {
		t.Fatalf("junction target location = %q, want local", location)
	}
}

func TestNormalizeFinalWindowsPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "drive", path: `\\?\C:\Apps\Deepzora`, want: `C:\Apps\Deepzora`},
		{name: "UNC", path: `\\?\UNC\server\share\Deepzora`, want: `\\server\share\Deepzora`},
		{name: "volume GUID", path: `\\?\Volume{abc}\Deepzora`, want: `\\?\Volume{abc}\Deepzora`},
		{name: "ordinary", path: `C:\Apps\Deepzora`, want: `C:\Apps\Deepzora`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeFinalWindowsPath(test.path); got != test.want {
				t.Fatalf("normalizeFinalWindowsPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestClassifyLaunchLocation(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		driveType uint32
		want      launchLocation
		wantErr   bool
		wantQuery bool
	}{
		{name: "Parallels UNC", path: `\\psf\Home\Desktop\Deepzora`, want: launchLocationUNC},
		{name: "ordinary UNC", path: `\\server\share\Deepzora`, want: launchLocationUNC},
		{name: "mapped remote drive", path: `Z:\Deepzora`, driveType: windows.DRIVE_REMOTE, want: launchLocationRemoteDrive, wantQuery: true},
		{name: "local fixed drive", path: `C:\Deepzora`, driveType: windows.DRIVE_FIXED, want: launchLocationLocal, wantQuery: true},
		{name: "local removable drive", path: `E:\Deepzora`, driveType: windows.DRIVE_REMOVABLE, want: launchLocationLocal, wantQuery: true},
		{name: "unknown drive", path: `Q:\Deepzora`, driveType: windows.DRIVE_UNKNOWN, wantErr: true, wantQuery: true},
		{name: "missing volume", path: `Deepzora`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queried := false
			got, err := classifyLaunchLocationWith(test.path, func(*uint16) uint32 {
				queried = true
				return test.driveType
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("location = %q, want %q", got, test.want)
			}
			if queried != test.wantQuery {
				t.Errorf("GetDriveType queried = %v, want %v", queried, test.wantQuery)
			}
		})
	}
}
