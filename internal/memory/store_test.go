package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	fileencoding "deepzora/internal/fileutil/encoding"
)

// TestRenderEscapesYAMLMetacharacters pins the frontmatter-corruption fix: a
// title or description with YAML metacharacters must survive a save→load
// round-trip. The previous hand-concatenated renderer produced unparseable
// YAML for "Plan: step one"-style titles; frontmatter.Split then returned an
// empty map and the reloaded memory silently lost its name, title, and type.
func TestRenderEscapesYAMLMetacharacters(t *testing.T) {
	cases := []struct {
		label, title, desc string
	}{
		{"colon", "My plan: step one", "Covers: the rollout"},
		{"hash", "Ship #42", "Tracks #release notes"},
		{"double-quote", `The "golden" path`, `Says "hello"`},
		{"single-quote", "Don't drop this", "User's preference"},
		{"leading-special", "- looks like a list", "* also a list"},
		{"yaml-lookalike", "type: reference", "metadata: nested"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			dir := t.TempDir()
			s := Store{Dir: filepath.Join(dir, "memory")}
			if _, err := s.Save(Memory{
				Name:        "esc-" + tc.label,
				Title:       tc.title,
				Description: tc.desc,
				Type:        TypeProject,
				Body:        "the body",
			}); err != nil {
				t.Fatal(err)
			}
			list := s.ListAll()
			if len(list) != 1 {
				t.Fatalf("want 1 memory, got %d", len(list))
			}
			got := list[0]
			if got.Title != tc.title {
				t.Errorf("Title = %q, want %q", got.Title, tc.title)
			}
			if got.Description != tc.desc {
				t.Errorf("Description = %q, want %q", got.Description, tc.desc)
			}
			if got.Type != TypeProject {
				t.Errorf("Type = %q, want project (metadata lost)", got.Type)
			}
			if got.Body != "the body" {
				t.Errorf("Body = %q", got.Body)
			}
		})
	}
}

func TestRenderPreservesIndependentTypeAndScope(t *testing.T) {
	for _, scope := range []FactScope{FactScopeProject, FactScopeGlobal} {
		for _, typ := range []Type{TypeUser, TypeFeedback, TypeProject, TypeReference} {
			fm, _ := splitFrontmatter(render(Memory{Type: typ, Scope: scope, Body: "fact"}, "fact"))
			if persistedFactType(fm) != typ || factScopeFromFrontmatter(fm["scope"]) != scope || fm["fact_type"] != "" {
				t.Fatalf("frontmatter: %v", fm)
			}
		}
	}
}

// TestStoreSaveAndIndex covers the round-trip: Save writes a frontmatter file,
// reindex adds exactly one index line, and List parses it back.
func TestStoreSaveAndIndex(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: filepath.Join(dir, "memory")}

	path, err := s.Save(Memory{
		Name:        "Prefers Tabs",
		Description: "User prefers tabs over spaces",
		Type:        TypeUser,
		Body:        "Always indent with tabs in this project.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "prefers-tabs.md" {
		t.Fatalf("name not slugified into filename: %s", path)
	}

	idx := s.Index()
	if !strings.Contains(idx, "prefers-tabs.md") || !strings.Contains(idx, "User prefers tabs") {
		t.Fatalf("index missing entry:\n%s", idx)
	}

	list := s.ListAll()
	if len(list) != 1 {
		t.Fatalf("want 1 memory, got %d", len(list))
	}
	m := list[0]
	if m.Name != "prefers-tabs" || m.Type != TypeUser {
		t.Fatalf("round-trip mismatch: %+v", m)
	}
	if !strings.Contains(m.Body, "indent with tabs") {
		t.Fatalf("body not preserved: %q", m.Body)
	}
}

func TestStoreListDecodesGB18030MemoryFile(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
id: mem-cn
revision: 1
name: cn-pref
scope: project
title: 中文偏好
description: 使用中文回答
type: user
---
用户希望默认使用中文。`
	if err := os.WriteFile(filepath.Join(s.Dir, "cn-pref.md"), fileencoding.MustEncode(body, fileencoding.GB18030), 0o644); err != nil {
		t.Fatal(err)
	}

	memories := s.ListAll()
	if len(memories) != 1 {
		t.Fatalf("List() = %+v, want one decoded memory", memories)
	}
	m := memories[0]
	if m.Title != "中文偏好" || m.Description != "使用中文回答" || !strings.Contains(m.Body, "默认使用中文") {
		t.Fatalf("decoded memory = %+v", m)
	}
}

// TestStoreOverwriteDoesNotDuplicateIndex verifies re-saving the same name
// replaces its index line rather than appending a second.
func TestStoreOverwriteDoesNotDuplicateIndex(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, desc := range []string{"first version", "second version"} {
		if _, err := s.Save(Memory{Name: "note", Description: desc, Type: TypeProject, Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	idx := s.Index()
	if n := strings.Count(idx, "note.md"); n != 1 {
		t.Fatalf("want exactly 1 index line for note, got %d:\n%s", n, idx)
	}
	if !strings.Contains(idx, "second version") || strings.Contains(idx, "first version") {
		t.Fatalf("index not updated to latest description:\n%s", idx)
	}
}

// TestStoreIndexPreservesHandEdits verifies reindex keeps unrelated lines, so a
// user hand-editing MEMORY.md isn't clobbered when the model saves a new fact.
func TestStoreIndexPreservesHandEdits(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{Name: "alpha", Description: "first", Type: TypeProject, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(s.Dir, indexFile)
	handEdited := "# Memory\n\nUser note before managed lines.\n\n" + mustReadString(t, indexPath) + "\nSee [design](design.md) for context.\n"
	if err := os.WriteFile(indexPath, []byte(handEdited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Memory{Name: "beta", Description: "second", Type: TypeProject, Body: "y"}); err != nil {
		t.Fatal(err)
	}
	raw := mustReadString(t, indexPath)
	for _, want := range []string{"User note before managed lines.", "See [design](design.md) for context.", "alpha.md", "beta.md"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("MEMORY.md lost %q:\n%s", want, raw)
		}
	}
	if strings.Count(raw, "alpha.md") != 1 || strings.Count(raw, "beta.md") != 1 {
		t.Fatalf("managed lines were duplicated:\n%s", raw)
	}
	if err := s.Delete("alpha"); err != nil {
		t.Fatal(err)
	}
	raw = mustReadString(t, indexPath)
	if strings.Contains(raw, "alpha.md") {
		t.Fatalf("deleted managed line remained:\n%s", raw)
	}
	if !strings.Contains(raw, "See [design](design.md) for context.") {
		t.Fatalf("ordinary markdown link was treated as managed:\n%s", raw)
	}
}

// TestStoreSaveTitleInIndexAndFrontmatter verifies an explicit title becomes the
// index link label and round-trips through the file's frontmatter.
func TestStoreSaveTitleInIndexAndFrontmatter(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{
		Name:        "tabs-rule",
		Title:       "Prefers tabs",
		Description: "indent with tabs",
		Type:        TypeUser,
		Body:        "b",
	}); err != nil {
		t.Fatal(err)
	}
	if idx := s.Index(); !strings.Contains(idx, "[Prefers tabs](project/tabs-rule.md)") {
		t.Fatalf("index link should use the title label:\n%s", idx)
	}
	if got := s.ListAll()[0].Title; got != "Prefers tabs" {
		t.Fatalf("title not round-tripped: %q", got)
	}
}

// TestStoreIndexLabelFallsBackToDeKebabbedName checks a title-less memory still
// gets a readable label instead of a bare slug.
func TestStoreIndexLabelFallsBackToDeKebabbedName(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{Name: "likes-go", Description: "d", Type: TypeUser, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if idx := s.Index(); !strings.Contains(idx, "[likes go](project/likes-go.md)") {
		t.Fatalf("missing-title label should de-kebab the name:\n%s", idx)
	}
}

// TestStoreDelete archives a fact's file and removes its index line while
// leaving others.
func TestStoreDelete(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, n := range []string{"alpha", "beta"} {
		if _, err := s.Save(Memory{Name: n, Description: "d", Type: TypeProject, Body: "b"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete("alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha.md")); !os.IsNotExist(err) {
		t.Fatalf("alpha.md should be gone, stat err = %v", err)
	}
	archived := archivedFiles(t, s.Dir)
	if len(archived) != 1 || !strings.HasSuffix(archived[0], "-alpha.md") {
		t.Fatalf("archive files = %v, want one alpha archive", archived)
	}
	idx := s.Index()
	if strings.Contains(idx, "alpha.md") {
		t.Fatalf("deleted entry still in index:\n%s", idx)
	}
	if !strings.Contains(idx, "beta.md") {
		t.Fatalf("unrelated entry lost on delete:\n%s", idx)
	}
	if names := s.ListAll(); len(names) != 1 || names[0].Name != "beta" {
		t.Fatalf("List after delete = %+v, want only beta", names)
	}
}

// TestStoreDeleteMissingIsNoError treats deleting an absent memory as success —
// the goal state (gone) already holds.
func TestStoreDeleteMissingIsNoError(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Delete("never-saved"); err != nil {
		t.Fatalf("deleting a missing memory should not error: %v", err)
	}
}

func TestSafeJoinRejectsStoreEscape(t *testing.T) {
	dir := t.TempDir()
	if _, err := safeJoin(dir, filepath.Join("..", "outside.md")); err == nil {
		t.Fatal("safeJoin should reject paths outside the store")
	}
	if _, err := safeJoin(dir, filepath.Join(t.TempDir(), "outside.md")); err == nil {
		t.Fatal("safeJoin should reject absolute paths outside the store")
	}
}

func TestStoreArchiveSanitizesNameBeforePathUse(t *testing.T) {
	root := t.TempDir()
	s := Store{Dir: filepath.Join(root, "memory")}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("do not move"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Archive("../outside"); err != nil {
		t.Fatalf("Archive with path-like name should be treated as a slug, not a path: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should remain untouched: %v", err)
	}
}

func TestStoreDeleteRepairsReadOnlyMemoryFile(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{Name: "locked", Description: "d", Type: TypeProject, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "locked.md")
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("locked"); err != nil {
		t.Fatalf("delete read-only memory: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("locked.md should be gone, stat err = %v", err)
	}
	archived := archivedFiles(t, s.Dir)
	if len(archived) != 1 || !strings.HasSuffix(archived[0], "-locked.md") {
		t.Fatalf("archive files = %v, want one locked archive", archived)
	}
	if strings.Contains(s.Index(), "locked.md") {
		t.Fatalf("deleted read-only entry still in index:\n%s", s.Index())
	}
}

func TestStoreArchiveReturnsArchivePath(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{Name: "old-fact", Description: "d", Type: TypeProject, Body: "body"}); err != nil {
		t.Fatal(err)
	}
	archive, err := s.Archive("old-fact")
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if archive == "" {
		t.Fatal("Archive returned empty path for existing memory")
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !strings.Contains(string(body), "body") {
		t.Fatalf("archive missing memory body:\n%s", body)
	}
	if strings.Contains(s.Index(), "old-fact.md") {
		t.Fatalf("archived memory still in index:\n%s", s.Index())
	}
	archived := s.ListArchived()
	if len(archived) != 1 {
		t.Fatalf("ListArchived = %+v, want one entry", archived)
	}
	if archived[0].Name != "old-fact" || archived[0].Path != archive {
		t.Fatalf("archived entry mismatch: %+v, path %q", archived[0], archive)
	}
	if archived[0].ArchivedAt.IsZero() {
		t.Fatalf("archived entry missing timestamp: %+v", archived[0])
	}
	if len(s.ListAll()) != 0 {
		t.Fatalf("active List should exclude archived memories: %+v", s.ListAll())
	}
}

func TestStoreArchiveFlushesStaleIndexWithoutFile(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save(Memory{Name: "beta", Description: "keep", Type: TypeProject, Body: "body"}); err != nil {
		t.Fatal(err)
	}
	if err := flushIndexIn(s.Dir, map[string]string{
		"alpha": "- [alpha](alpha.md) — stale",
		"beta":  "- [beta](beta.md) — keep",
	}); err != nil {
		t.Fatal(err)
	}

	archive, err := s.Archive("alpha")
	if err != nil {
		t.Fatalf("Archive stale index: %v", err)
	}
	if archive != "" {
		t.Fatalf("Archive should return no path for missing file, got %q", archive)
	}
	idx := s.Index()
	if strings.Contains(idx, "alpha.md") {
		t.Fatalf("stale index line should be removed:\n%s", idx)
	}
	if !strings.Contains(idx, "beta.md") {
		t.Fatalf("unrelated index line should remain:\n%s", idx)
	}
}

func mustReadString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStoreListArchivedNewestFirst(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	dir := filepath.Join(s.Dir, ".archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := []struct {
		name string
		body string
	}{
		{"20260101-010000.000-old.md", render(Memory{ID: "mem-old", Revision: 1, Scope: FactScopeProject, Name: "old", Description: "old d", Type: TypeProject, Body: "old body"}, "old")},
		{"20260102-010000.000-new.md", render(Memory{ID: "mem-new", Revision: 1, Scope: FactScopeGlobal, Name: "new", Description: "new d", Type: TypeFeedback, Body: "new body"}, "new")},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archived := s.ListArchived()
	if len(archived) != 2 {
		t.Fatalf("ListArchived len = %d, want 2: %+v", len(archived), archived)
	}
	if archived[0].Name != "new" || archived[1].Name != "old" {
		t.Fatalf("ListArchived order = %+v, want newest first", archived)
	}
	if archived[0].Type != TypeFeedback || !strings.Contains(archived[1].Body, "old body") {
		t.Fatalf("archived memory did not round-trip metadata/body: %+v", archived)
	}
}

func archivedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, ".archive"))
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	var out []string
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

// TestNormalizeType maps unknown types to project and keeps known ones.
func TestNormalizeType(t *testing.T) {
	if got := NormalizeType("feedback"); got != TypeFeedback {
		t.Errorf("feedback: got %q", got)
	}
	if got := NormalizeType("garbage"); got != TypeProject {
		t.Errorf("unknown should default to project, got %q", got)
	}
}

func TestStoreForWorkspaceIdentity(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	store := StoreFor(home, root)
	again := StoreFor(home, root)
	other := StoreFor(home, t.TempDir())
	if store.Dir != again.Dir || store.Dir == other.Dir {
		t.Fatalf("workspace identity directories = %q / %q / %q", store.Dir, again.Dir, other.Dir)
	}
	if filepath.Dir(filepath.Dir(store.Dir)) != filepath.Join(home, "projects") {
		t.Fatalf("memory escaped project home: %q", store.Dir)
	}
}

// TestDisabledStoreIsNoOp ensures a zero Store (no user config dir) never panics
// and errors cleanly on Save.
func TestDisabledStoreIsNoOp(t *testing.T) {
	var s Store
	if s.Index() != "" || s.ListAll() != nil {
		t.Fatal("disabled store should read empty")
	}
	if _, err := s.Save(Memory{Name: "x", Description: "d", Body: "b"}); err == nil {
		t.Fatal("disabled store Save should error, not silently drop")
	}
}

// TestStoreGlobalAndProject verifies explicit scope routing independently of
// memory type, merged reads, and deletion from the correct directory.
func TestStoreGlobalAndProject(t *testing.T) {
	dir := t.TempDir()
	s := Store{
		Dir:       filepath.Join(dir, "project", "memory"),
		GlobalDir: filepath.Join(dir, "global"),
	}

	// A user preference can be explicitly global.
	pUser, err := s.Save(Memory{Name: "prefers-tabs", Description: "user pref", Type: TypeUser, Scope: FactScopeGlobal, Body: "use tabs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pUser, s.GlobalDir) {
		t.Fatalf("global-scoped user memory should go to GlobalDir, got %s", pUser)
	}

	// Project scope is the default, independently of type.
	pProj, err := s.Save(Memory{Name: "build-target", Description: "build target", Type: TypeProject, Body: "go build"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pProj, s.Dir) {
		t.Fatalf("project-scoped memory should go to Dir, got %s", pProj)
	}

	// Feedback can also be explicitly global.
	pFb, err := s.Save(Memory{Name: "no-emoji", Description: "no emoji", Type: TypeFeedback, Scope: FactScopeGlobal, Body: "skip emoji"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pFb, s.GlobalDir) {
		t.Fatalf("global-scoped feedback should go to GlobalDir, got %s", pFb)
	}

	// A reference also defaults to project scope.
	pRef, err := s.Save(Memory{Name: "api-docs", Description: "api docs", Type: TypeReference, Body: "see docs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pRef, s.Dir) {
		t.Fatalf("project-scoped reference should go to Dir, got %s", pRef)
	}

	// List merges both directories
	list := s.ListAll()
	if len(list) != 4 {
		t.Fatalf("want 4 memories, got %d", len(list))
	}

	// Index merges both directories
	idx := s.Index()
	if !strings.Contains(idx, "prefers-tabs") || !strings.Contains(idx, "build-target") {
		t.Fatalf("index should contain both global and project memories:\n%s", idx)
	}

	// Delete removes from the correct directory
	if err := s.Delete("prefers-tabs"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pUser); !os.IsNotExist(err) {
		t.Fatal("global memory file should be gone after delete")
	}

	// List after delete
	list2 := s.ListAll()
	if len(list2) != 3 {
		t.Fatalf("want 3 memories after delete, got %d", len(list2))
	}

	// Index should not duplicate # Memory headers (Block() adds its own).
	idx2 := s.Index()
	if strings.Count(idx2, "# Memory") != 0 {
		t.Fatalf("Index should have 0 # Memory headers (Block() adds one), got %d:\n%s", strings.Count(idx2, "# Memory"), idx2)
	}
}

func TestStoreSaveKeepsSameNamedFactsInDifferentScopes(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: filepath.Join(dir, "project", "memory"), GlobalDir: filepath.Join(dir, "global")}
	global, err := s.SaveWithOptions(Memory{Name: "same-name", Scope: FactScopeGlobal, Description: "global fact", Body: "global body"}, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.SaveWithOptions(Memory{Name: "same-name", Scope: FactScopeProject, Description: "project fact", Body: "project body"}, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if global.Memory.ID == project.Memory.ID {
		t.Fatal("scopes reused one identity")
	}
	for _, fact := range []SaveResult{global, project} {
		if got, ok := s.Read(fact.Memory.ID); !ok || got.Body != fact.Memory.Body || got.Scope != fact.Memory.Scope {
			t.Fatalf("scope changed or fact removed: %+v", got)
		}
	}
	if got := s.ListAll(); len(got) != 2 {
		t.Fatalf("facts=%+v, want both scopes", got)
	}
}

// TestStoreForInitializesGlobalDir ensures StoreFor sets GlobalDir alongside Dir.
func TestStoreForInitializesGlobalDir(t *testing.T) {
	s := StoreFor("/home/me/.deepzora", "/Users/me/proj")
	if s.GlobalDir == "" {
		t.Fatal("StoreFor should set GlobalDir")
	}
	if !strings.Contains(s.GlobalDir, "memory") || !strings.Contains(s.GlobalDir, "global") {
		t.Fatalf("unexpected GlobalDir: %s", s.GlobalDir)
	}
	if s.GlobalDir == s.Dir {
		t.Fatal("GlobalDir and Dir should be different paths")
	}
}

// TestDirForRoutesCorrectly verifies scope, rather than type, owns routing.
func TestDirForRoutesCorrectly(t *testing.T) {
	dir := t.TempDir()
	s := Store{
		Dir:       filepath.Join(dir, "project", "memory"),
		GlobalDir: filepath.Join(dir, "global"),
	}
	if got := s.DirFor(FactScopeGlobal); got != s.GlobalDir {
		t.Errorf("global: got %q, want %q", got, s.GlobalDir)
	}
	if got := s.DirFor(FactScopeProject); got != s.Dir {
		t.Errorf("project: got %q, want %q", got, s.Dir)
	}
	if got := s.DirFor(""); got != s.Dir {
		t.Errorf("default: got %q, want %q", got, s.Dir)
	}
}

// TestDirForFallsBackWhenNoGlobalDir ensures DirFor falls back to Dir when
// GlobalDir is empty.
func TestDirForFallsBackWhenNoGlobalDir(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: filepath.Join(dir, "memory")}
	if got := s.DirFor(FactScopeGlobal); got != s.Dir {
		t.Errorf("global scope without GlobalDir should fall back to Dir, got %q", got)
	}
}

func TestStoreDefaultsNewMemoriesToProjectScope(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: filepath.Join(dir, "project"), GlobalDir: filepath.Join(dir, "global")}
	path, err := s.Save(Memory{Name: "project-feedback", Description: "project-only feedback", Type: TypeFeedback, Body: "keep this local"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, s.Dir) {
		t.Fatalf("default save path = %q, want project dir %q", path, s.Dir)
	}
	list := s.ListAll()
	if len(list) != 1 || list[0].Scope != FactScopeProject {
		t.Fatalf("default memory = %+v, want project scope", list)
	}
}

func TestStoreSaveWithoutScopePreservesExistingGlobalScope(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: filepath.Join(dir, "project"), GlobalDir: filepath.Join(dir, "global")}
	globalPath, err := s.Save(Memory{Name: "same-name", Description: "old", Type: TypeUser, Scope: FactScopeGlobal, Body: "old body"})
	if err != nil {
		t.Fatal(err)
	}
	written, err := s.Save(Memory{Name: "same-name", Description: "new", Type: TypeUser, Body: "new body"})
	if err != nil {
		t.Fatal(err)
	}
	if written != globalPath {
		t.Fatalf("omitted-scope update path = %q, want existing global path %q", written, globalPath)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "same-name.md")); !os.IsNotExist(err) {
		t.Fatalf("unexpected project copy after inherited update, stat err=%v", err)
	}
}

// TestStoreSaveVerifiesIndexDir verifies that Save writes MEMORY.md to the
// directory selected by explicit scope, independently of type.
func TestStoreSaveVerifiesIndexDir(t *testing.T) {
	dir := t.TempDir()
	s := Store{
		Dir:       filepath.Join(dir, "project", "memory"),
		GlobalDir: filepath.Join(dir, "global"),
	}

	// Explicit global user preference → GlobalDir.
	if _, err := s.Save(Memory{Name: "user-pref", Description: "d", Type: TypeUser, Scope: FactScopeGlobal, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	gb, _ := os.ReadFile(filepath.Join(s.GlobalDir, indexFile))
	pb, _ := os.ReadFile(filepath.Join(s.Dir, indexFile))
	if !strings.Contains(string(gb), "user-pref") {
		t.Fatal("GlobalDir MEMORY.md should contain user-pref")
	}
	if strings.Contains(string(pb), "user-pref") {
		t.Fatal("Dir MEMORY.md should NOT contain user-pref (it went to GlobalDir)")
	}

	// TypeProject → Dir
	if _, err := s.Save(Memory{Name: "build-cmd", Description: "d", Type: TypeProject, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	pb2, _ := os.ReadFile(filepath.Join(s.Dir, indexFile))
	if !strings.Contains(string(pb2), "build-cmd") {
		t.Fatal("Dir MEMORY.md should contain build-cmd")
	}
	gb2, _ := os.ReadFile(filepath.Join(s.GlobalDir, indexFile))
	if strings.Contains(string(gb2), "build-cmd") {
		t.Fatal("GlobalDir MEMORY.md should NOT contain build-cmd (it went to Dir)")
	}
}

// TestStoreDeleteFlushesIndexPerDir verifies that Delete calls flushIndexIn
// for each directory where the memory file existed.
func TestStoreDeleteFlushesIndexPerDir(t *testing.T) {
	dir := t.TempDir()
	s := Store{
		Dir:       filepath.Join(dir, "project", "memory"),
		GlobalDir: filepath.Join(dir, "global"),
	}

	// Write to both dirs manually (migration scenario).
	name := "prefers-tabs"
	for _, d := range []string{s.GlobalDir, s.Dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		m := Memory{Name: name, Description: "d", Type: TypeUser, Body: "b"}
		if err := os.WriteFile(filepath.Join(d, name+".md"), []byte(render(m, name)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := reindexIn(d, name, m); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Delete(name); err != nil {
		t.Fatal(err)
	}

	// Verify both MEMORY.md files have the entry removed.
	gb, _ := os.ReadFile(filepath.Join(s.GlobalDir, indexFile))
	pb, _ := os.ReadFile(filepath.Join(s.Dir, indexFile))
	if strings.Contains(string(gb), name+".md") {
		t.Fatalf("GlobalDir MEMORY.md should not reference %s after delete:\n%s", name, gb)
	}
	if strings.Contains(string(pb), name+".md") {
		t.Fatalf("Dir MEMORY.md should not reference %s after delete:\n%s", name, pb)
	}

	// Index() should return "" (no entries, no orphaned header).
	idx := s.Index()
	if idx != "" {
		t.Fatalf("Index() should return empty after deleting all entries, got:\n%s", idx)
	}
}

// TestStorePathWithGlobalDir verifies Path() checks GlobalDir first and
// falls back to Dir for new files.
func TestStorePathWithGlobalDir(t *testing.T) {
	dir := t.TempDir()
	s := Store{
		Dir:       filepath.Join(dir, "project", "memory"),
		GlobalDir: filepath.Join(dir, "global"),
	}

	// No files yet → defaults to Dir.
	p := s.Path("new-fact")
	if !strings.HasPrefix(p, s.Dir) {
		t.Fatalf("Path for new file should default to Dir, got %s", p)
	}

	// Write a file to GlobalDir.
	if err := os.MkdirAll(s.GlobalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.GlobalDir, "existing.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2 := s.Path("existing")
	if !strings.HasPrefix(p2, s.GlobalDir) {
		t.Fatalf("Path for file in GlobalDir should return GlobalDir path, got %s", p2)
	}

	// Write a file to Dir (not GlobalDir).
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "proj-fact.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	p3 := s.Path("proj-fact")
	if !strings.HasPrefix(p3, s.Dir) {
		t.Fatalf("Path for file only in Dir should return Dir path, got %s", p3)
	}
}
