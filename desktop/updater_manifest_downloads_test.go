package main

import (
	"strings"
	"testing"

	"deepzora/desktop/internal/update"
	"deepzora/internal/releaseassets"
)

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
		t.Fatalf("new client selected compatibility alias: %s", a.URL)
	}
	if a, ok := m.NativePackage(); ok && !strings.HasSuffix(a.URL, "/Deepzora-0.0.2-Linux-x64.deb") {
		t.Fatalf("native package was not renamed: %s", a.URL)
	}
	// Emulate the v0.0.1 decoder, which ignores the new naming field and only
	// validates the original routes. Additional downloads do not break it.
	m.ArtifactNaming = ""
	if err := validateDesktopManifest("stable", &m); err != nil {
		t.Fatalf("legacy client cannot validate new manifest: %v", err)
	}
	m.ArtifactNaming = "versioned-v1"
	name := "Deepzora-0.0.2-macOS-arm64.zip"
	a := m.Downloads[name]
	a.SHA256 = strings.Repeat("b", 64)
	m.Downloads[name] = a
	if err := validateDesktopManifest("stable", &m); err == nil {
		t.Fatal("accepted named asset with different bytes from compatibility alias")
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
		delete(stable.Downloads, "Deepzora-darwin-arm64.dmg")
		delete(stable.Downloads, "Deepzora-darwin-amd64.dmg")
		if err := validateDesktopManifest("stable", &stable); err == nil {
			t.Fatal("historical manifest was accepted")
		}
	})
	t.Run("partial architecture DMGs fail closed", func(t *testing.T) {
		manifest := validDesktopManifest(t, "stable", "v1.39.0")
		delete(manifest.Downloads, "Deepzora-darwin-amd64.dmg")
		if err := validateDesktopManifest("stable", &manifest); err == nil {
			t.Fatal("manifest with one architecture DMG passed validation")
		}
	})
}
