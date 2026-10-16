package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueryStorageGenerationFencesReplacementWithoutOpeningWriter(t *testing.T) {
	root, id, dir, manifest, log := currentRevisionFixture(t)
	persistence := NewFilesystemPersistence(root)
	service, err := NewService("local", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(t.Context()) })
	ref := SessionRef{HostID: "local", SessionID: id}
	original, err := service.Query().StorageGeneration(t.Context(), ref)
	if err != nil || original == "" {
		t.Fatalf("generation = %q, %v", original, err)
	}
	assertRevisionFixtureBytes(t, dir, manifest, log)
	writer, err := persistence.Open(id, ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := service.Query().StorageGeneration(t.Context(), ref); err != nil || got != original {
		t.Fatalf("read acquired writer: %q, %v", got, err)
	}
	if _, err := writer.Append(t.Context(), Batch{OperationID: "append", Events: []Event{{Kind: "message/complete", Payload: []byte(`{"message":{"id":"append","role":"user","content":"next"}}`)}}}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Query().StorageGeneration(t.Context(), ref); err != nil || got != original {
		t.Fatalf("append changed generation: %q, %v", got, err)
	}
	if _, err := service.Query().StorageGeneration(t.Context(), SessionRef{HostID: "foreign", SessionID: id}); err == nil {
		t.Fatal("foreign host was accepted")
	}
	if err := os.Rename(dir, filepath.Join(t.TempDir(), "original")); err != nil {
		t.Fatal(err)
	}
	replacement, err := persistence.Create(CreateOptions{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Query().StorageGeneration(t.Context(), ref); err != nil || got == original {
		t.Fatalf("replacement not fenced: %q, %v", got, err)
	}
}
