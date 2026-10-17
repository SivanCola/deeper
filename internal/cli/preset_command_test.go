package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
)

func TestPresetCommandScopedCRUD(t *testing.T) {
	isolateCLIConfigHome(t)
	root := t.TempDir()
	t.Chdir(root)
	run := func(args ...string) string {
		t.Helper()
		var rc int
		out := captureStdout(t, func() { rc = presetCommand(args) })
		if rc != 0 {
			t.Fatalf("preset %v exit = %d", args, rc)
		}
		return out
	}
	run("new", "review", "--prompt", "User persona")
	user, err := resolveCommandPreset(root, "user", "review")
	if err != nil || user.Body != "User persona" || user.Tools != nil || user.Complete || !user.IncludeRuntimeContext {
		t.Fatalf("new user preset = %+v, %v", user, err)
	}
	run("new", "review", "--scope", "project", "--prompt", "Project persona", "--tools", "[]")
	project, err := resolveCommandPreset(root, "", "review")
	if err != nil || project.Scope != agentpreset.ScopeProject || project.Tools == nil || len(project.Tools) != 0 {
		t.Fatalf("project override = %+v, %v", project, err)
	}
	run("edit", "review", "--description", "Local review")
	project, _ = resolveCommandPreset(root, "project", "review")
	if project.Tools == nil || len(project.Tools) != 0 || project.Body != "Project persona" {
		t.Fatalf("edit changed omitted fields: %+v", project)
	}
	run("copy", "review", "review-copy")
	copied, err := resolveCommandPreset(root, "user", "review-copy")
	if err != nil || copied.Body != project.Body || copied.Tools == nil || copied.Name != "review-copy" {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
	run("edit", "review-copy", "--tools", "all")
	copied, _ = resolveCommandPreset(root, "user", "review-copy")
	if copied.Tools != nil {
		t.Fatalf("unrestricted tools = %#v", copied.Tools)
	}
	var shown agentpreset.Preset
	if err := json.Unmarshal([]byte(run("show", "review", "--scope", "user", "--json")), &shown); err != nil || shown.Body != user.Body {
		t.Fatalf("show user preset = %+v, %v", shown, err)
	}
	run("rm", "review")
	visible, err := resolveCommandPreset(root, "", "review")
	if err != nil || visible.Scope != agentpreset.ScopeUser {
		t.Fatalf("removal did not reveal user preset: %+v, %v", visible, err)
	}
	run("rm", "review", "--scope", "user")
	if _, err := resolveCommandPreset(root, "", "review"); err == nil {
		t.Fatal("removed user preset still resolves")
	}
}

func TestPresetCommandPromptFilesAndValidation(t *testing.T) {
	isolateCLIConfigHome(t)
	root := t.TempDir()
	t.Chdir(root)
	promptPath := filepath.Join(root, "persona.txt")
	if err := os.WriteFile(promptPath, []byte("From file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rc int
	captureStdout(t, func() {
		rc = presetCommand([]string{"new", "writer", "--prompt-file", promptPath, "--tools", "read_file, mcp:docs, read_file"})
	})
	if rc != 0 {
		t.Fatalf("new from file exit = %d", rc)
	}
	preset, err := resolveCommandPreset(root, "", "writer")
	if err != nil || preset.Body != "From file" || !reflect.DeepEqual(preset.Tools, []string{"read_file", "mcp:docs"}) {
		t.Fatalf("file preset = %+v, %v", preset, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"new", "writer"}, "already exists"},
		{[]string{"new", "../escape"}, "invalid preset id"},
		{[]string{"new", "default"}, "read-only"},
		{[]string{"edit", "default", "--prompt", "changed"}, "read-only"},
		{[]string{"edit", "writer"}, "requires"},
		{[]string{"new", "other", "--prompt", "text", "--prompt-file", promptPath}, "only one"},
		{[]string{"edit", "writer", "--tools", "mcp__docs__search"}, "whole servers"},
		{[]string{"new", "other", "--scope", "global"}, "user or project"},
		{[]string{"edit", "missing", "--prompt", "text"}, "not found"},
		{[]string{"new", "other", "--keep-coding-instructions=false"}, "needs a body"},
		{[]string{"new", "other", "--complete"}, "needs a nonempty body"},
		{[]string{"new", "other", "--complete", "--prompt", " \t\n"}, "needs a nonempty body"},
		{[]string{"new", "other", "--complete=maybe", "--prompt", "text"}, "invalid argument"},
		{[]string{"edit", "writer", "--include-runtime-context=maybe"}, "invalid argument"},
		{[]string{"new", "other", "--model", "x"}, "unknown flag"},
	} {
		out := captureStderr(t, func() { rc = presetCommand(tc.args) })
		if rc == 0 || !strings.Contains(out, tc.want) {
			t.Errorf("preset %v exit = %d, stderr = %q; want %q", tc.args, rc, out, tc.want)
		}
	}
}

func TestPresetCommandPromptModeFlagsPreserveOmittedValues(t *testing.T) {
	isolateCLIConfigHome(t)
	root := t.TempDir()
	t.Chdir(root)
	run := func(args ...string) {
		t.Helper()
		var rc int
		captureStdout(t, func() { rc = presetCommand(args) })
		if rc != 0 {
			t.Fatalf("preset %v exit = %d", args, rc)
		}
	}
	check := func(id string, complete, context, keepCoding bool) {
		t.Helper()
		p, err := resolveCommandPreset(root, "user", id)
		if err != nil || p.Complete != complete || p.IncludeRuntimeContext != context || p.KeepCoding != keepCoding || p.Body != "Persona" || p.Tools == nil {
			t.Fatalf("preset %q = %+v, %v", id, p, err)
		}
	}
	run("new", "complete", "--prompt", "Persona", "--complete", "--include-runtime-context=false", "--tools", "[]")
	check("complete", true, false, true)
	run("edit", "complete", "--description", "Updated")
	check("complete", true, false, true)
	run("copy", "complete", "preserved")
	check("preserved", true, false, true)
	run("copy", "complete", "overridden", "--complete=false", "--include-runtime-context=true", "--keep-coding-instructions=false")
	check("overridden", false, true, false)
	run("edit", "preserved", "--keep-coding-instructions=false")
	check("preserved", true, false, false)
	run("edit", "preserved", "--complete=false")
	check("preserved", false, false, false)
	run("edit", "preserved", "--include-runtime-context=true")
	check("preserved", false, true, false)
	run("edit", "preserved", "--complete")
	check("preserved", true, true, false)
	run("copy", "default", "from-default", "--prompt", "Persona", "--complete", "--include-runtime-context=false", "--tools", "[]")
	check("from-default", true, false, true)
}

func TestPresetCommandListReportsMalformedDefinitions(t *testing.T) {
	isolateCLIConfigHome(t)
	root := t.TempDir()
	t.Chdir(root)
	dir := filepath.Join(root, ".deepzora", "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("---\ntool: [bash]\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var rc int
	out := captureStdout(t, func() { rc = Run([]string{"preset", "list", "--json"}, "test") })
	var list control.AgentPresetList
	if err := json.Unmarshal([]byte(out), &list); err != nil || rc != 0 || len(list.Problems) != 1 || len(list.Presets) != 1 {
		t.Fatalf("list = %+v, rc %d, %v; output %q", list, rc, err, out)
	}
	if rc := presetCommand([]string{"rm", "broken"}); rc != 0 {
		t.Fatalf("remove malformed preset exit=%d", rc)
	}
}

func TestPresetCommandReadsStdinAndCopiesDefault(t *testing.T) {
	isolateCLIConfigHome(t)
	root := t.TempDir()
	t.Chdir(root)
	stdin, err := os.CreateTemp(t.TempDir(), "preset-stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	if _, err := stdin.WriteString("Persona from stdin"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	previousStdin := os.Stdin
	os.Stdin = stdin
	t.Cleanup(func() { os.Stdin = previousStdin })
	var rc int
	captureStdout(t, func() {
		rc = presetCommand([]string{"copy", "default", "writer", "--scope", "project", "--prompt-file", "-", "--tools", "", "--keep-coding-instructions=false"})
	})
	if rc != 0 {
		t.Fatalf("copy default exit=%d", rc)
	}
	preset, err := resolveCommandPreset(root, "project", "writer")
	if err != nil || preset.Body != "Persona from stdin" || preset.KeepCoding || preset.Tools == nil || len(preset.Tools) != 0 {
		t.Fatalf("stdin definition = %+v, %v", preset, err)
	}
}
