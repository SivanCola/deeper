package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentImportPublishesReadableHistoryWithoutOpeningRuntime(t *testing.T) {
	source, _, runtime := newSourceService(t, "current-archive")
	appendCompletedTurn(t, runtime, "turn", "historical-answer")
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := source.Export(t.Context(), runtime.Ref(), bundle); err != nil {
		t.Fatal(err)
	}
	identity, err := os.ReadFile(filepath.Join(bundle, storageIdentityName))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"current-archive", "remapped-archive"} {
		t.Run(id, func(t *testing.T) {
			target, err := NewService("desktop", NewFilesystemPersistence(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = target.Shutdown(t.Context()) })
			ref, err := target.ImportWithHeader(t.Context(), bundle, CreateOptions{SessionID: id, CWD: filepath.Clean("/workspace"), Origin: SessionOriginCanonicalImport})
			if err != nil {
				t.Fatal(err)
			}
			page := windowReady(t, target.Query(), ref, HistoryWindowRequest{Anchor: "newest", Limit: 10})
			if len(page.Messages) != 1 || page.Messages[0].MessageID != "historical-answer" {
				t.Fatalf("imported message changed: %+v", page)
			}
			if _, live := target.Runtime(ref); live {
				t.Fatal("history read started a runtime")
			}
			if current, err := os.ReadFile(filepath.Join(bundle, storageIdentityName)); err != nil || !bytes.Equal(current, identity) {
				t.Fatalf("import modified the source identity: %v", err)
			}
		})
	}
}

func TestImportRejectsArchiveWithoutCurrentStorageIdentity(t *testing.T) {
	source, _, runtime := newSourceService(t, "old-archive")
	appendCompletedTurn(t, runtime, "turn", "answer")
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := source.Export(t.Context(), runtime.Ref(), bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bundle, storageIdentityName)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old-archive", "remapped-archive"} {
		root := t.TempDir()
		target, err := NewService("desktop", NewFilesystemPersistence(root))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = target.Shutdown(t.Context()) })
		if _, err := target.ImportWithHeader(t.Context(), bundle, CreateOptions{SessionID: id}); err == nil {
			t.Fatal("archive without current identity was accepted")
		}
		if _, err := os.Stat(filepath.Join(root, id)); !os.IsNotExist(err) {
			t.Fatalf("failed import published a session: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(bundle, storageIdentityName)); !os.IsNotExist(err) {
		t.Fatalf("rejected import repaired the old archive: %v", err)
	}
}
