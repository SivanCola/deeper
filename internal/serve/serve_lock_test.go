package serve

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/jobs"
)

type blockingRequestBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingRequestBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return 0, io.EOF
}

func (*blockingRequestBody) Close() error { return nil }

type blockingNewSessionController struct {
	*control.Controller
	entered chan struct{}
	release chan struct{}
}

func (c *blockingNewSessionController) NewSession() error {
	close(c.entered)
	<-c.release
	return c.Controller.NewSession()
}

// expectServerMutexAvailable returns a callback that fails the test if s.mu can't
// be acquired within 500ms — i.e. switchModel is holding the lock across the
// callback. It signals checks once it has probed, so the test can assert the
// callback actually ran.
func expectServerMutexAvailable(t *testing.T, s *Server, checks chan<- struct{}) func() {
	t.Helper()
	return func() {
		acquired := make(chan struct{})
		go func() {
			s.mu.Lock()
			s.mu.Unlock() //nolint:staticcheck // probe: lock must be immediately acquirable
			close(acquired)
		}()
		select {
		case <-acquired:
		case <-time.After(500 * time.Millisecond):
			t.Error("switchModel held s.mu across a Snapshot/Close callback")
		}
		if checks == nil {
			return
		}
		select {
		case checks <- struct{}{}:
		default:
		}
	}
}

// Candidate construction must leave the publication mutex available to reads.
func TestSwitchModelDoesNotHoldServerLockDuringBuild(t *testing.T) {
	s, _, _, _ := newExclusiveSessionServe(t)
	defer s.Close()
	checks := make(chan struct{}, 1)
	var built *control.Controller
	s.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		expectServerMutexAvailable(t, s, checks)()
		built = serveModelReplacement(opts, nil)
		return built, nil
	}
	if err := s.switchModel(t.Context(), "next-model"); err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || s.ctl() != built {
		t.Fatal("model candidate was not built and published")
	}
}

// TestSwitchModelDiscardsBuiltControllerOnConcurrentSwap verifies the failure
// path: if the controller is swapped out (e.g. by resume) between Build and the
// publish lock, switchModel must discard the new controller instead of leaking
// it or clobbering the concurrent swap.
func TestSwitchModelDiscardsBuiltControllerOnConcurrentSwap(t *testing.T) {
	s, old, service, ref := newExclusiveSessionServe(t)
	defer s.Close()
	_, other, _, _ := newExclusiveSessionServe(t)
	before, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}

	var built *control.Controller
	s.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		// Simulate a concurrent path (resume/new-session) replacing the
		// controller after the off-lock snapshot but before the publish lock.
		s.mu.Lock()
		s.ctrl = other
		s.mu.Unlock()
		built = serveModelReplacement(opts, nil)
		return built, nil
	}

	err = s.switchModel(context.Background(), "next-model")
	if err == nil {
		t.Fatal("expected switchModel to fail when the controller changed mid-switch")
	}
	if s.ctl() != other {
		t.Fatal("switchModel clobbered a concurrent controller swap")
	}
	if err := old.Snapshot(); err != nil {
		t.Fatalf("discarding the candidate closed its source writer: %v", err)
	}
	after, err := service.Query().Snapshot(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if after.Projection.ModelRef != before.Projection.ModelRef {
		t.Fatal("discarded candidate published its model selection")
	}
	select {
	case <-built.Closed():
	case <-time.After(time.Second):
		t.Fatal("discarded model candidate leaked controller resources")
	}
}

// TestSwitchModelRejectsWhileRunning keeps the pre-existing guard: a switch is
// refused while a turn is running, before any snapshot/build work.
func TestSwitchModelRejectsWhileRunning(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Runner: blockingRunner{}, Sink: bc})
	s := &Server{ctrl: ctrl, bc: bc}
	built := false
	s.buildControllerWithOptions = func(_ context.Context, _ string, _ boot.Options) (*control.Controller, error) {
		built = true
		return control.New(control.Options{Sink: bc}), nil
	}

	// Drive the controller into a running turn.
	ctrl.SubmitHTTP("hi")
	waitRunning(t, ctrl)

	if err := s.switchModel(context.Background(), "next-model"); err == nil {
		t.Fatal("expected switchModel to refuse while a turn is running")
	}
	if built {
		t.Fatal("switchModel built a controller despite a running turn")
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
}

func TestForegroundMutationRejectsStaleSessionID(t *testing.T) {
	s, ctrl, _, ref := newExclusiveSessionServeWithOptions(t, func(o *control.Options) { o.Runner = blockingRunner{} })

	ctrl.SubmitHTTP("hi")
	waitRunning(t, ctrl)
	req := httptest.NewRequest(http.MethodPost, "/cancel", strings.NewReader(`{}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(expectedSessionIDHeader, "stale")
	rec := httptest.NewRecorder()
	operatorHandler(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale cancel status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if !ctrl.Running() {
		t.Fatal("stale cancel reached the newly current controller")
	}

	req = httptest.NewRequest(http.MethodPost, "/cancel", strings.NewReader(`{}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(expectedSessionIDHeader, ref.SessionID)
	rec = httptest.NewRecorder()
	operatorHandler(s).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("current cancel status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	waitNotRunning(t, ctrl)
}

func TestForegroundMutationReadsBodyBeforeBindingLock(t *testing.T) {
	s := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{})
	blockedBody := &blockingRequestBody{started: make(chan struct{}), release: make(chan struct{})}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		req := httptest.NewRequest(http.MethodPost, "/slow", blockedBody)
		s.foregroundMutation(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusNoContent)
		})(httptest.NewRecorder(), req)
	}()
	select {
	case <-blockedBody.started:
	case <-time.After(time.Second):
		t.Fatal("slow request body was never read")
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		req := httptest.NewRequest(http.MethodPost, "/fast", http.NoBody)
		s.foregroundMutation(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})(httptest.NewRecorder(), req)
	}()
	select {
	case <-secondDone:
	case <-time.After(500 * time.Millisecond):
		close(blockedBody.release)
		<-firstDone
		t.Fatal("slow body held the session binding lock")
	}
	close(blockedBody.release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("slow request did not finish after its body was released")
	}
}

func TestForegroundMutationRejectsOversizedBodyBeforeHandler(t *testing.T) {
	s := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{})
	called := false
	req := httptest.NewRequest(http.MethodPost, "/oversized", strings.NewReader(strings.Repeat("x", foregroundMutationMaxBody+1)))
	rec := httptest.NewRecorder()
	s.foregroundMutation(func(http.ResponseWriter, *http.Request) { called = true })(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if called {
		t.Fatal("oversized body reached the foreground handler")
	}
}

func TestSwitchModelRejectsWhileBackgroundJobRunning(t *testing.T) {
	bc := NewBroadcaster()
	manager := jobs.NewManager(bc)
	ctrl := control.New(control.Options{Sink: bc, Jobs: manager})
	defer ctrl.Close()
	manager.StartForSession("", "task", "running", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	s := &Server{ctrl: ctrl, bc: bc}
	built := false
	s.buildControllerWithOptions = func(_ context.Context, _ string, _ boot.Options) (*control.Controller, error) {
		built = true
		return control.New(control.Options{Sink: bc}), nil
	}

	if err := s.switchModel(context.Background(), "next-model"); err == nil {
		t.Fatal("expected switchModel to refuse while a background job is running")
	}
	if built {
		t.Fatal("switchModel built a controller despite a running background job")
	}
}

func TestExtensionReloadRejectsWhileTurnRunning(t *testing.T) {
	s, ctrl, _, _ := newExclusiveSessionServeWithOptions(t, func(options *control.Options) { options.Runner = blockingRunner{} })
	defer s.Close()
	built := false
	s.rebuildControllerWithOptions = func(_ context.Context, _ *control.Controller, _ string, _ boot.Options) (*control.Controller, error) {
		built = true
		return nil, errors.New("unexpected rebuild")
	}

	ctrl.SubmitHTTP("hi")
	waitRunning(t, ctrl)
	if err := s.reloadExtensions(context.Background()); err == nil {
		t.Fatal("expected extension reload to refuse while a turn is running")
	}
	if built {
		t.Fatal("extension reload built a controller despite a running turn")
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
}

func TestConcurrentExtensionReloadsAreSerialized(t *testing.T) {
	s, _, _, _ := newExclusiveSessionServe(t)
	defer s.Close()
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	s.rebuildControllerWithOptions = func(_ context.Context, _ *control.Controller, _ string, opts boot.Options) (*control.Controller, error) {
		if calls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		} else {
			close(secondEntered)
		}
		return serveModelReplacement(opts, nil), nil
	}

	done := make(chan error, 2)
	go func() { done <- s.reloadExtensions(context.Background()) }()
	<-firstEntered
	go func() { done <- s.reloadExtensions(context.Background()) }()
	select {
	case <-secondEntered:
		t.Fatal("second extension reload entered the rebuild while the first still owned bindMu")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("reload: %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("rebuild calls = %d, want 2", calls.Load())
	}
}

func TestSubmitWaitsForExtensionReloadAndTargetsReplacement(t *testing.T) {
	s, old, _, _ := newExclusiveSessionServe(t)
	defer s.Close()
	buildEntered := make(chan struct{})
	releaseBuild := make(chan struct{})
	var replacement *control.Controller
	s.rebuildControllerWithOptions = func(_ context.Context, _ *control.Controller, _ string, opts boot.Options) (*control.Controller, error) {
		close(buildEntered)
		<-releaseBuild
		replacement = serveModelReplacement(opts, func(options *control.Options) { options.Runner = blockingRunner{} })
		return replacement, nil
	}

	reloadDone := make(chan error, 1)
	go func() { reloadDone <- s.reloadExtensions(context.Background()) }()
	<-buildEntered

	submitDone := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{"input":"hello"}`))
		rec := httptest.NewRecorder()
		s.submit(rec, req)
		submitDone <- rec.Code
	}()
	select {
	case <-submitDone:
		t.Fatal("submit crossed the extension reload generation boundary")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseBuild)
	if err := <-reloadDone; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if code := <-submitDone; code != 202 {
		t.Fatalf("submit status = %d, want 202", code)
	}
	waitRunning(t, replacement)
	if old.Running() {
		t.Fatal("submit started on the outgoing controller")
	}
	replacement.Cancel()
	waitNotRunning(t, replacement)
}

// blockingRunner keeps a turn "running" until its context is cancelled, so tests
// can observe Running() == true deterministically.
type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func waitRunning(t *testing.T, ctrl *control.Controller) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if ctrl.Running() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("controller never entered the running state")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func waitNotRunning(t *testing.T, ctrl *control.Controller) {
	t.Helper()
	done, running := ctrl.TurnIdleDone()
	if !running {
		return
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("controller never reached idle after cancel: %+v", ctrl.RuntimeStateSnapshot())
	}
}
