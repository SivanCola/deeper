//go:build windows

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"deepzora/internal/winaclresidue"
	"golang.org/x/sys/windows"
)

// An explicit save can recover an unreadable current credential store while
// preserving its values. Ordinary reads must not modify its ACL.
func TestCredentialSaveResetsDeniedCurrentStore(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	t.Setenv("ACL_RESET_INTEGRATION_KEY", "")
	path := UserCredentialsPath()
	if err := os.WriteFile(path, []byte("EXISTING_KEY=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	// Install a protected descriptor through the native API so the fixture
	// doesn't depend on icacls argument parsing or inherited profile grants.
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;FR;;;" + user.User.Sid.String() + ")(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(sd)
	t.Cleanup(func() {
		if err := winaclresidue.ResetCredentialDACL(path); err != nil {
			t.Errorf("restore credential ACL: %v", err)
		}
	})
	if _, err := os.ReadFile(path); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("denied credential read = %v, want permission error", err)
	}
	if _, err := readCredentialFile(path); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("ordinary credential read = %v, want unchanged permission error", err)
	}
	if revision := CredentialStoreRevision(); revision != "unreadable" {
		t.Fatalf("denied credential revision = %q, want unreadable", revision)
	}
	if _, err := SetCredential("ACL_RESET_INTEGRATION_KEY", "new"); err != nil {
		t.Fatalf("SetCredential with a denied current store: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read reset credential file: %v", err)
	}
	if got := string(data); !strings.Contains(got, "EXISTING_KEY=old") || !strings.Contains(got, "ACL_RESET_INTEGRATION_KEY=new") {
		t.Fatalf("credential contents after reset = %q", got)
	}
	if quarantined, _ := filepath.Glob(path + ".locked-*"); len(quarantined) != 0 {
		t.Fatalf("reset path must not quarantine the store: %v", quarantined)
	}
}
