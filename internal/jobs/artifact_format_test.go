package jobs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/event"
)

func TestTaskArtifactRejectsUnsupportedEvidenceWithoutRepair(t *testing.T) {
	for _, version := range []int{0, mutationEvidenceVersion + 1} {
		dir := t.TempDir()
		path := filepath.Join(ArtifactDir(dir), "task-1.json")
		if err := writeMeta(path, artifactMeta{ID: "task-1", Kind: "task", Status: Done, MutationEvidenceVersion: version}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readMeta(path); err == nil {
			t.Fatalf("accepted unsupported evidence version %d", version)
		}
		if _, err := ListArtifactViews(dir); err == nil {
			t.Fatal("machine listing accepted unsupported task metadata")
		}
		manager := NewManager(event.Discard)
		if err := manager.BindSession("session", dir); err == nil {
			t.Fatal("binding did not report unsupported metadata")
		}
		if _, _, found := manager.OutputForSession("session", "task-1"); found {
			t.Fatal("restored an incompatible task")
		}
		manager.Close()
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("repaired incompatible metadata: %v", err)
		}
	}
}
