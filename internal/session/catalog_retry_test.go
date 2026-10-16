package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitMetadataRepairClearsFailureForLaterAutomaticRepair(t *testing.T) {
	root := t.TempDir()
	service, err := NewService("local", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	ref := runtime.Ref()
	appendRecoveryTestMessage(t, runtime.Session(), "message", "current retry fixture")
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(root, ".query-cache", ref.SessionID)
	if err := os.Rename(cacheDir, cacheDir+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheDir, []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	query := service.Query()
	if _, err := query.Stat(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	query.rebuildWG.Wait()
	if status := query.CatalogState(); status.Failed != 1 {
		t.Fatalf("repair failure not observed: %+v", status)
	}
	if err := os.Remove(cacheDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cacheDir+".saved", cacheDir); err != nil {
		t.Fatal(err)
	}
	if _, err := query.RefreshMetadata(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(catalogMetadataPath(cacheDir)); err != nil {
		t.Fatal(err)
	}
	info, err := query.Stat(t.Context(), ref)
	if err != nil || info.MetadataStatus == MetadataFailed {
		t.Fatalf("old failure blocked new repair: %+v, %v", info, err)
	}
	query.rebuildWG.Wait()
	info, err = query.Stat(t.Context(), ref)
	if err != nil || info.MetadataStatus != MetadataReady {
		t.Fatalf("subsequent automatic repair failed: %+v, %v", info, err)
	}
}
