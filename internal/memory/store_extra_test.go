package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// slug

func TestSlug(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Prefers Tabs", "prefers-tabs"},
		{"  spaces  ", "spaces"},
		{"CamelCase", "camelcase"},
		{"with/slash", "with-slash"},
		{"", ""},
		{"---", ""},
		{"hello_world", "hello-world"},
		{"中文标题", "中文标题"},
		{"HÉLLO", "héllo"},
		{"日本語123", "日本語123"},
	}
	for _, c := range cases {
		got := slug(c.input)
		if got != c.want {
			t.Errorf("slug(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestStoreSaveUnicodeAndRejectsEmptySlug(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	path, err := s.Save(Memory{Name: "中文标题", Description: "d", Type: TypeProject, Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "中文标题.md" {
		t.Fatalf("unicode name path = %q", path)
	}
	if _, err := s.Save(Memory{Name: "---", Description: "d", Type: TypeProject, Body: "body"}); err == nil {
		t.Fatal("punctuation-only name should be rejected")
	}
}

// oneLine

func TestOneLine(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello world", "hello world"},
		{"  multiple   spaces  ", "multiple spaces"},
		{"tabs\there", "tabs here"},
		{"\n\nnewlines\n\n", "newlines"},
		{"", ""},
	}
	for _, c := range cases {
		got := oneLine(c.input)
		if got != c.want {
			t.Errorf("oneLine(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// render

func TestRenderRoundTrip(t *testing.T) {
	m := Memory{
		Name:        "test-fact",
		Description: "A test fact",
		Type:        TypeUser,
		Body:        "The body of the fact.",
	}
	rendered := render(m, "test-fact")
	fm, body := splitFrontmatter(rendered)
	if fm["name"] != "test-fact" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "A test fact" {
		t.Errorf("description = %q", fm["description"])
	}
	if got := persistedFactType(fm); got != TypeUser {
		t.Errorf("type = %q", got)
	}
	if !strings.Contains(body, "The body of the fact.") {
		t.Errorf("body = %q", body)
	}
}

func TestRenderNormalizesType(t *testing.T) {
	m := Memory{Name: "x", Description: "d", Type: Type("unknown"), Body: "b"}
	rendered := render(m, "x")
	fm, _ := splitFrontmatter(rendered)
	if fm["type"] != "project" {
		t.Errorf("unknown type should normalize to project, got %q", fm["type"])
	}
}

// loadMemory

func TestLoadMemoryMissingFile(t *testing.T) {
	_, ok := loadMemory("/nonexistent/path.md")
	if ok {
		t.Error("loadMemory should return false for missing file")
	}
}

// Store.List edge cases

func TestListSkipsNonMdFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fact.md"), []byte(render(Memory{ID: "mem-fact", Revision: 1, Scope: FactScopeProject, Name: "fact", Body: "body"}, "fact")), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a memory"), 0o644)
	os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("# Memory\n"), 0o644)
	s := Store{Dir: dir}
	list := s.ListAll()
	if len(list) != 1 {
		t.Fatalf("want 1 memory, got %d", len(list))
	}
	if list[0].Name != "fact" {
		t.Errorf("name = %q", list[0].Name)
	}
}

func TestListEmptyDir(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if list := s.ListAll(); len(list) != 0 {
		t.Errorf("empty dir should return empty list, got %d", len(list))
	}
}

func TestListSortedByName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"zebra", "alpha", "middle"} {
		os.WriteFile(filepath.Join(dir, name+".md"), []byte(render(Memory{ID: "mem-" + name, Revision: 1, Scope: FactScopeProject, Name: name, Body: "body"}, name)), 0o644)
	}
	s := Store{Dir: dir}
	list := s.ListAll()
	if len(list) != 3 {
		t.Fatalf("want 3, got %d", len(list))
	}
	if list[0].Name != "alpha" || list[1].Name != "middle" || list[2].Name != "zebra" {
		t.Errorf("not sorted: %v %v %v", list[0].Name, list[1].Name, list[2].Name)
	}
}

// Store.Save edge cases

func TestSaveEmptyName(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	_, err := s.Save(Memory{Name: "", Description: "d", Body: "b"})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestSaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: filepath.Join(dir, "deep", "nested", "memory")}
	_, err := s.Save(Memory{Name: "test", Description: "d", Body: "b"})
	if err != nil {
		t.Fatalf("Save should create dirs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "test.md")); err != nil {
		t.Fatal("memory file should exist")
	}
}

// Store.Path

func TestStorePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memory")
	s := Store{Dir: dir}
	got := s.Path("My Fact")
	if want := filepath.Join(dir, "my-fact.md"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
