package main

import (
	"fmt"

	"deepzora/desktop/internal/update"
	"deepzora/internal/releaseassets"
)

func validateNamedDesktopAssets(selected string, m *update.Manifest) error {
	if m.ArtifactNaming == "" {
		return nil
	}
	if m.ArtifactNaming != "versioned-v1" || m.Version == "v0.0.1" {
		return fmt.Errorf("unsupported artifact_naming %q for %s", m.ArtifactNaming, m.Version)
	}
	for _, spec := range releaseassets.DesktopAssets() {
		name := releaseassets.DesktopName(m.Version, spec)
		asset, ok := m.Downloads[name]
		if !ok {
			return fmt.Errorf("missing versioned download %s", name)
		}
		if _, err := validateManifestAsset(selected, m.Version, name, asset); err != nil {
			return err
		}
		// A compatibility alias must contain the very same signed payload.
		legacy := m.Downloads[spec.Legacy]
		for _, group := range []map[string]update.Asset{m.Platforms, m.NativePackages} {
			for _, item := range group {
				if item.URL == desktopAssetBases(selected, m.Version)[0]+spec.Legacy {
					legacy = item
				}
			}
		}
		if legacy.URL != "" && (legacy.Size != asset.Size || legacy.SHA256 != asset.SHA256 || legacy.InstallLayout != asset.InstallLayout) {
			return fmt.Errorf("versioned download differs from compatibility alias: %s", name)
		}
	}
	return nil
}

type requiredDesktopAsset struct {
	group    string
	key      string
	filename string
}

var (
	requiredDesktopUpdaterAssets = []requiredDesktopAsset{
		{group: "platforms", key: "darwin-arm64", filename: "Deepzora-darwin-arm64.zip"},
		{group: "platforms", key: "darwin-amd64", filename: "Deepzora-darwin-amd64.zip"},
		{group: "platforms", key: "windows-amd64", filename: "Deepzora-windows-amd64-installer.exe"},
		{group: "platforms", key: "windows-arm64", filename: "Deepzora-windows-arm64-installer.exe"},
		{group: "platforms", key: "linux-amd64", filename: "Deepzora-linux-amd64.tar.gz"},
		{group: "native_packages", key: "linux-amd64", filename: "Deepzora-linux-amd64.deb"},
	}
	requiredDesktopDownloadAssets = []requiredDesktopAsset{
		{group: "downloads", key: "Deepzora-darwin-universal.dmg", filename: "Deepzora-darwin-universal.dmg"},
		{group: "downloads", key: "Deepzora-windows-amd64.zip", filename: "Deepzora-windows-amd64.zip"},
		{group: "downloads", key: "Deepzora-darwin-arm64.dmg", filename: "Deepzora-darwin-arm64.dmg"},
		{group: "downloads", key: "Deepzora-darwin-amd64.dmg", filename: "Deepzora-darwin-amd64.dmg"},
	}
)
