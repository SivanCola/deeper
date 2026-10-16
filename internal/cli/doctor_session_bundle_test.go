package cli

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"deepzora/internal/session"
)

func TestWriteDoctorSessionBundleUsesCurrentEventsWithoutTakingWriter(t *testing.T) {
	root := t.TempDir()
	const credential = "sk-current-bundle-secret-123456789"
	service, ref := createDoctorBundleSession(t, root, "keep this support evidence\nDEEPSEEK_API_KEY="+credential)
	writer, err := session.NewFilesystemPersistence(root).Open(ref.SessionID, session.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close(context.Background()) })
	before := currentBundleSourceBytes(t, filepath.Join(root, ref.SessionID))
	out := filepath.Join(t.TempDir(), "diag.zip")
	result, err := writeDoctorSessionBundle(doctorSessionBundleOptions{Version: "test-version", Service: service, Ref: ref, OutputPath: out, Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != out {
		t.Fatalf("bundle path = %q", result.Path)
	}
	files := zipFiles(t, out)
	for name, body := range files {
		if strings.Contains(string(body), credential) {
			t.Fatalf("credential leaked in %s", name)
		}
	}
	if len(files) != 3 || files["doctor.json"] == nil || files["session-diagnostics.json"] == nil {
		t.Fatalf("bundle entries = %v", keys(files))
	}
	var manifest doctorSessionBundleManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "test-version" || manifest.Snapshot.Ref != ref || manifest.Snapshot.DurableThrough == 0 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if !strings.Contains(string(files["session-diagnostics.json"]), "keep this support evidence") {
		t.Fatal("bundle omitted current durable transcript")
	}
	if strings.Contains(string(files["manifest.json"]), root) {
		t.Fatal("manifest leaked storage path")
	}
	if !session.ProbeWriterHeld(filepath.Join(root, ref.SessionID)) {
		t.Fatal("diagnosis interrupted the current writer")
	}
	if after := currentBundleSourceBytes(t, filepath.Join(root, ref.SessionID)); !reflect.DeepEqual(before, after) {
		t.Fatal("bundle changed current session files")
	}
}

func TestWriteDoctorSessionBundlePreservesDamagedTailAndReportsReadablePrefix(t *testing.T) {
	root := t.TempDir()
	service, ref := createDoctorBundleSession(t, root, "readable prefix")
	path := filepath.Join(root, ref.SessionID, "events.frames")
	writer, err := session.NewFilesystemPersistence(root).Open(ref.SessionID, session.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.AppendBatch(t.Context(), "later", []session.Event{{Kind: "diagnostic", Payload: json.RawMessage(`{"message":"checksum-test"}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the final complete frame's checksum, leaving the earlier commit
	// intact. An incomplete append is deliberately ignored by the reader and
	// would not exercise unreadable committed evidence.
	if len(body) == 0 {
		t.Fatal("empty event store")
	}
	body[len(body)-1] ^= 0xff
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	before := currentBundleSourceBytes(t, filepath.Join(root, ref.SessionID))
	out := filepath.Join(t.TempDir(), "diagnostic.zip")
	if _, err := writeDoctorSessionBundle(doctorSessionBundleOptions{Service: service, Ref: ref, OutputPath: out}); err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, out)
	var manifest doctorSessionBundleManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Snapshot.ReadIncomplete {
		t.Fatal("damaged source was not reported")
	}
	if !strings.Contains(string(files["session-diagnostics.json"]), "readable prefix") {
		t.Fatal("readable evidence was lost")
	}
	if after := currentBundleSourceBytes(t, filepath.Join(root, ref.SessionID)); !reflect.DeepEqual(before, after) {
		t.Fatal("bundle repaired damaged source")
	}
}

func currentBundleSourceBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		// The writer lock is coordination state, not persisted session content.
		// Windows prevents reading its locked range; ownership is checked with
		// ProbeWriterHeld independently of this source-byte comparison.
		if path == filepath.Join(root, "writer.lock") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err == nil {
			out[path] = string(body)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSessionBundleCannotOverwriteSource(t *testing.T) {
	root := t.TempDir()
	service, ref := createDoctorBundleSession(t, root, "")
	dir := filepath.Join(root, ref.SessionID)
	before := currentBundleSourceBytes(t, dir)
	for _, target := range []string{filepath.Join(dir, "manifest.json"), filepath.Join(dir, "nested", "diagnostic.zip")} {
		if _, err := writeDoctorSessionBundle(doctorSessionBundleOptions{Service: service, Ref: ref, OutputPath: target}); err == nil || !strings.Contains(err.Error(), "outside the source") {
			t.Fatalf("source output accepted: %s: %v", target, err)
		}
	}
	if after := currentBundleSourceBytes(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected export changed source")
	}
}

func zipFiles(t *testing.T, path string) map[string][]byte {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string][]byte{}
	for _, file := range zr.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[file.Name] = body
	}
	return out
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func createDoctorBundleSession(t *testing.T, root, prompt string) (*session.Service, session.SessionRef) {
	t.Helper()
	createCanonicalTestSession(t, root, "bundle-session", prompt)
	service, err := session.NewService("local", session.NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return service, session.SessionRef{HostID: "local", SessionID: "bundle-session"}
}
