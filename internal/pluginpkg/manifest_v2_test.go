package pluginpkg

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestV2RequiresProvides(t *testing.T) {
	root := t.TempDir()
	writeV2Plugin(t, root, `{
  "apiVersion": "deepzora.com/plugin/v2",
  "name": "deps",
  "version": "2.0.0",
  "requires": [
    {
      "namespace": "deepzora",
      "kind": "provider",
      "id": "deepseek/v4",
      "versionRange": ">=1.0.0",
      "optional": false
    }
  ],
  "provides": [
    {
      "namespace": "plugin/deps",
      "kind": "provider",
      "id": "fake/x",
      "version": "1.0.0",
      "schemaHash": "sha256:abc"
    }
  ]
}`)
	pkg, _, err := ParseDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Manifest.Requires) != 1 || len(pkg.Manifest.Provides) != 1 {
		t.Fatalf("requires/provides = %d/%d", len(pkg.Manifest.Requires), len(pkg.Manifest.Provides))
	}
	if err := pkg.ProvidesCapabilities()[0].Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestManifestV2ProviderRequiresSchemaHash(t *testing.T) {
	root := t.TempDir()
	writeV2Plugin(t, root, `{
  "apiVersion": "deepzora.com/plugin/v2",
  "name": "bad",
  "provides": [
    {"namespace": "plugin/bad", "kind": "provider", "id": "x", "version": "1.0.0"}
  ]
}`)
	_, _, err := ParseDir(root)
	if err == nil || !strings.Contains(err.Error(), "schemaHash") {
		t.Fatalf("err = %v, want schemaHash", err)
	}
}

func TestManifestV2LoadsOnlyExplicitlyDeclaredResources(t *testing.T) {
	root := t.TempDir()
	writeV2Plugin(t, root, `{
  "apiVersion": "deepzora.com/plugin/v2",
  "name": "explicit-only",
  "contributes": {
    "skills": ["skills"],
    "commands": ["commands"],
    "agents": ["agents"]
  }
}`)
	writeTestFile(t, filepath.Join(root, "skills", "plan", "SKILL.md"), "---\ndescription: plan work\n---\nPlan carefully.")
	writeTestFile(t, filepath.Join(root, "commands", "spec.md"), "---\ndescription: write a spec\n---\nWrite a spec.")
	writeTestFile(t, filepath.Join(root, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: review changes\n---\nReview carefully.")

	// These Claude compatibility sidecars are present in many multi-host
	// packages, but a native v2 manifest is an explicit capability boundary.
	writeTestFile(t, filepath.Join(root, "CLAUDE.md"), "Repository maintenance instructions.")
	writeTestFile(t, filepath.Join(root, "hooks", "hooks.json"), `{
  "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "bin/start"}]}]}
}`)
	writeTestFile(t, filepath.Join(root, ".mcp.json"), `{
  "mcpServers": {"helper": {"command": "bin/helper"}}
}`)

	pkg, warnings, err := ParseDir(root)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	inv := pkg.Inventory()
	if len(inv.Skills) != 1 || len(inv.Commands) != 1 || len(inv.Agents) != 1 {
		t.Fatalf("inventory skills/commands/agents = %d/%d/%d, want 1/1/1", len(inv.Skills), len(inv.Commands), len(inv.Agents))
	}
	if len(pkg.Manifest.Hooks) != 0 {
		t.Fatalf("hooks = %+v, want only explicitly declared resources", pkg.Manifest.Hooks)
	}
	if len(pkg.Manifest.MCPServers) != 0 {
		t.Fatalf("MCP servers = %+v, want only explicitly declared resources", pkg.Manifest.MCPServers)
	}
}
