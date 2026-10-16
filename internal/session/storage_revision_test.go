package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Build a current-format store and retain its bytes for non-mutation checks.
func currentRevisionFixture(t *testing.T) (string, string, string, []byte, []byte) {
	t.Helper()
	root, id := filepath.Join(t.TempDir(), "sessions"), "current-revision"
	persistence := NewFilesystemPersistence(root)
	writer, err := persistence.Create(CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(t.Context(), Batch{OperationID: "old-message", Events: []Event{{Kind: "message/complete", Payload: []byte(`{"message":{"id":"old","role":"user","content":"preserve original bytes"}}`)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, id)
	manifestPath := filepath.Join(dir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	logBytes, err := os.ReadFile(filepath.Join(dir, currentLogName))
	if err != nil {
		t.Fatal(err)
	}
	return root, id, dir, manifestBytes, logBytes
}

func assertRevisionFixtureBytes(t *testing.T, dir string, manifestBytes, logBytes []byte) {
	t.Helper()
	for name, want := range map[string][]byte{"manifest.json": manifestBytes, currentLogName: logBytes} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s bytes changed", name)
		}
	}
}

func TestStorageRevisionReadOnlyPreservesBytes(t *testing.T) {
	root, id, dir, manifestBytes, logBytes := currentRevisionFixture(t)
	persistence := NewFilesystemPersistence(root)
	reader, err := persistence.Open(id, ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertRevisionFixtureBytes(t, dir, manifestBytes, logBytes)
}

func TestStorageRevisionDamagedLogPreservesBytes(t *testing.T) {
	root, id, dir, manifestBytes, logBytes := currentRevisionFixture(t)
	logBytes[0] ^= 0xff
	if err := os.WriteFile(filepath.Join(dir, currentLogName), logBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := NewFilesystemPersistence(root).Open(id, ReadWrite)
	if err == nil {
		_ = writer.Close(context.Background())
		t.Fatal("damaged log was opened for writing")
	}
	assertRevisionFixtureBytes(t, dir, manifestBytes, logBytes)
}

func TestStorageRevisionExternalLeasePreservesBytes(t *testing.T) {
	root, id, dir, manifestBytes, logBytes := currentRevisionFixture(t)
	release, err := acquireSessionWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	writer, err := NewFilesystemPersistence(root).Open(id, ReadWrite)
	if writer != nil {
		_ = writer.Close(context.Background())
	}
	if !errors.Is(err, ErrWriterOwned) {
		t.Fatalf("open while owned: %v", err)
	}
	assertRevisionFixtureBytes(t, dir, manifestBytes, logBytes)
}

func TestStorageRevisionUnknownRejectedByReadersAndWriters(t *testing.T) {
	for _, mode := range []AccessMode{ReadOnly, ReadWrite} {
		root, id, dir, _, logBytes := currentRevisionFixture(t)
		manifestPath := filepath.Join(dir, "manifest.json")
		manifest, err := readStoredManifest(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		manifest.StorageRevision = 999
		if err := writeManifestFile(manifestPath, manifest); err != nil {
			t.Fatal(err)
		}
		manifestBytes, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := NewFilesystemPersistence(root).Open(id, mode)
		if handle != nil {
			_ = handle.Close(context.Background())
		}
		if !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatalf("mode=%v unknown revision: %v", mode, err)
		}
		assertRevisionFixtureBytes(t, dir, manifestBytes, logBytes)
	}
}
