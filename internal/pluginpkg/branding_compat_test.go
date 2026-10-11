package pluginpkg

import (
	"path/filepath"
	"testing"
)

func TestDeeperPluginV2CompatibilityKeepsStrictVersionAndPrecedence(t *testing.T) {
	for _, version := range []string{"deeper.io/plugin/v2", "deeper.io/plugin/v2.1"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, LegacyNativeManifest), jsonText(map[string]any{
				"apiVersion": version, "name": "legacy-plugin",
				"runtime": map[string]any{"command": "${DEEPER_PLUGIN_ROOT}/bin/plugin"},
			}))
			pkg, _, err := ParseDir(root)
			if version == "deeper.io/plugin/v2" {
				if err != nil || pkg.Manifest.Name != "legacy-plugin" {
					t.Fatalf("legacy v2 rejected: %v", err)
				}
				if !pkg.Manifest.LegacyIdentity || ExpandRuntimeCommand(pkg.Manifest.Runtime.Command, root) != filepath.Join(root, "bin", "plugin") {
					t.Fatal("legacy runtime identity or executable expansion lost")
				}
			} else if err == nil {
				t.Fatal("unknown legacy revision accepted")
			}
			writeTestFile(t, filepath.Join(root, NativeManifest), "{invalid")
			if _, _, err := ParseDir(root); err == nil {
				t.Fatal("invalid preferred manifest fell back to legacy")
			}
		})
	}
}
