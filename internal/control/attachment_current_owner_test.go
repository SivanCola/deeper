package control

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/session"
	"deepzora/internal/sessioncontent"
)

func TestCurrentAttachmentStagingKeepsStoreWhenFirstSessionIsPublished(t *testing.T) {
	root := t.TempDir()
	service, err := session.NewService("local", session.NewFilesystemPersistence(filepath.Join(root, "current")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	oldDir := filepath.Join(root, "old-transcripts")
	executor := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := newOwnedTestController(t, Options{Executor: executor, SessionDir: oldDir, SessionService: service, ExclusiveSession: true, Sink: event.Discard})
	staged := ctrl.sessionContentStore()
	ref, err := staged.Put(t.Context(), bytes.NewBufferString("staged before first prompt"), sessioncontent.Metadata{MediaType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	bound := ctrl.sessionContentStore()
	if staged.Root() != bound.Root() {
		t.Fatalf("content owner changed from %q to %q", staged.Root(), bound.Root())
	}
	got, err := bound.ReadRange(t.Context(), ref, 0, ref.Bytes)
	if err != nil || string(got) != "staged before first prompt" {
		t.Fatalf("staged content = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(oldDir, ".content-v1")); !os.IsNotExist(err) {
		t.Fatalf("legacy content root was used: %v", err)
	}
}
