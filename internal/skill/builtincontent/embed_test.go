package builtincontent_test

import (
	"strings"
	"testing"

	"deeper/internal/skill/builtincontent"
)

func TestLoadDeeperGuide(t *testing.T) {
	sk, err := builtincontent.LoadDeeperGuide()
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "deeper-guide" {
		t.Fatalf("name = %q", sk.Name)
	}
	if sk.Description == "" {
		t.Fatal("missing description")
	}
	if sk.RunAs != "inline" {
		t.Fatalf("runAs = %q, want inline", sk.RunAs)
	}
	if !strings.Contains(sk.Body, "deeper doctor capabilities") {
		t.Fatal("body should recommend doctor capabilities")
	}
	if strings.Contains(strings.ToLower(sk.Frontmatter["auto-use"]), "require") {
		t.Fatal("guide must not set auto-use require")
	}
}

func TestParseSkillMarkdownStable(t *testing.T) {
	raw := "---\nname: demo\ndescription: d\nrunAs: inline\n---\n\n# hi\n"
	a, err := builtincontent.ParseSkillMarkdown("demo/SKILL.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := builtincontent.ParseSkillMarkdown("demo/SKILL.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Body != b.Body || a.Description != b.Description {
		t.Fatal("parse not deterministic")
	}
}
