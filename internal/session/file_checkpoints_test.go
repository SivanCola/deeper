package session

import (
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/checkpoint"
)

func TestFileCheckpointsExportImportKeepsPreimagesAndTurnIdentity(t *testing.T) {
	service, _, runtime := newSourceService(t, "file-checkpoints")
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	store := runtime.Session().FileCheckpoints(root)
	store.Begin(0, "edit", 0, "turn-original")
	store.CaptureBefore(path, checkpoint.CaptureBeforeOpts{})
	if err := os.WriteFile(path, []byte("modified"), 0o640); err != nil {
		t.Fatal(err)
	}
	store.CaptureAfter(path, checkpoint.CaptureAfterOpts{})
	appendCompletedTurn(t, runtime, "turn-original", "message")
	exported := filepath.Join(t.TempDir(), "export")
	if err := service.Export(t.Context(), runtime.Ref(), exported); err != nil {
		t.Fatal(err)
	}
	imported, err := service.ImportWithHeader(t.Context(), exported, CreateOptions{SessionID: "imported-checkpoints"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := service.Open(t.Context(), imported)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Release(t.Context()) })
	copied := binding.Runtime().Session().FileCheckpoints(root)
	if copied.TurnIDs()[0] != "turn-original" {
		t.Fatalf("imported turn IDs: %v", copied.TurnIDs())
	}
	plan, err := copied.PrepareRewind(0, checkpoint.RewindCode, 0, 0, true)
	if err != nil || !plan.CanFiles {
		t.Fatalf("imported file rewind = %+v, %v", plan, err)
	}
	result, err := copied.CommitRewind(plan.PlanID, nil, nil)
	if err != nil || !result.OK {
		t.Fatalf("restore imported preimage = %+v, %v", result, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "original" {
		t.Fatalf("imported restore = %q, %v", got, err)
	}
}

func TestFileCheckpointsExportRefusesActiveMutation(t *testing.T) {
	service, _, runtime := newSourceService(t, "file-checkpoint-busy")
	store := runtime.Session().FileCheckpoints(t.TempDir())
	if err := store.Barrier().EnterWrite(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "export")
	err := service.Export(t.Context(), runtime.Ref(), destination)
	store.Barrier().ExitWrite()
	if err == nil {
		t.Fatal("export copied file checkpoints during an active mutation")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("busy export published a destination: %v", err)
	}
	if err := service.Export(t.Context(), runtime.Ref(), destination); err != nil {
		t.Fatal(err)
	}
}

func TestForkKeepsParentFileCheckpointsAndStartsIndependentChildUndo(t *testing.T) {
	service, _, parent := newSourceService(t, "fork-file-owner")
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	edit := func(store *checkpoint.Store, turnID, text string) {
		t.Helper()
		store.Begin(0, "edit", 0, turnID)
		store.CaptureBefore(path, checkpoint.CaptureBeforeOpts{})
		if err := os.WriteFile(path, []byte(text), 0o640); err != nil {
			t.Fatal(err)
		}
		store.CaptureAfter(path, checkpoint.CaptureAfterOpts{})
	}
	undo := func(store *checkpoint.Store, want string) {
		t.Helper()
		plan, err := store.PrepareRewind(0, checkpoint.RewindCode, 0, 0, true)
		if err != nil || !plan.CanFiles {
			t.Fatalf("prepare file undo: %+v, %v", plan, err)
		}
		result, err := store.CommitRewind(plan.PlanID, nil, nil)
		if err != nil || !result.OK {
			t.Fatalf("commit file undo: %+v, %v", result, err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("file undo = %q, %v; want %q", got, err, want)
		}
	}
	parentFiles := parent.Session().FileCheckpoints(root)
	edit(parentFiles, "parent-turn", "parent edit")
	appendCompletedTurn(t, parent, "parent-turn", "parent-message")
	child, err := service.Fork(t.Context(), parent.Ref(), "parent-turn", "fork-file-child")
	if err != nil {
		t.Fatal(err)
	}
	// A conversation fork starts a separate file-undo history. It must neither
	// move the parent's preimages nor grant the child ownership of prior edits.
	childFiles := child.Session().FileCheckpoints(root)
	if len(childFiles.List()) != 0 || parentFiles.TurnIDs()[0] != "parent-turn" {
		t.Fatalf("fork changed file ownership: parent=%v child=%v", parentFiles.TurnIDs(), childFiles.TurnIDs())
	}
	edit(childFiles, "child-turn", "child edit")
	appendCompletedTurn(t, child, "child-turn", "child-message")
	undo(childFiles, "parent edit")
	undo(parentFiles, "original")
}
