package pluginpkg

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestParseClaudePluginDoesNotRegisterCodexSessionStartHook pins the security
// boundary of the includeCodexSessionStartHook flag: a claude-kind package
// shipping a hooks/session-start-codex file must NOT get it registered as an
// executable SessionStart hook (that convention belongs to codex manifests).
func TestParseClaudePluginDoesNotRegisterCodexSessionStartHook(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ClaudeManifest), `{"name": "sneaky-pack"}`)
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\ndescription: s\n---\nbody")
	writeTestFile(t, filepath.Join(root, "hooks", "session-start-codex"), "#!/bin/sh\necho pwned\n")

	pkg, _, err := ParseDir(root)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	for _, h := range pkg.Manifest.Hooks["SessionStart"] {
		if h.Command != "" {
			t.Fatalf("claude package registered executable SessionStart hook: %+v", h)
		}
	}
}

// TestParseCodexManifestNotAffectedByClaudeFallback: the convention-dir
// fallback is claude-only; a codex manifest without a skills field keeps its
// existing "no skills" behavior even when a skills/ directory exists.
func TestParseCodexManifestNotAffectedByClaudeFallback(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, CodexManifest), `{"name": "codex-pack"}`)
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\ndescription: s\n---\nbody")

	pkg, _, err := ParseDir(root)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if pkg.ManifestKind != "codex" {
		t.Fatalf("kind = %q", pkg.ManifestKind)
	}
	if got := pkg.SkillRoots(); len(got) != 0 {
		t.Fatalf("SkillRoots = %#v, codex parsing must not adopt convention dirs", got)
	}
}

// TestParseClaudePluginAdoptsNestedCommands pins that namespace layouts like
// commands/git/commit.md — which the runtime loader walks — also gate command
// root adoption, and surface in the inventory under their namespaced name.
func TestParseClaudePluginAdoptsNestedCommands(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ClaudeManifest), `{"name": "nested-pack"}`)
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\ndescription: s\n---\nbody")
	writeTestFile(t, filepath.Join(root, "commands", "git", "commit.md"), "---\ndescription: commit helper\n---\nCommit: $ARGUMENTS")

	pkg, warnings, err := ParseDir(root)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if got := pkg.CommandRoots(); len(got) != 1 || got[0] != filepath.Join(root, "commands") {
		t.Fatalf("CommandRoots = %#v, want commands adopted for nested-only layout", got)
	}
	inv := pkg.Inventory()
	if len(inv.Commands) != 1 || inv.Commands[0].Name != "git:commit" || inv.Commands[0].Invocation != "/git:commit" {
		t.Fatalf("inventory commands = %#v, want namespaced git:commit", inv.Commands)
	}
}

// TestInventoryTextCommandsOnly pins that a commands-only inventory does not
// also claim "no detailed inventory available".
func TestInventoryTextCommandsOnly(t *testing.T) {
	var b strings.Builder
	appendInventoryText(&b, "superpowers", Inventory{Commands: []CommandRef{{Name: "plan", Invocation: "/plan", Description: "plan things"}}})
	out := b.String()
	if !strings.Contains(out, "commands:") || !strings.Contains(out, "/superpowers:plan") {
		t.Fatalf("output = %q, want the commands listing", out)
	}
	if strings.Contains(out, "no detailed inventory available") {
		t.Fatalf("output = %q, must not claim an empty inventory after listing commands", out)
	}
}

// TestParseClaudePluginAdoptsDeeplyNestedCommands pins that adoption gating
// shares the runtime loader's discovery semantics with no depth ceiling: a
// plugin whose only command sits six levels deep is still adopted.
func TestParseClaudePluginAdoptsDeeplyNestedCommands(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ClaudeManifest), `{"name": "deep-pack"}`)
	writeTestFile(t, filepath.Join(root, "skills", "s", "SKILL.md"), "---\ndescription: s\n---\nbody")
	writeTestFile(t, filepath.Join(root, "commands", "a", "b", "c", "d", "e", "commit.md"), "---\ndescription: deep commit\n---\nCommit")
	pkg, _, err := ParseDir(root)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if got := pkg.CommandRoots(); len(got) != 1 || got[0] != filepath.Join(root, "commands") {
		t.Fatalf("CommandRoots = %#v, want commands adopted for the deeply nested layout", got)
	}
	inv := pkg.Inventory()
	if len(inv.Commands) != 1 || inv.Commands[0].Name != "a:b:c:d:e:commit" {
		t.Fatalf("inventory commands = %#v, want the namespaced deep command", inv.Commands)
	}
}
