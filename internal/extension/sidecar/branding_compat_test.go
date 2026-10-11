package sidecar

import (
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/extension/protocol"
	"deepzora/internal/pluginpkg"
)

func TestLegacyRuntimeIdentityCannotBeOverriddenByManifest(t *testing.T) {
	root := t.TempDir()
	command, err := resolveRuntimeCommand(&pluginpkg.RuntimeSpec{Command: "${DEEPER_PLUGIN_ROOT}/bin/plugin"}, root)
	if err != nil || command != filepath.Join(root, "bin", "plugin") {
		t.Fatalf("legacy command: %q %v", command, err)
	}
	for _, legacy := range []bool{true, false} {
		pkg := pluginpkg.Package{Root: root, Manifest: pluginpkg.Manifest{LegacyIdentity: legacy}}
		rt := &pluginpkg.RuntimeSpec{Env: map[string]string{"DEEPER_PLUGIN_ROOT": "untrusted", "DEEPER_PLUGIN_ID": "wrong", "DEEPER_PLUGIN_VERSION": "wrong"}}
		client := &Client{rt: rt, legacyIdentity: legacy}
		identity := protocol.ProtocolID
		if legacy {
			identity = "deeper.extension.v2"
		}
		if params := client.initializeParams(); params.ProtocolID != identity || params.ProtocolVersion != protocol.ProtocolVersion {
			t.Fatal("legacy handshake changed protocol version or identity")
		}
		env := map[string]string{}
		for _, entry := range runtimeEnv(rt, pkg, pluginpkg.InstalledPlugin{Name: "plugin", Version: "1.2.3"}) {
			key, value, _ := strings.Cut(entry, "=")
			env[key] = value
		}
		if env[envPluginRoot] != root || env[envPluginName] != "plugin" {
			t.Fatal("new identity lost")
		}
		if legacy && (env["DEEPER_PLUGIN_ROOT"] != root || env["DEEPER_PLUGIN_ID"] != "plugin" || env["DEEPER_PLUGIN_VERSION"] != "1.2.3") {
			t.Fatal("legacy identity was forged")
		}
		if !legacy && env["DEEPER_PLUGIN_ROOT"] != "untrusted" {
			t.Fatal("legacy aliases injected into current plugin")
		}
	}
}
