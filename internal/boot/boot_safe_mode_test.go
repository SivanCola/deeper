package boot

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/event"
	"deepzora/internal/plugin"
)

// v1.20+ removes product Safe Mode. DEEPZORA_SAFE_MODE no longer changes boot
// behavior; these tests pin that the normal tool/plugin surface stays available.

func TestBuildIgnoresSafeModeEnvForTools(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	t.Setenv("DEEPZORA_SAFE_MODE", "1")

	ctrl, err := Build(context.Background(), Options{
		SessionDir: filepath.Join(t.TempDir(), "sessions"),
		Sink:       event.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range ctrl.AllToolContractEntries() {
		names[e.Name] = true
	}
	ctrl.Close()
	for _, name := range []string{"install_source", "run_skill", "read_skill", "use_capability"} {
		if !names[name] {
			t.Fatalf("DEEPZORA_SAFE_MODE must not strip %s from the capability registry", name)
		}
	}
}

func TestBuildNormalModeKeepsSourceConnectorAndSkillTools(t *testing.T) {
	isolateConfigHome(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	t.Setenv("DEEPZORA_SAFE_MODE", "")

	for _, tokenMode := range []string{"standard", "standard"} {
		ctrl, err := Build(context.Background(), Options{
			SessionDir: filepath.Join(t.TempDir(), "sessions"),
			Sink:       event.Discard,
		})
		if err != nil {
			t.Fatalf("Build(%q): %v", tokenMode, err)
		}
		names := map[string]bool{}
		for _, e := range ctrl.AllToolContractEntries() {
			names[e.Name] = true
		}
		visible := map[string]bool{}
		for _, e := range ctrl.ToolContractEntries() {
			visible[e.Name] = true
		}
		ctrl.Close()
		// Full registry always has skill/install tools + use_capability.
		for _, name := range []string{"install_source", "run_skill", "read_skill", "use_capability"} {
			if !names[name] {
				t.Fatalf("normal mode (%q) must register %s in the capability registry", tokenMode, name)
			}
		}
		// Provider-visible surface is the unified core for every role setting.
		if !visible["use_capability"] {
			t.Fatalf("normal mode (%q) must expose use_capability", tokenMode)
		}
		if visible["connect_tool_source"] {
			t.Fatalf("normal mode (%q) must not expose connect_tool_source", tokenMode)
		}
	}
}

func TestBuildStillSpawnsExtraPluginsWhenSafeModeEnvSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sentinel spec uses /bin/sh")
	}
	isolateConfigHome(t)
	workspace := robustTempDir(t)
	t.Chdir(workspace)
	t.Setenv("DEEPZORA_SAFE_MODE", "1")
	marker := filepath.Join(plugin.MCPStateDir(config.DeepzoraHomeDir(), workspace, "acp-extra"), "started")
	ctrl, err := Build(context.Background(), Options{
		SessionDir: filepath.Join(t.TempDir(), "sessions"),
		Sink:       event.Discard,
		ExtraPlugins: []plugin.Spec{{
			Name:    "acp-extra",
			Command: "/bin/sh",
			Args:    []string{"-c", "echo started > '" + marker + "'"},
		}},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ctrl.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("host-supplied MCP server never spawned; DEEPZORA_SAFE_MODE must not drop ExtraPlugins")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
