package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/instruction"
	"deepzora/internal/memory"
)

func TestMemoryViewReturnsNonNilArraysBeforeStartup(t *testing.T) {
	isolateDesktopUserDirs(t)

	view := NewApp().Memory()
	if view.Docs == nil || view.Facts == nil || view.Archives == nil || view.Scopes == nil || view.InstructionDiagnostics == nil || view.Conflicts == nil || view.LastRecall.Hits == nil {
		t.Fatalf("Memory() arrays must be non-nil before startup: %+v", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal Memory(): %v", err)
	}
	for _, bad := range []string{`"docs":null`, `"facts":null`, `"archives":null`, `"scopes":null`, `"instructionDiagnostics":null`, `"conflicts":null`, `"hits":null`} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("Memory() JSON contains %s; frontend expects []: %s", bad, raw)
		}
	}
	if revisions := NewApp().MemoryRevisions("missing"); revisions == nil {
		t.Fatal("MemoryRevisions must return [] before startup, not nil")
	}
}

func TestMemoryViewIncludesRecallFreshnessAndOverrides(t *testing.T) {
	isolateDesktopUserDirs(t)
	root := t.TempDir()
	store := memory.Store{Dir: filepath.Join(root, "project"), GlobalDir: filepath.Join(root, "global")}
	if _, err := (memory.Store{Dir: store.GlobalDir}).Save(memory.Memory{
		Name: "deploy-target", Title: "Deploy target", Description: "legacy deployment target", Scope: memory.FactScopeGlobal, Type: memory.TypeProject, Body: "Deploy payments to the legacy cluster.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := (memory.Store{Dir: store.Dir}).Save(memory.Memory{
		Name: "deploy-target", Title: "Deploy target", Description: "current deployment target", Scope: memory.FactScopeProject, Type: memory.TypeProject, Body: "Deploy payments to the green cluster.",
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{Memory: &memory.Set{Store: store}})
	ctrl.Compose("deploy payments target cluster")
	app := NewApp()
	app.setTestCtrl(ctrl, "test-model")

	view := app.Memory()
	if len(view.Facts) != 2 || view.Facts[0].Freshness == "" || view.Facts[1].Freshness == "" {
		t.Fatalf("facts with freshness = %+v", view.Facts)
	}
	if len(view.Conflicts) != 1 || view.Conflicts[0].Resolution != "project_over_global" {
		t.Fatalf("conflicts = %+v", view.Conflicts)
	}
	if view.LastRecall.Query != "deploy payments target cluster" || len(view.LastRecall.Hits) != 1 || view.LastRecall.Hits[0].Scope != "project" {
		t.Fatalf("last recall = %+v", view.LastRecall)
	}
}

func TestMemoryRevisionAPIRestoresSelectedRevision(t *testing.T) {
	isolateDesktopUserDirs(t)
	store := memory.Store{Dir: t.TempDir()}
	first, err := store.SaveWithOptions(memory.Memory{Name: "fact", Description: "one", Body: "v1"}, memory.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveWithOptions(memory.Memory{ID: first.Memory.ID, Name: "fact", Description: "two", Body: "v2"}, memory.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Memory: &memory.Set{Store: store}}), "test-model")

	revisions := app.MemoryRevisions(first.Memory.ID)
	if len(revisions) != 1 || revisions[0].Revision != 1 {
		t.Fatalf("revisions = %+v", revisions)
	}
	restored, err := app.RestoreMemoryRevision(first.Memory.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != 3 || restored.Body != "v1" {
		t.Fatalf("restored = %+v", restored)
	}
}

func TestMemoryViewIncludesActiveAndArchivedFacts(t *testing.T) {
	isolateDesktopUserDirs(t)
	userDir := t.TempDir()
	cwd := t.TempDir()
	store := memory.Store{Dir: filepath.Join(userDir, "projects", "test", "memory")}
	if _, err := store.Save(memory.Memory{
		Name:        "active-fact",
		Title:       "Active fact",
		Description: "Still applies",
		Type:        memory.TypeProject,
		Body:        "Active body",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(memory.Memory{
		Name:        "archived-fact",
		Description: "No longer applies",
		Type:        memory.TypeFeedback,
		Body:        "Archived body",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Archive("archived-fact"); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Memory: &memory.Set{
		Docs: []memory.Source{{
			Path: filepath.Join(cwd, "AGENTS.md"), Scope: memory.ScopeProject, Directory: cwd,
			Body: "Project instructions", Imports: []instruction.Import{{Path: filepath.Join(cwd, "shared.md"), SourcePath: filepath.Join(cwd, "AGENTS.md")}},
		}},
		InstructionDiagnostics: []instruction.Diagnostic{{Code: "import_cycle", Path: "shared.md", SourcePath: filepath.Join(cwd, "AGENTS.md"), Line: 3, Message: "cycle"}},
		Store:                  store, CWD: cwd, UserDir: userDir,
	}}), "test-model")

	view := app.Memory()
	if !view.Available || view.StoreDir != store.Dir {
		t.Fatalf("Memory() availability/store = %v/%q, want true/%q", view.Available, view.StoreDir, store.Dir)
	}
	if len(view.Docs) != 1 || view.Docs[0].Scope != "project" || !strings.Contains(view.Docs[0].Body, "Project instructions") {
		t.Fatalf("Memory() docs = %+v", view.Docs)
	}
	if view.Docs[0].Directory != cwd || len(view.Docs[0].Imports) != 1 || len(view.InstructionDiagnostics) != 1 || view.InstructionDiagnostics[0].Code != "import_cycle" {
		t.Fatalf("Memory() instruction provenance = docs %+v diagnostics %+v", view.Docs, view.InstructionDiagnostics)
	}
	if len(view.Facts) != 1 || view.Facts[0].Name != "active-fact" || view.Facts[0].Type != "project" || view.Facts[0].Scope != "project" {
		t.Fatalf("Memory() active facts = %+v", view.Facts)
	}
	if view.Facts[0].ID == "" || view.Facts[0].Revision != 1 || view.Facts[0].CreatedAt == "" || view.Facts[0].UpdatedAt == "" {
		t.Fatalf("Memory() active fact metadata = %+v", view.Facts[0])
	}
	if len(view.Archives) != 1 || view.Archives[0].Name != "archived-fact" || view.Archives[0].Type != "feedback" || view.Archives[0].Scope != "project" ||
		view.Archives[0].Path == "" || view.Archives[0].ArchivedAt == "" {
		t.Fatalf("Memory() archived facts = %+v", view.Archives)
	}
	if view.Archives[0].ID == "" || view.Archives[0].Revision != 1 || view.Archives[0].CreatedAt == "" || view.Archives[0].UpdatedAt == "" {
		t.Fatalf("Memory() archived fact metadata = %+v", view.Archives[0])
	}
	if len(view.Scopes) != 3 {
		t.Fatalf("Memory() scopes = %+v, want user/project/local", view.Scopes)
	}
}

func TestRestoreArchivedMemoryRecoversFactForCurrentSession(t *testing.T) {
	isolateDesktopUserDirs(t)
	userDir := t.TempDir()
	cwd := t.TempDir()
	store := memory.StoreFor(userDir, cwd)
	first, err := store.SaveWithOptions(memory.Memory{
		Name: "restorable-fact", Description: "recover me", Body: "Recovered guidance.",
	}, memory.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := store.Archive(first.Memory.ID)
	if err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Memory: &memory.Set{Store: store, CWD: cwd, UserDir: userDir}}), "test-model")
	if _, err := app.RestoreArchivedMemory(archivePath); err != nil {
		t.Fatal(err)
	}
	view := app.Memory()
	if len(view.Facts) != 1 || view.Facts[0].ID != first.Memory.ID || view.Facts[0].Revision != 2 {
		t.Fatalf("restored memory view = %+v", view)
	}
	if len(view.Archives) != 0 {
		t.Fatalf("restored archive remained visible: %+v", view.Archives)
	}
}
