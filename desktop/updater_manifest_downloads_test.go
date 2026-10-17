package main

import (
	"strings"
	"testing"

	"deepzora/desktop/internal/update"
	"deepzora/internal/releaseassets"
)

func TestManualDebInstallHintUsesPublishedVersionedName(t *testing.T) {
	for _, version := range []string{"v0.0.1", "v1.2.3"} {
		want := "sudo apt install ./Deepzora-" + strings.TrimPrefix(version, "v") + "-Linux-x64.deb"
		if got := manualDebInstallHint(version); !strings.Contains(got, want) {
			t.Fatalf("manual install hint = %q, want %q", got, want)
		}
	}
}

func TestVersionedDesktopDownloads(t *testing.T) {
	m := validDesktopManifest(t, "stable", "v0.0.2")
	m.ArtifactNaming = "versioned-v1"
	for _, spec := range releaseassets.DesktopAssets() {
		name := releaseassets.DesktopName(m.Version, spec)
		url := desktopAssetBases("stable", m.Version)[0] + name
		m.Downloads[name] = update.Asset{URL: url, Sig: url + ".minisig", Size: 1024, SHA256: strings.Repeat("a", 64), InstallLayout: update.ElectronInstallLayout}
	}
	if err := validateDesktopManifest("stable", &m); err != nil {
		t.Fatal(err)
	}
	if a, ok := m.Asset(); ok && !strings.Contains(a.URL, "/Deepzora-0.0.2-") {
		t.Fatalf("new client selected updater route: %s", a.URL)
	}
	if a, ok := m.NativePackage(); ok && !strings.HasSuffix(a.URL, "/Deepzora-0.0.2-Linux-x64.deb") {
		t.Fatalf("native package was not renamed: %s", a.URL)
	}
	m.ArtifactNaming = ""
	if err := validateDesktopManifest("stable", &m); err == nil {
		t.Fatal("accepted missing artifact naming")
	}
	m.ArtifactNaming = "versioned-v1"
	name := "Deepzora-0.0.2-macOS-arm64.zip"
	a := m.Downloads[name]
	a.SHA256 = strings.Repeat("b", 64)
	m.Downloads[name] = a
	if err := validateDesktopManifest("stable", &m); err == nil {
		t.Fatal("accepted named asset with different bytes from updater route")
	}
	a.SHA256 = strings.Repeat("a", 64)
	m.Downloads[name] = a
	delete(m.Downloads, "Deepzora-0.0.2-Windows-arm64-Portable.zip")
	if err := validateDesktopManifest("stable", &m); err == nil {
		t.Fatal("accepted incomplete versioned download matrix")
	}
}

func TestDesktopManifestArchitectureDownloads(t *testing.T) {
	t.Run("historical two-download manifests are rejected", func(t *testing.T) {
		stable := validDesktopManifest(t, "stable", "v1.17.21")
		delete(stable.Downloads, "Deepzora-1.17.21-macOS-arm64.dmg")
		delete(stable.Downloads, "Deepzora-1.17.21-macOS-x64.dmg")
		if err := validateDesktopManifest("stable", &stable); err == nil {
			t.Fatal("historical manifest was accepted")
		}
	})
	t.Run("partial architecture DMGs fail closed", func(t *testing.T) {
		manifest := validDesktopManifest(t, "stable", "v1.39.0")
		delete(manifest.Downloads, "Deepzora-1.39.0-macOS-x64.dmg")
		if err := validateDesktopManifest("stable", &manifest); err == nil {
			t.Fatal("manifest with one architecture DMG passed validation")
		}
	})
}
