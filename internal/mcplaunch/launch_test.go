package mcplaunch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProjectLaunchIdentityDigestCanonicalizesStableIdentity(t *testing.T) {
	base := ProjectLaunchIdentity{
		Server: "reader", Transport: "stdio", CommandPath: "/bin/tool",
		CommandSHA256: "abc", Args: []string{"--serve"},
		EnvKeys: []string{"TOKEN", "PATH"}, HeaderKeys: []string{"Authorization", "X-Tenant"},
	}
	reordered := base
	reordered.EnvKeys = []string{"PATH", "TOKEN"}
	reordered.HeaderKeys = []string{"X-Tenant", "Authorization"}
	a, err := ProjectLaunchIdentityDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	normalized := normalizeIdentity(base, runtime.GOOS == "windows")
	payload := struct {
		Server, Transport, CommandPath, CommandSHA256, Dir, URL string
		Args, EnvKeys, HeaderKeys                               []string
		LauncherDigest                                          string
	}{
		normalized.Server, normalized.Transport, normalized.CommandPath, normalized.CommandSHA256,
		normalized.Dir, normalized.URL, normalized.Args, normalized.EnvKeys,
		normalized.HeaderKeys, normalized.LauncherDigest,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if want := digestBytes(body); a != want {
		t.Fatalf("project launch identity digest = %q, want %q", a, want)
	}
	b, err := ProjectLaunchIdentityDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("equivalent key ordering changed the project launch identity digest")
	}
	changedCommand := base
	changedCommand.Args = []string{"--serve", "--network"}
	c, err := ProjectLaunchIdentityDigest(changedCommand)
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("command arguments did not change the server identity")
	}
}

func TestLaunchGrantIsExactDurableAndWorkspaceScoped(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFilename)
	a := NewManager(path, "/workspace/a")
	if err := a.Authorize("project", "project_config", "identity-a"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"identity_fingerprint": "identity-a"`) || strings.Contains(string(body), `"identity_digest"`) {
		t.Fatalf("launch grant JSON compatibility changed: %s", body)
	}
	if authorized, changed, err := a.LaunchAuthorized("project", "project_config", "identity-a"); err != nil || !authorized || changed {
		t.Fatalf("exact grant = (%v,%v,%v)", authorized, changed, err)
	}
	if authorized, changed, err := a.LaunchAuthorized("project", "project_config", "identity-b"); err != nil || authorized || !changed {
		t.Fatalf("changed identity = (%v,%v,%v)", authorized, changed, err)
	}
	b := NewManager(path, "/workspace/b")
	if authorized, changed, err := b.LaunchAuthorized("project", "project_config", "identity-a"); err != nil || authorized || changed {
		t.Fatalf("other workspace grant = (%v,%v,%v)", authorized, changed, err)
	}
	reloaded := NewManager(path, "/workspace/a")
	if authorized, changed, err := reloaded.LaunchAuthorized("project", "project_config", "identity-a"); err != nil || !authorized || changed {
		t.Fatalf("reloaded grant = (%v,%v,%v)", authorized, changed, err)
	}
}

func TestRevokeOnlyRemovesCurrentWorkspaceServerGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFilename)
	a := NewManager(path, "/workspace/a")
	b := NewManager(path, "/workspace/b")
	for _, item := range []struct {
		manager *Manager
		server  string
	}{
		{a, "one"}, {a, "two"}, {b, "one"},
	} {
		if err := item.manager.Authorize(item.server, "project_config", "identity"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Revoke("one"); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := a.LaunchAuthorized("one", "project_config", "identity"); ok {
		t.Fatal("revoked current-workspace grant remains authorized")
	}
	if ok, _, _ := a.LaunchAuthorized("two", "project_config", "identity"); !ok {
		t.Fatal("unrelated server grant was revoked")
	}
	if ok, _, _ := b.LaunchAuthorized("one", "project_config", "identity"); !ok {
		t.Fatal("other workspace grant was revoked")
	}
}

func TestLauncherLockRoundTrip(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), StateFilename), "/workspace")
	lock := LauncherLock{Server: "project", Locator: "pkg@latest", ResolvedVersion: "1.2.3", ContentSHA256: "abc"}
	if err := manager.PutLauncherLock(lock); err != nil {
		t.Fatal(err)
	}
	got, found, err := manager.GetLauncherLock("project", "pkg@latest")
	if err != nil || !found {
		t.Fatalf("launcher lock = (%+v,%v,%v)", got, found, err)
	}
	if got.ResolvedVersion != lock.ResolvedVersion || got.ContentSHA256 != lock.ContentSHA256 {
		t.Fatalf("launcher lock = %+v", got)
	}
	if LauncherLockFingerprint(got) == "" {
		t.Fatal("empty launcher lock fingerprint")
	}
}

func TestStateRequiresCurrentVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFilename)
	if err := os.WriteFile(path, []byte(`{"launch_grants":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(path, "/workspace").Load(); err == nil || !strings.Contains(err.Error(), "unsupported MCP launch authorization state version 0") {
		t.Fatalf("unversioned state error = %v", err)
	}
}

func TestStateFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFilename)
	manager := NewManager(path, "/workspace")
	if err := manager.Authorize("project", "project_config", "identity"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state permissions = %o, want owner-only", info.Mode().Perm())
	}
}
