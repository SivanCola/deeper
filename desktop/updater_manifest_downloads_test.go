package main

import "testing"

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
