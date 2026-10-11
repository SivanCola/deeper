package serve

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestAutoReclaimIdentityProbesCanonicalWriterNotLegacyPath(t *testing.T) {
	service, err := session.NewService("test-host", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	created, err := service.Create(t.Context(), session.CreateOptions{SessionID: "held-identity"})
	if err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{SessionDir: t.TempDir(), SessionService: service})
	srv := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	t.Cleanup(srv.Close)
	route := remoteSessionIDQueryPrefix + created.Ref().SessionID
	srv.markMirrored(mirroredSession{path: route, mirrorID: "identity-writer", lastContact: time.Now().Add(-2 * mirrorStaleAfter)})
	done := srv.maybeAutoReclaimMirrored(route)
	if done != nil {
		<-done
		t.Error("live canonical writer must prevent an automatic reclaim")
	}
}

func TestAutoReclaimRejectsStaleMirrorOutsideCurrentSessionRoot(t *testing.T) {
	srv, _, _, _ := newExclusiveSessionServe(t)
	outside := filepath.Join(t.TempDir(), "old-root.jsonl")
	srv.markMirrored(mirroredSession{path: outside, mirrorID: "old-root", lastContact: time.Now().Add(-2 * mirrorStaleAfter)})
	if done := srv.maybeAutoReclaimMirrored(outside); done != nil {
		<-done
		t.Error("outside mirror must not begin filesystem recovery")
	}
	if _, _, err := srv.resolveSessionIdentity(outside); err == nil {
		t.Fatal("filesystem route must be rejected before any recovery work")
	}
}

func TestAutoReclaimIdentityReopensReleasedWriter(t *testing.T) {
	srv, ctrl, service, _ := newExclusiveSessionServe(t)
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "released-writer"})
	if err != nil {
		t.Fatal(err)
	}
	ref := target.Ref()
	if err := service.Close(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	route := remoteSessionIDQueryPrefix + ref.SessionID
	srv.markMirrored(mirroredSession{path: route, mirrorID: "released-writer", lastContact: time.Now().Add(-2 * mirrorStaleAfter)})
	done := srv.maybeAutoReclaimMirrored(route)
	if done == nil {
		t.Fatal("released canonical writer must start recovery")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic identity recovery did not finish")
	}
	if current, bound := ctrl.SessionRef(); !bound || current != ref {
		t.Fatalf("recovered foreground = %+v, bound %v; want %+v", current, bound, ref)
	}
	if _, mirrored := srv.mirroredEntry(route); mirrored {
		t.Fatal("recovered identity must clear its mirror")
	}
}
