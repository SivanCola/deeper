package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoricalCodecManifestBoundaries(t *testing.T) {
	for _, prefix := range []string{"deepzora", "deeper", "reasonix"} {
		for _, format := range []string{"linear/v3", "linear/v3.1", "events/v3"} {
			codec := prefix + ".session." + format
			if !supportedStoredManifest(Manifest{SchemaVersion: 3, Codec: codec}) {
				t.Errorf("rejected historical format %q", codec)
			}
			if supportedStoredManifest(Manifest{SchemaVersion: 4, Codec: codec}) {
				t.Errorf("accepted mismatched schema for %q", codec)
			}
		}
		for revision := range StorageRevision + 1 {
			manifest := Manifest{SchemaVersion: 4, Codec: prefix + ".session.linear/v4", StorageRevision: revision}
			if !supportedStoredManifest(manifest) {
				t.Errorf("rejected supported v4 manifest: %+v", manifest)
			}
			manifest.StorageRevision = StorageRevision + 1
			if supportedStoredManifest(manifest) {
				t.Errorf("accepted future storage revision: %+v", manifest)
			}
		}
	}
	for _, codec := range []string{"other.session.linear/v4", "reasonix.session.linear/v5", "deepzora.session.events/v4"} {
		if supportedStoredManifest(Manifest{SchemaVersion: 4, Codec: codec, StorageRevision: StorageRevision}) {
			t.Errorf("accepted unknown format %q", codec)
		}
	}
}

func TestV4CodecRejectsMixedIdentities(t *testing.T) {
	var log bytes.Buffer
	for i, codec := range []string{V4Codec, "reasonix.session.linear/v4"} {
		commit := v4TestCommit(nil)
		commit.Codec, commit.FirstSequence = codec, uint64(i+1)
		commit.Events[0].Sequence = commit.FirstSequence
		if _, err := encodeV4Commits(t.Context(), &log, nil, []Commit{commit}); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanV4CommitFile(t.Context(), bytes.NewReader(log.Bytes()), 0, 1, nil, nil, nil); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("accepted mixed wire identities: %v", err)
	}
}

func TestHistoricalV4WriterRejectsManifestCodecMismatch(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join("..", "..", "desktop", "testdata", "session-history-upgrade", "1.38.11", "canonical")
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "canonical-complete")
	path := filepath.Join(dir, "manifest.json")
	manifest, err := readStoredManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Codec = V4Codec
	if err := writeManifestFile(path, manifest); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime, err := Open(dir, manifest.SessionID); !errors.Is(err, ErrUnsupportedVersion) {
		if runtime != nil {
			_ = runtime.Close(t.Context())
		}
		t.Fatalf("writer accepted a mislabeled historical log: %v", err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, original) {
		t.Fatalf("failed open rewrote the manifest: %v", err)
	}
}

func TestReasonixLinearStoreRetainsOriginalCodec(t *testing.T) {
	for _, prefix := range []string{"deeper", "reasonix"} {
		for _, format := range []string{"linear/v3", "linear/v3.1", "events/v3"} {
			codec := prefix + ".session." + format
			t.Run(codec, func(t *testing.T) { testNativeLinearStore(t, codec) })
		}
	}
}

func TestDeeperV4StorePreservesIdentityAfterRename(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "legacy")
	runtime, err := CreateStore(dir, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	manifest, err := readStoredManifest(path)
	if err != nil || manifest.Codec != "deepzora.session.linear/v4" {
		t.Fatalf("new namespace: %+v, %v", manifest, err)
	}
	manifest.Codec = "deeper.session.linear/v4"
	if err := writeManifestFile(path, manifest); err != nil {
		t.Fatal(err)
	}
	batch := Batch{OperationID: "after-rename", Events: []Event{{Kind: "diagnostic", Payload: json.RawMessage(`{"text":"preserve history"}`)}}}
	var first Commit
	for restart := range 2 {
		runtime, err := Open(dir, "legacy")
		if err != nil {
			t.Fatal(err)
		}
		commit, appendErr := runtime.Append(t.Context(), batch)
		closeErr := runtime.Close(t.Context())
		if appendErr != nil || closeErr != nil || commit.Codec != manifest.Codec || (restart > 0 && commit.ID != first.ID) {
			t.Fatalf("restart %d: %+v, append=%v close=%v", restart, commit, appendErr, closeErr)
		}
		first = commit
	}
	commits, err := Replay(dir, nil)
	if err != nil || len(commits) != 1 || commits[0].Codec != manifest.Codec || !bytes.Equal(commits[0].Events[0].Payload, batch.Events[0].Payload) {
		t.Fatalf("legacy round trip: %+v, %v", commits, err)
	}
}

func TestHistoricalV4StorePreservesCodecAcrossAppendAndRestart(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join("..", "..", "desktop", "testdata", "session-history-upgrade", "1.38.11", "canonical")
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "canonical-complete")
	logPath := filepath.Join(dir, currentLogName)
	original, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	commits, err := Replay(dir, nil)
	if err != nil || len(commits) == 0 || commits[0].Codec != "reasonix.session.linear/v4" {
		t.Fatalf("historical replay: %d commits, %v", len(commits), err)
	}
	payload, _ := json.Marshal(map[string]string{"text": string(bytes.Repeat([]byte("x"), 2*v4InlinePayloadBytes))})
	batch := Batch{OperationID: "deepzora-continue-old", Events: []Event{{Kind: "diagnostic", Payload: payload}}}
	var first Commit
	for restart := range 2 {
		runtime, err := Open(dir, "canonical-complete")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close(context.Background()) })
		commit, err := runtime.Append(t.Context(), batch)
		if err != nil || commit.Codec != "reasonix.session.linear/v4" || (restart > 0 && commit.ID != first.ID) {
			t.Fatalf("append after restart %d: %+v, %v", restart, commit, err)
		}
		first = commit
		if err := runtime.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
		if err != nil || manifest.Codec != "reasonix.session.linear/v4" {
			t.Fatalf("source codec changed: %+v, %v", manifest, err)
		}
	}
	replayed, err := Replay(dir, nil)
	if err != nil || len(replayed) != len(commits)+1 || replayed[len(commits)].Codec != first.Codec || !bytes.Equal(replayed[len(commits)].Events[0].Payload, payload) {
		t.Fatalf("round trip changed history or duplicated operation: %d commits, %v", len(replayed), err)
	}
	complete, err := os.ReadFile(logPath)
	if err != nil || !bytes.HasPrefix(complete, original) {
		t.Fatalf("historical log prefix changed: %v", err)
	}
	if err := os.WriteFile(logPath, append(bytes.Clone(complete), v4FrameMagic[:]...), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(dir, "canonical-complete")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repaired, err := os.ReadFile(logPath); err != nil || !bytes.Equal(repaired, complete) {
		t.Fatalf("torn-tail repair changed durable bytes: %v", err)
	}
}
