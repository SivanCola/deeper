package appidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentShortcutIdentityAndOwnership(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "Deepzora.exe")
	if err := os.WriteFile(current, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := planShortcutRepair(current, "", root); got.identity != AppUserModelID {
		t.Fatalf("new shortcut: %+v", got)
	}
	for _, id := range []string{AppUserModelID, "Deepzora", "com.deepzora.desktop.dev", "Custom.App"} {
		if got := planShortcutRepair(current, id, root); got != (shortcutRepair{}) {
			t.Fatalf("rewrote existing identity %q: %+v", id, got)
		}
	}
	for _, path := range []string{filepath.Join(root, "deepzora-launcher.exe"), filepath.Join(root, "versions", "v1", "app", "Deepzora.exe"), filepath.Join(t.TempDir(), "Deepzora.exe")} {
		if got := planShortcutRepair(path, "", root); got != (shortcutRepair{}) {
			t.Fatalf("rewrote noncurrent target %q: %+v", path, got)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if ownedShortcutTarget(filepath.Join(root, "link", "Deepzora.exe"), root) {
		t.Fatal("external linked target treated as owned")
	}
}
