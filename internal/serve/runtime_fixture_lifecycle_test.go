package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestRegisterDetachedRevalidatesIdentityAtPublication(t *testing.T) {
	server, ctrl, service, _ := newExclusiveSessionServeWithOptions(t, func(opts *control.Options) {
		opts.Runner = blockingRunner{}
	})
	defer server.Close()
	target, err := service.Create(t.Context(), session.CreateOptions{SessionID: "replacement"})
	if err != nil {
		t.Fatal(err)
	}
	tag := NewSessionTagSink(server.bc)
	server.RegisterSessionTag(ctrl, tag)
	started, release := make(chan struct{}), make(chan struct{})
	registerDetachedHookForTest = func() { close(started); <-release }
	t.Cleanup(func() { registerDetachedHookForTest = nil })
	result := make(chan *detachedSession, 1)
	go func() { detached, _ := server.registerDetached(ctrl, tag); result <- detached }()
	<-started
	if _, err := ctrl.OpenSession(t.Context(), target.Ref()); err != nil {
		t.Fatal(err)
	}
	ctrl.Submit("keep running")
	waitRunning(t, ctrl)
	close(release)
	detached := <-result
	canonical := remoteSessionIDQueryPrefix + target.Ref().SessionID
	server.detachedMu.Lock()
	registered := server.detached[canonical]
	server.detachedMu.Unlock()
	if detached == nil {
		t.Fatal("current identity was not detached")
	}
	if detached.path != canonical || registered != detached {
		t.Fatalf("detached publication path = %q entry=%v, want %q", detached.path, registered == detached, canonical)
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
	server.CloseBackground()
}

func TestServeSwitchEffortUsesModelRefForDuplicateModelNames(t *testing.T) {
	writeServeModelConfig(t)

	server, _, _, _ := newExclusiveSessionServeWithOptions(t, func(opts *control.Options) {
		opts.Label, opts.ModelRef = "shared-chat", "alternate/shared-chat"
	})
	defer server.Close()
	var builtRef string
	server.buildControllerWithOptions = func(_ context.Context, ref string, opts boot.Options) (*control.Controller, error) {
		builtRef = ref
		return serveModelReplacement(opts, nil), nil
	}

	if err := server.switchEffort(context.Background(), "high"); err != nil {
		t.Fatalf("switchEffort: %v", err)
	}
	if builtRef != "alternate/shared-chat" {
		t.Fatalf("rebuilt model ref = %q, want alternate/shared-chat", builtRef)
	}
	edit := config.LoadForEdit(config.UserConfigPath())
	def, _ := edit.Provider("default")
	if def.Effort != "" {
		t.Fatalf("default effort = %q, want unchanged", def.Effort)
	}
	alt, _ := edit.Provider("alternate")
	if alt.Effort != "high" {
		t.Fatalf("alternate effort = %q, want high", alt.Effort)
	}
}

func TestSubmitNewHoldsBindingLockUntilRotationCompletes(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := &blockingNewSessionController{
		Controller: control.New(control.Options{Sink: bc, SessionDir: t.TempDir()}),
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-ctrl.release:
		default:
			close(ctrl.release)
		}
	})
	s := New(ctrl, bc, config.ServeConfig{})
	defer func() {
		select {
		case <-ctrl.release:
		default:
			close(ctrl.release)
		}
		s.Close()
	}()
	submitDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{"input":"/new"}`))
		rec := httptest.NewRecorder()
		s.submit(rec, req)
		submitDone <- rec
	}()
	select {
	case <-ctrl.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("/submit /new did not enter synchronous rotation")
	}
	lockAcquired := make(chan struct{})
	go func() {
		s.bindMu.Lock()
		close(lockAcquired)
		s.bindMu.Unlock()
	}()
	select {
	case <-lockAcquired:
		t.Fatal("bindMu was released before /new finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(ctrl.release)
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-submitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("/submit /new did not return after rotation finished")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("/submit /new status = %d, want 204", rec.Code)
	}
	select {
	case <-lockAcquired:
	case <-time.After(2 * time.Second):
		t.Fatal("bindMu stayed locked after /new completed")
	}
}

func TestSessionSnapshotEndpointsWaitForBindingEpoch(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, SessionDir: t.TempDir()})
	s := New(ctrl, bc, config.ServeConfig{})
	defer s.Close()

	for _, endpoint := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{name: "history", handler: s.history},
		{name: "status", handler: s.status},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			s.bindMu.Lock()
			done := make(chan struct{})
			go func() {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/"+endpoint.name+"?runtime=1", nil)
				endpoint.handler(rec, req)
				close(done)
			}()
			select {
			case <-done:
				s.bindMu.Unlock()
				t.Fatalf("/%s observed a controller snapshot during an active binding epoch", endpoint.name)
			case <-time.After(100 * time.Millisecond):
			}
			s.bindMu.Unlock()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("/%s stayed blocked after the binding epoch completed", endpoint.name)
			}
		})
	}
}

func TestDeleteSessionSerializesWithForegroundPromotion(t *testing.T) {
	server, first, service, _ := newExclusiveSessionServe(t)
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	promoted := control.New(control.Options{SessionService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(promoted.Close)
	reachedLock := make(chan struct{})
	deleteSessionBeforeOwnershipLockHookForTest = func() { close(reachedLock) }
	t.Cleanup(func() { deleteSessionBeforeOwnershipLockHookForTest = nil })
	server.bindMu.Lock()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.deleteSession(rec, httptest.NewRequest(http.MethodPost, "/delete-session", strings.NewReader(`{"sessionId":"target"}`)))
		close(done)
	}()
	select {
	case <-reachedLock:
	case <-time.After(2 * time.Second):
		server.bindMu.Unlock()
		t.Fatal("delete did not reach ownership boundary")
	}
	if !server.publishControllerSwap(first, promoted) {
		server.bindMu.Unlock()
		t.Fatal("foreground promotion failed")
	}
	server.bindMu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("delete remained blocked after promotion")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete promoted session status = %d, want 409", rec.Code)
	}
	if _, err := service.Query().Stat(t.Context(), runtime.Ref()); err != nil {
		t.Fatalf("promoted session was deleted: %v", err)
	}
}
