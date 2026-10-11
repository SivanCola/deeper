package appidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShortcutRepairPolicy(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "deepzora-launcher.exe")
	if err := os.WriteFile(launcher, []byte("launcher"), 0o600); err != nil {
		t.Fatal(err)
	}
	electron := filepath.Join(root, "versions", "v1.38.6", "app", "Deepzora.exe")
	goDesktop := filepath.Join(root, "versions", "v1.20.0", "deepzora-desktop.exe")
	flat := filepath.Join(root, "deepzora-desktop.exe")
	customIcon := filepath.Join(root, "custom.ico")
	for _, tt := range []struct {
		name, target, icon, id string
		want                   shortcutRepair
	}{
		{"new pin", launcher, "", "", shortcutRepair{identity: AppUserModelID}},
		{"legacy pin", launcher, "", "Deepzora", shortcutRepair{identity: AppUserModelID}},
		{"healthy", launcher, launcher, AppUserModelID, shortcutRepair{}},
		{"versioned electron", electron, electron, "Deepzora", shortcutRepair{target: launcher, icon: launcher, identity: AppUserModelID}},
		{"versioned Go", goDesktop, goDesktop, "", shortcutRepair{target: launcher, icon: launcher, identity: AppUserModelID}},
		{"flat electron", filepath.Join(root, "app", "Deepzora.exe"), "", "Deepzora", shortcutRepair{target: launcher, identity: AppUserModelID}},
		{"custom icon", electron, customIcon, "", shortcutRepair{target: launcher, identity: AppUserModelID}},
		{"missing flat", flat, flat, "", shortcutRepair{target: launcher, icon: launcher, identity: AppUserModelID}},
		{"current ID stale target", electron, launcher, AppUserModelID, shortcutRepair{target: launcher}},
		{"stable target stale icon", launcher, electron, AppUserModelID, shortcutRepair{icon: launcher}},
		{"studio ID", electron, electron, "com.deepzora.desktop.studio", shortcutRepair{}},
		{"Tauri ID", electron, electron, "com.deepzora.desktop.dev", shortcutRepair{}},
		{"unknown ID", electron, electron, "Custom.App", shortcutRepair{}},
		{"other install", filepath.Join(t.TempDir(), "Deepzora.exe"), electron, "Deepzora", shortcutRepair{}},
		{"Studio executable", filepath.Join(root, "Deepzora Studio.exe"), electron, "Deepzora", shortcutRepair{}},
		{"unrelated executable", filepath.Join(root, "other.exe"), electron, "", shortcutRepair{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := planShortcutRepair(tt.target, tt.icon, tt.id, root, false); got != tt.want {
				t.Fatalf("repair = %+v, want %+v", got, tt.want)
			}
		})
	}
	if err := os.WriteFile(flat, []byte("flat desktop"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := planShortcutRepair(flat, flat, AppUserModelID, root, false); got != (shortcutRepair{}) {
		t.Fatalf("live flat install must keep its entry: %+v", got)
	}
	if got := planShortcutRepair(flat, flat, AppUserModelID, root, true); got != (shortcutRepair{target: launcher, icon: launcher}) {
		t.Fatalf("activated versioned install must replace stale flat entry: %+v", got)
	}
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	if got := planShortcutRepair(electron, electron, "Deepzora", root, false); got != (shortcutRepair{identity: AppUserModelID}) {
		t.Fatalf("missing launcher must not create a dangling entry: %+v", got)
	}
}

func TestShortcutOwnershipResolvesLinksBeforeCheckingLayout(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "versions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "versions", "v1.0.0")); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	for _, suffix := range []string{"deepzora-desktop.exe", filepath.Join("app", "Deepzora.exe")} {
		if ownedShortcutTarget(filepath.Join(root, "versions", "v1.0.0", suffix), root) {
			t.Fatalf("external target %s treated as owned", suffix)
		}
	}
	alias := filepath.Join(t.TempDir(), "current")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if !ownedShortcutTarget(filepath.Join(alias, "versions", "v2.0.0", "app", "Deepzora.exe"), root) {
		t.Fatal("alias of the owned root with a pruned version must remain repairable")
	}
	if ownedShortcutTarget(filepath.Join(root, "Deepzora.exe"), filepath.Join(root, "missing-root")) {
		t.Fatal("missing installation root must not establish ownership")
	}
}

func TestCanonicalShortcutMigratesLegacyButPreservesCustomProperties(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "Deepzora.exe")
	legacy := filepath.Join(root, "deepzora-launcher.exe")
	for _, path := range []string{canonical, legacy} {
		if err := os.WriteFile(path, []byte("launcher"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, target, icon, id string
		want                   shortcutRepair
	}{
		{"legacy stable", legacy, legacy, AppUserModelID, shortcutRepair{target: canonical, icon: canonical}},
		{"custom icon", legacy, filepath.Join(root, "custom.ico"), AppUserModelID, shortcutRepair{target: canonical}},
		{"canonical", canonical, canonical, AppUserModelID, shortcutRepair{}},
		{"foreign identity", legacy, legacy, "com.deepzora.desktop.studio", shortcutRepair{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := planShortcutRepair(tc.target, tc.icon, tc.id, root, true); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	oldTarget := filepath.Join(root, "versions", "v1.38.9", "app", "Deepzora.exe")
	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{"", true}, {filepath.Dir(oldTarget), true}, {root, false}, {t.TempDir(), false}, {filepath.Join(root, "workspace"), false},
	} {
		if got := repairShortcutWorkingDirectory(tc.dir, oldTarget, root); got != tc.want {
			t.Fatalf("directory %q: got %v, want %v", tc.dir, got, tc.want)
		}
	}
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if got := planShortcutRepair(legacy, legacy, AppUserModelID, root, true); got != (shortcutRepair{}) {
		t.Fatalf("missing canonical entry broke legacy shortcut: %+v", got)
	}
}
