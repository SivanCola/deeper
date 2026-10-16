package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentCatalogStatusAndExplicitRepairLeaveSourceUntouched(t *testing.T) {
	root := t.TempDir()
	service, err := NewService("local", NewFilesystemPersistence(root))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	service.MetadataOnlyListings()
	runtime, err := service.Create(t.Context(), CreateOptions{SessionID: "current"})
	if err != nil {
		t.Fatal(err)
	}
	appendRecoveryTestMessage(t, runtime.Session(), "message", "current searchable title")
	ref := runtime.Ref()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, ref.SessionID, "events.frames")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(catalogMetadataPath(filepath.Join(root, ".query-cache", ref.SessionID))); err != nil {
		t.Fatal(err)
	}
	query := service.Query()
	info, err := query.Stat(t.Context(), ref)
	if err != nil || info.MetadataStatus == MetadataReady {
		t.Fatalf("missing cache: %+v %v", info, err)
	}
	status := query.CatalogState()
	if status.Pending != 1 || status.Failed != 0 {
		t.Fatalf("pending metadata: %+v", status)
	}
	select {
	case <-query.CatalogChanges():
	default:
		t.Fatal("pending status not published")
	}
	if _, err := query.Stat(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if query.CatalogState().Revision != status.Revision {
		t.Fatal("unchanged observation published another revision")
	}
	info, err = query.RefreshMetadata(t.Context(), ref)
	if err != nil || info.MetadataStatus != MetadataReady || info.Preview != "current searchable title" {
		t.Fatalf("repair current metadata: %+v %v", info, err)
	}
	afterStatus := query.CatalogState()
	if afterStatus.Pending != 0 || afterStatus.Failed != 0 || afterStatus.Revision <= status.Revision {
		t.Fatalf("repair status: %+v", afterStatus)
	}
	select {
	case <-query.CatalogChanges():
	default:
		t.Fatal("repair completion not published")
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("metadata repair changed current source: %v", err)
	}
}
