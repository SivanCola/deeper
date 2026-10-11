package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func TestDetachedRecoveryMovesRegistryKey(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.jsonl")
	recoveryPath := filepath.Join(dir, "old-recovery.jsonl")
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: oldPath})
	server := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	defer ctrl.Close()
	detached := &detachedSession{path: oldPath, ctrl: ctrl}
	server.detached[oldPath] = detached
	if err := server.moveDetachedRecovery(ctrl, recoveryPath); err != nil {
		t.Fatal(err)
	}
	canonical := agent.CanonicalSessionPath(recoveryPath)
	if got := server.detached[canonical]; got != detached || detached.path != canonical {
		t.Fatalf("recovery registry = %+v path=%q", got, detached.path)
	}
	if server.detached[oldPath] != nil {
		t.Fatal("old detached registry key was retained")
	}
}

func TestRegisterDetachedRevalidatesPathAtPublication(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.jsonl")
	newPath := filepath.Join(dir, "recovery.jsonl")
	saveServeTestSession(t, oldPath)
	saveServeTestSession(t, newPath)
	ctrl := control.New(control.Options{Runner: blockingRunner{}, SessionDir: dir, SessionPath: oldPath})
	server := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	defer server.Close()
	tag := NewSessionTagSink(server.bc)
	server.RegisterSessionTag(ctrl, tag)
	started, release := make(chan struct{}), make(chan struct{})
	registerDetachedHookForTest = func() { close(started); <-release }
	t.Cleanup(func() { registerDetachedHookForTest = nil })
	result := make(chan *detachedSession, 1)
	go func() { detached, _ := server.registerDetached(ctrl, nil, tag); result <- detached }()
	<-started
	loaded, err := agent.LoadSession(newPath)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Resume(loaded, newPath)
	ctrl.Submit("keep running")
	waitRunning(t, ctrl)
	close(release)
	detached := <-result
	canonical := agent.CanonicalSessionPath(newPath)
	server.detachedMu.Lock()
	registered := server.detached[canonical]
	server.detachedMu.Unlock()
	if detached == nil || detached.path != canonical || registered != detached {
		t.Fatalf("detached publication path = %q entry=%v, want %q", detached.path, registered == detached, canonical)
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
	server.CloseBackground()
}

func TestServeSwitchEffortUsesModelRefForDuplicateModelNames(t *testing.T) {
	writeServeModelConfig(t)

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{
		Sink:       bc,
		Label:      "shared-chat",
		ModelRef:   "alternate/shared-chat",
		SessionDir: t.TempDir(),
	})
	server := New(ctrl, bc, config.ServeConfig{})
	defer server.Close()
	var builtRef string
	server.buildController = func(_ context.Context, ref string) (*control.Controller, error) {
		builtRef = ref
		return control.New(control.Options{
			Sink:       bc,
			Label:      "shared-chat",
			ModelRef:   ref,
			SessionDir: t.TempDir(),
		}), nil
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
	if !server.publishControllerSwap(first, promoted, "") {
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
