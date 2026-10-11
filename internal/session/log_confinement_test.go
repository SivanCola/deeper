package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func v4LogConfinementFixture(t *testing.T) (string, []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "confined")
	store, err := CreateStore(dir, "confined")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(t.Context()) })
	if _, err := store.AppendBatch(t.Context(), "seed", []Event{{Kind: "diagnostic", Payload: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	frames, err := os.ReadFile(filepath.Join(dir, currentLogName))
	if err != nil {
		t.Fatal(err)
	}
	return dir, frames
}

func TestV4LogValidationRejectsEscapingSymlinks(t *testing.T) {
	for _, kind := range []string{"absolute", "relative", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir, frames := v4LogConfinementFixture(t)
			outside := filepath.Join(t.TempDir(), "outside.frames")
			if kind != "missing" {
				if err := os.WriteFile(outside, frames, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			target := outside
			if kind == "relative" {
				var err error
				target, err = filepath.Rel(dir, outside)
				if err != nil {
					t.Fatal(err)
				}
			}
			log := filepath.Join(dir, currentLogName)
			if err := os.Remove(log); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, log); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			manifest := filepath.Join(dir, "manifest.json")
			before, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := Open(dir, "confined")
			if opened != nil {
				_ = opened.Close(t.Context())
			}
			if err == nil {
				t.Fatal("writer followed a log symlink outside its session directory")
			}
			if after, err := os.ReadFile(manifest); err != nil || !bytes.Equal(before, after) {
				t.Fatalf("rejected open changed the manifest: %v", err)
			}
			after, err := os.ReadFile(outside)
			if kind == "missing" {
				if !os.IsNotExist(err) {
					t.Fatalf("rejected open created an outside log: %v", err)
				}
			} else if err != nil || !bytes.Equal(frames, after) {
				t.Fatalf("rejected open changed the outside log: %v", err)
			}
		})
	}
}

func TestV4LogValidationAcceptsConfinedSymlink(t *testing.T) {
	dir, _ := v4LogConfinementFixture(t)
	log := filepath.Join(dir, currentLogName)
	if err := os.Rename(log, filepath.Join(dir, "confined.frames")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("confined.frames", log); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store, err := Open(dir, "confined")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(t.Context()) })
	if _, err := store.AppendBatch(t.Context(), "continue", []Event{{Kind: "diagnostic", Payload: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	commits, err := Replay(dir, nil)
	if err != nil || len(commits) != 2 {
		t.Fatalf("confined log did not round trip: %d commits, %v", len(commits), err)
	}
}
