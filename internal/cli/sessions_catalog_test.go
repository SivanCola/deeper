package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/session"
)

func snapshotCatalogSource(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[path] = string(data)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSessionsDiagnoseDoesNotRepairMissingMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	root := session.RootForSessionDir(dir)
	createCanonicalTestSession(t, root, "current", "preserve this message")
	cache := filepath.Join(root, ".query-cache")
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	before := snapshotCatalogSource(t, root)
	out := captureStdout(t, func() {
		if rc := sessionsCommand([]string{"diagnose", "--dir", dir, "--json"}); rc != 1 {
			t.Fatalf("diagnose rc = %d; want degraded", rc)
		}
	})
	var report sessionCatalogReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceSessions != 1 || report.UnindexedSessions != 1 || report.RefreshedSessions != 0 || report.FailedSessions != 0 {
		t.Fatalf("diagnosis = %+v", report)
	}
	if after := snapshotCatalogSource(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("diagnose changed source files or recreated the metadata cache")
	}
}

func TestSessionsCatalogReportsDamagedSessionAndContinues(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	root := session.RootForSessionDir(dir)
	createCanonicalTestSession(t, root, "healthy", "keep healthy content")
	createCanonicalTestSession(t, root, "damaged", "keep damaged content")
	manifest := filepath.Join(root, "damaged", "manifest.json")
	if err := os.WriteFile(manifest, []byte("broken manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshotCatalogSource(t, filepath.Join(root, "damaged"))
	out := captureStdout(t, func() {
		if rc := sessionsCommand([]string{"reindex", "--dir", dir, "--json"}); rc != 1 {
			t.Fatalf("reindex rc = %d; want partial failure", rc)
		}
	})
	var report sessionCatalogReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceSessions != 2 || report.FailedSessions != 1 || report.RefreshedSessions != 1 || report.IndexedSessions != 1 || len(report.Errors) != 1 {
		t.Fatalf("reindex report = %+v", report)
	}
	if after := snapshotCatalogSource(t, filepath.Join(root, "damaged")); !reflect.DeepEqual(before, after) {
		t.Fatal("reindex repaired or rewrote damaged source")
	}
}

func TestSessionsCleanupIsNotACommand(t *testing.T) {
	if rc := sessionsCommand([]string{"cleanup", "--apply"}); rc != 2 {
		t.Fatalf("retired cleanup command returned %d", rc)
	}
}

func TestSessionsCatalogReadsEveryPageAndReportsActiveWriter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions-v4")
	for i := range 101 {
		createCanonicalTestSession(t, root, fmt.Sprintf("session-%03d", i), "")
	}
	persistence := session.NewFilesystemPersistence(root)
	writer, err := persistence.Open("session-100", session.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	var report sessionCatalogReport
	inspectCurrentSessionCatalog(t.Context(), root, false, &report)
	if report.SourceSessions != 101 || report.ActiveSessions != 1 || report.FailedSessions != 0 || len(report.Errors) != 0 {
		t.Fatalf("catalog dropped a page or writer state: %+v", report)
	}
}

func TestCurrentSessionCatalogRootsIncludeDesktopAndDeduplicate(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	roots := currentSessionCatalogRoots(nil)
	found := false
	for _, root := range roots {
		if root == config.DesktopSessionStoreDir() {
			found = true
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("root discovery created %s: %v", root, err)
		}
	}
	if !found {
		t.Fatalf("Desktop store is missing from %v", roots)
	}
	dir := filepath.Join(t.TempDir(), "sessions")
	if got := currentSessionCatalogRoots([]string{dir, dir + string(filepath.Separator)}); len(got) != 1 || got[0] != session.RootForSessionDir(dir) {
		t.Fatalf("duplicate roots: %v", got)
	}
}
