package checkpoint

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	fileenc "deepzora/internal/fileutil/encoding"
)

func TestFileRevertPreservesCapturedEncoding(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		name := "memory"
		if persisted {
			name = "cold"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "gbk.txt")
			original := fileenc.MustEncode("你好，旧内容\n", fileenc.GB18030)
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}
			dir := ""
			if persisted {
				dir = t.TempDir()
			}
			store := New(dir, root)
			store.Begin(0, "edit", 0, "current-turn")
			store.CaptureBefore(path, CaptureBeforeOpts{})
			// A later tool deliberately changes the encoding. Restore must use the
			// preimage's encoding, never guess from the workspace's current bytes.
			if err := os.WriteFile(path, []byte("新的 UTF-8 内容"), 0o640); err != nil {
				t.Fatal(err)
			}
			store.CaptureAfter(path, CaptureAfterOpts{})
			if persisted {
				store = New(dir, root)
			}
			plan, err := store.PrepareFileRevert(path, 0)
			if err != nil || !plan.CanFiles {
				t.Fatalf("prepare = %+v, %v", plan, err)
			}
			result, err := store.CommitFileRevert(plan.PlanID, ResolveOverwriteCheckpoint)
			if err != nil || !result.OK {
				t.Fatalf("commit = %+v, %v", result, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("restored bytes = %x, %v; want %x", got, err, original)
			}
		})
	}
}

func TestCheckpointBackupRejectsOldSchemasBeforeChangingStore(t *testing.T) {
	store := New("", t.TempDir())
	store.Begin(0, "current", 0, "current-turn")
	for _, old := range []string{`[null]`, `[{"turn":2}]`, `[{"schemaVersion":2,"turn":2}]`} {
		if err := store.RestoreCheckpointBackupPublic([]byte(old)); err == nil {
			t.Fatalf("accepted old backup %s", old)
		}
		if ids := store.TurnIDs(); len(ids) != 1 || ids[0] != "current-turn" {
			t.Fatalf("rejected backup changed current checkpoints: %v", ids)
		}
	}
}
