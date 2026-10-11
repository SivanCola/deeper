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
	legacyProbes := 0
	previous := leaseHeldByForeignRuntime
	leaseHeldByForeignRuntime = func(string) bool { legacyProbes++; return false }
	t.Cleanup(func() { leaseHeldByForeignRuntime = previous })
	done := srv.maybeAutoReclaimMirrored(route)
	if done != nil {
		<-done
		t.Error("live canonical writer must prevent an automatic reclaim")
	}
	if legacyProbes != 0 {
		t.Fatalf("identity route reached legacy filesystem lease probe %d times", legacyProbes)
	}
}

func TestAutoReclaimRejectsStaleMirrorOutsideCurrentSessionRoot(t *testing.T) {
	f := newOwnershipFixture(t)
	outside := filepath.Join(t.TempDir(), "old-root.jsonl")
	f.server.markMirrored(mirroredSession{path: outside, mirrorID: "old-root", lastContact: time.Now().Add(-2 * mirrorStaleAfter)})
	probes := 0
	previous := leaseHeldByForeignRuntime
	leaseHeldByForeignRuntime = func(string) bool { probes++; return false }
	t.Cleanup(func() { leaseHeldByForeignRuntime = previous })
	if done := f.server.maybeAutoReclaimMirrored(outside); done != nil {
		<-done
		t.Error("outside mirror must not begin filesystem recovery")
	}
	if probes != 0 {
		t.Fatalf("outside mirror reached filesystem lease probe %d times", probes)
	}
}
