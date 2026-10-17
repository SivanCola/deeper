package main

import (
	"fmt"

	"deepzora/desktop/internal/update"
	"deepzora/internal/releaseassets"
)

type requiredDesktopAsset struct{ group, key, filename string }

func requiredDesktopAssets(version string) []requiredDesktopAsset {
	var out []requiredDesktopAsset
	for _, spec := range releaseassets.DesktopAssets() {
		name := releaseassets.DesktopName(version, spec)
		out = append(out, requiredDesktopAsset{group: "downloads", key: name, filename: name})
		if spec.Channel != "" {
			out = append(out, requiredDesktopAsset{group: spec.Channel, key: spec.Platform, filename: name})
		}
	}
	return out
}

func validateNamedDesktopAssets(_ string, m *update.Manifest) error {
	if m.ArtifactNaming != "versioned-v1" {
		return fmt.Errorf("unsupported artifact_naming %q", m.ArtifactNaming)
	}
	expected := map[string]int{"downloads": len(releaseassets.DesktopAssets())}
	for _, spec := range releaseassets.DesktopAssets() {
		if spec.Channel == "" {
			continue
		}
		expected[spec.Channel]++
		routed := m.Platforms[spec.Platform]
		if spec.Channel == "native_packages" {
			routed = m.NativePackages[spec.Platform]
		}
		if routed != m.Downloads[releaseassets.DesktopName(m.Version, spec)] {
			return fmt.Errorf("updater route differs from download: %s", spec.Platform)
		}
	}
	for group, assets := range map[string]map[string]update.Asset{"downloads": m.Downloads, "platforms": m.Platforms, "native_packages": m.NativePackages} {
		if len(assets) != expected[group] {
			return fmt.Errorf("unexpected %s asset matrix", group)
		}
	}
	return nil
}
