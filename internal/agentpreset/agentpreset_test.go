package agentpreset

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writePreset(t *testing.T, dir, id, text string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProjectOverridesUserAndDefaultIsBuiltin(t *testing.T) {
	dirs := Dirs{User: t.TempDir(), Project: t.TempDir()}
	writePreset(t, dirs.User, "review", "---\nname: User\n---\nuser body\n")
	writePreset(t, dirs.Project, "review", "---\nname: Project\ntools: [read_file]\n---\nproject body\n")
	writePreset(t, dirs.User, "default", "---\nname: Shadow\n---\nshadow\n")

	p, err := Resolve(dirs, "Review")
	if err != nil {
		t.Fatal(err)
	}
	if p.Scope != ScopeProject || p.Body != "project body" || !reflect.DeepEqual(p.Tools, []string{"read_file"}) {
		t.Fatalf("project preset did not win: %+v", p)
	}
	d, err := Resolve(dirs, "")
	if err != nil || !d.IsDefault() || d.Scope != ScopeBuiltin || d.Body != "" {
		t.Fatalf("empty id must resolve to the built-in default: %+v %v", d, err)
	}
	list, problems := List(dirs)
	if len(list) != 2 || !list[0].IsDefault() || list[1].ID != "review" {
		t.Fatalf("list = %+v", list)
	}
	if len(problems) != 1 || !errors.Is(problems[0], ErrBuiltin) {
		t.Fatalf("shadowing default must be reported, got %v", problems)
	}
	if _, err := Resolve(dirs, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing preset err = %v", err)
	}
	if _, err := Resolve(dirs, "../etc"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("path-like id must be rejected before any read, got %v", err)
	}
}

func TestParseRejectsUnsafeDefinitions(t *testing.T) {
	cases := map[string]string{
		"unknown key":    "---\ntool: [bash]\n---\nbody\n",
		"per-tool mcp":   "---\ntools: [mcp__github__create_issue]\n---\nbody\n",
		"capability mcp": "---\ntools: [\"mcp-tool:github/x\"]\n---\nbody\n",
		"empty server":   "---\ntools: [\"mcp:\"]\n---\nbody\n",
		"bad pattern":    "---\ntools: [\"read_[\"]\n---\nbody\n",
		"empty replace":  "---\nkeep-coding-instructions: false\n---\n",
		"empty complete": "---\ncomplete: true\n---\n \t\n",
		"bad complete":   "---\ncomplete: sometimes\n---\nbody\n",
		"bad context":    "---\ninclude-runtime-context: sometimes\n---\nbody\n",
		"malformed yaml": "---\ntools: [a\n---\nbody\n",
	}
	for name, text := range cases {
		if _, err := Parse("x", text); err == nil {
			t.Errorf("%s: Parse accepted %q", name, text)
		}
	}
	p, err := Parse("x", "---\ntools: []\n---\nonly talk\n")
	if err != nil || p.Tools == nil || len(p.Tools) != 0 || p.Unrestricted() {
		t.Fatalf("explicit empty tools must mean no tools, got %+v %v", p, err)
	}
}

func TestWriteRoundTripsAndRefusesBuiltin(t *testing.T) {
	dir := t.TempDir()
	in := Preset{ID: "docs", Name: "Docs writer", Description: "Writes \"docs\"", Body: "Write docs.", KeepCoding: false, Tools: []string{"read_file", "mcp:github"}}
	saved, err := Write(dir, "docs", Render(in))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(Dirs{User: dir}, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != in.Name || got.Description != in.Description || got.Body != in.Body || got.KeepCoding || !reflect.DeepEqual(got.Tools, in.Tools) || got.Path != saved.Path {
		t.Fatalf("round trip mismatch:\nin  %+v\ngot %+v", in, got)
	}
	if _, err := Write(dir, "default", "body"); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("writing default err = %v", err)
	}
	if _, err := Write(dir, "bad", "---\ntools: [mcp__x__y]\n---\nb"); err == nil {
		t.Fatal("an unloadable preset was saved")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected preset left a file: %v", err)
	}
	if err := Remove(dir, "docs"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, "docs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove err = %v", err)
	}
}

func TestApplyAppendsOrReplaces(t *testing.T) {
	if got := (Preset{Body: "P", KeepCoding: true}).Apply("BASE"); got != "BASE\n\nP" {
		t.Fatalf("append = %q", got)
	}
	if got := (Preset{Body: "P"}).Apply("BASE"); got != "P" {
		t.Fatalf("replace = %q", got)
	}
	if got := (Preset{Body: "P", Complete: true, KeepCoding: true}).Apply("BASE"); got != "P" {
		t.Fatalf("complete = %q", got)
	}
	if got := Default().Apply("BASE"); got != "BASE" {
		t.Fatalf("default changed the prompt: %q", got)
	}
}

func TestPromptModeRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, frontmatter string
		complete, context bool
		keepCoding        bool
	}{
		{"defaults", "", false, true, true},
		{"explicit defaults", "complete: false\ninclude-runtime-context: true\n", false, true, true},
		{"complete", "complete: true\n", true, true, true},
		{"without context", "include-runtime-context: false\n", false, false, true},
		{"complete without context", "complete: true\ninclude-runtime-context: false\nkeep-coding-instructions: false\n", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse("review", "---\n"+tc.frontmatter+"---\n  Persona. \n\n")
			if err != nil {
				t.Fatal(err)
			}
			if p.Complete != tc.complete || p.IncludeRuntimeContext != tc.context || p.KeepCoding != tc.keepCoding || p.Body != "Persona." {
				t.Fatalf("parsed preset = %+v", p)
			}
			rendered := Render(p)
			if strings.Contains(rendered, "complete: true\n") != tc.complete || strings.Contains(rendered, "include-runtime-context: false\n") == tc.context {
				t.Fatalf("nondefault prompt mode fields missing or default fields rendered: %q", rendered)
			}
			if strings.Contains(rendered, "complete: false\n") || strings.Contains(rendered, "include-runtime-context: true\n") {
				t.Fatalf("rendered redundant defaults: %q", rendered)
			}
			got, err := Parse("review", rendered)
			if err != nil || !reflect.DeepEqual(p, got) {
				t.Fatalf("round trip = %+v, %v; want %+v", got, err, p)
			}
		})
	}
}

func TestPromptModeDefaultAndMissing(t *testing.T) {
	parsed, err := Parse("empty", " \n")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Preset{Default(), Missing("deleted"), parsed} {
		if p.Complete || !p.IncludeRuntimeContext || !p.KeepCoding {
			t.Errorf("default prompt mode = %+v", p)
		}
	}
}

func TestToolAdmission(t *testing.T) {
	prefix := func(server string) string { return "mcp__" + server + "__" }
	if Default().ToolAdmission(prefix) != nil {
		t.Fatal("unrestricted preset must not install a filter")
	}
	p := Preset{ID: "x", Tools: []string{"bash", "read_*", "mcp:github"}}
	admit := p.ToolAdmission(prefix)
	for name, want := range map[string]bool{
		"bash": true, "pwsh": true, "read_file": true, "use_capability": true,
		"mcp__github__create": true, "mcp__gitlab__create": false, "write_file": false,
	} {
		if admit(name) != want {
			t.Errorf("admit(%q) = %v, want %v", name, !want, want)
		}
	}
	if !p.AdmitsMCPServer("github") || p.AdmitsMCPServer("gitlab") {
		t.Fatal("server admission mismatch")
	}
	if all := (Preset{Tools: []string{"mcp:*"}}); !all.AdmitsMCPServer("any") || !all.ToolAdmission(prefix)("mcp__any__t") {
		t.Fatal("mcp:* must admit every server")
	}
	if got := p.UnmatchedTools([]string{"pwsh"}); len(got) != 0 {
		t.Fatalf("shell alias reported unmatched: %v", got)
	}
	if got := (Preset{Tools: []string{"red_file"}}).UnmatchedTools([]string{"read_file"}); !reflect.DeepEqual(got, []string{"red_file"}) {
		t.Fatalf("typo not reported: %v", got)
	}
}

func TestListReportsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	writePreset(t, dir, "ok", "fine body\n")
	writePreset(t, dir, "broken", "---\nbogus: 1\n---\nx\n")
	writePreset(t, dir, "Bad_Name", "x\n")
	list, problems := List(Dirs{User: dir})
	if len(list) != 2 || list[1].ID != "ok" {
		t.Fatalf("list = %+v", list)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p.Error(), dir) {
			t.Fatalf("problem lacks its file path: %v", p)
		}
	}
}
