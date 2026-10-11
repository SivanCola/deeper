package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedFilesCopyOnlyCurrentArtifactRoots(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	for _, name := range []string{"attachments", "assets", "legacy"} {
		dir := filepath.Join(source, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyOwnedSessionFiles(t.Context(), source, target); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"attachments", "assets"} {
		if _, err := os.Stat(filepath.Join(target, name, "sample.txt")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "legacy")); !os.IsNotExist(err) {
		t.Fatalf("historical artifact root copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "legacy", "sample.txt")); err != nil {
		t.Fatal("historical source changed", err)
	}
}

func TestStoredManifestAcceptsOnlyCurrentCodecAndRevision(t *testing.T) {
	for _, codec := range []string{V4Codec, "deeper.session.linear/v4", "reasonix.session.linear/v4", "deepzora.session.linear/v3.1", "deepzora.session.events/v3"} {
		for revision := 0; revision <= StorageRevision+1; revision++ {
			manifest := Manifest{SchemaVersion: SchemaVersion, StorageRevision: revision, Codec: codec}
			want := codec == V4Codec && revision == StorageRevision
			if got := manifest.Supported(); got != want {
				t.Fatalf("manifest %+v: supported=%t", manifest, got)
			}
			if want {
				continue
			}
			dir := t.TempDir()
			manifest.SessionID = "rejected"
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(dir, manifest.SessionID)
			if opened != nil {
				_ = opened.Close(t.Context())
			}
			if !errors.Is(err, ErrUnsupportedVersion) {
				t.Fatalf("open %+v: %v", manifest, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, raw) {
				t.Fatal("unsupported manifest changed")
			}
		}
	}
}

func TestHistoricalImportEventRejected(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "current"), "current")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(t.Context())
	_, err = s.Append(t.Context(), Batch{OperationID: "historical-import", Events: []Event{{Kind: "legacy/import", Required: true, Payload: json.RawMessage(`{"messages":[]}`)}}})
	if err == nil {
		t.Fatal("historical import was accepted")
	}
}
