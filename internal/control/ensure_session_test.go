package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

func TestEnsureSessionPublishesOneCurrentIdentityAndPreservesKind(t *testing.T) {
	root := t.TempDir()
	service, err := session.NewService("test", session.NewFilesystemPersistence(filepath.Join(root, "current")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := newOwnedTestController(t, Options{Executor: exec, SessionService: service, ExclusiveSession: true, SessionDir: filepath.Join(root, "old")})
	var wg sync.WaitGroup
	refs := make(chan session.SessionRef, 8)
	for range 8 {
		wg.Go(func() {
			ref, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{Kind: session.SessionKindHeadlessRun})
			if err != nil {
				t.Error(err)
				return
			}
			refs <- ref
		})
	}
	wg.Wait()
	close(refs)
	ref, bound := ctrl.SessionRef()
	if !bound {
		t.Fatal("no current identity")
	}
	for got := range refs {
		if got != ref {
			t.Fatalf("competing identities: %+v, %+v", ref, got)
		}
	}
	page, err := service.Query().List(t.Context(), "", 100)
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("current catalog: %+v, %v", page, err)
	}
	info, err := service.Query().Stat(t.Context(), ref)
	if err != nil || info.Kind != session.SessionKindHeadlessRun {
		t.Fatalf("headless kind: %+v, %v", info, err)
	}
	if again, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{SessionID: "replacement"}); err != nil || again != ref {
		t.Fatalf("ensure replaced a bound identity: %+v, %v", again, err)
	}
	if _, err := os.Stat(filepath.Join(root, "old")); !os.IsNotExist(err) {
		t.Fatalf("created legacy path: %v", err)
	}
}

func TestEnsureSessionReturnsFailureWithoutAllocatingLegacyFiles(t *testing.T) {
	root := t.TempDir()
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := newOwnedTestController(t, Options{Executor: exec, SessionDir: root})
	if _, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{}); err == nil {
		t.Fatal("missing service accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing service allocated files: %v, %v", entries, err)
	}
	service, err := session.NewService("test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	ctrl = newOwnedTestController(t, Options{Executor: agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard), SessionService: service, ExclusiveSession: true})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ctrl.EnsureSession(ctx, session.CreateOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled creation: %v", err)
	}
	if _, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{SessionID: "../invalid"}); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if _, bound := ctrl.SessionRef(); bound {
		t.Fatal("failed creation published an identity")
	}
	if _, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{SessionID: "retry"}); err != nil {
		t.Fatalf("failed creation prevented retry: %v", err)
	}
	ctrl.Close()
	<-ctrl.Closed()
	if _, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{}); !errors.Is(err, session.ErrSessionNotRunning) {
		t.Fatalf("closed controller accepted ensure: %v", err)
	}
}

func TestEnsureSessionRejectsUnboundConversationWithoutDroppingIt(t *testing.T) {
	service, err := session.NewService("test", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.CloseAll(context.Background()) })
	saved := agent.NewSession("system")
	saved.Add(provider.Message{Role: provider.RoleUser, Content: "unsaved input"})
	ctrl := newOwnedTestController(t, Options{Executor: agent.New(nil, nil, saved, agent.Options{}, event.Discard), SessionService: service, ExclusiveSession: true})
	if _, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{}); err == nil {
		t.Fatal("unbound conversation was replaced")
	}
	if got := saved.Snapshot(); len(got) != 2 || got[1].Content != "unsaved input" {
		t.Fatalf("unsaved input changed: %+v", got)
	}
}

type ensureBlockedPersistence struct {
	session.SessionPersistence
	entered chan struct{}
	release chan struct{}
}

func (p *ensureBlockedPersistence) Create(options session.CreateOptions) (*session.Session, error) {
	close(p.entered)
	<-p.release
	return p.SessionPersistence.Create(options)
}

func TestEnsureSessionCannotPublishAfterClose(t *testing.T) {
	persistence := &ensureBlockedPersistence{
		SessionPersistence: session.NewFilesystemPersistence(t.TempDir()),
		entered:            make(chan struct{}), release: make(chan struct{}),
	}
	service, err := session.NewService("test", persistence)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	ctrl := newOwnedTestController(t, Options{
		Executor:       agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard),
		SessionService: service, ExclusiveSession: true,
	})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(persistence.release) }) })
	created := make(chan error, 1)
	go func() {
		_, err := ctrl.EnsureSession(t.Context(), session.CreateOptions{})
		created <- err
	}()
	select {
	case <-persistence.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("creation did not reach persistence")
	}
	go ctrl.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctrl.mu.Lock()
		closed := ctrl.closed
		ctrl.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not seal admission")
		}
		time.Sleep(time.Millisecond)
	}
	release.Do(func() { close(persistence.release) })
	select {
	case err := <-created:
		if !errors.Is(err, session.ErrSessionNotRunning) {
			t.Fatalf("creation after Close = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closed creation did not finish")
	}
	select {
	case <-ctrl.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join creation")
	}
	if _, bound := ctrl.SessionRef(); bound {
		t.Fatal("closed controller published a late session")
	}
}
