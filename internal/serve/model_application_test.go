package serve

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/jobs"
)

func modelApplicationServer(t *testing.T) (*Server, *jobs.SessionBackgroundScope, *atomic.Int32) {
	t.Helper()
	bc := NewBroadcaster()
	scope := jobs.NewSessionBackgroundScope(jobs.NewManager(bc), nil)
	_, old, _, _ := newExclusiveSessionServeWithOptions(t, func(opts *control.Options) {
		opts.Sink, opts.BackgroundSink = bc, bc
		opts.Jobs, opts.BackgroundScope = scope.Manager, scope
		opts.ModelRef, opts.ModelSettingsRevision = "p/m", "old"
		opts.ModelSettingsCurrent = func() (string, error) { return "new", nil }
		opts.ModelSettingsContinuation = func() error { return nil }
	})
	old.PublishBackgroundScope()
	s := New(old, bc, config.ServeConfig{AuthMode: "none"})
	t.Cleanup(s.Close)
	builds := &atomic.Int32{}
	s.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		builds.Add(1)
		if err := opts.BackgroundScope.Acquire(); err != nil {
			return nil, err
		}
		return serveModelReplacement(opts, func(o *control.Options) {
			o.Jobs, o.BackgroundSink = opts.BackgroundScope.Manager, opts.Sink
			o.ModelSettingsRevision = "new"
			o.ModelSettingsCurrent = func() (string, error) { return "new", nil }
			o.ModelSettingsContinuation = func() error { return nil }
		}), nil
	}
	return s, scope, builds
}

func TestModelApplicationServePreservesProcess(t *testing.T) {
	s, scope, builds := modelApplicationServer(t)
	entered := make(chan struct{})
	ref, _ := sessionAPIRef(s.ctl())
	job := scope.Manager.StartSessionProcess(ref.SessionID, "bash", "gateway", func(ctx context.Context, out io.Writer) (string, error) {
		close(entered)
		<-ctx.Done()
		return "", ctx.Err()
	})
	<-entered
	old := s.ctl()
	s.bindMu.Lock()
	err := s.refreshRunModelSettingsLocked(t.Context())
	s.bindMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if old == s.ctl() || builds.Load() != 1 || len(scope.Manager.Running()) != 1 {
		t.Fatal("model replacement did not preserve gateway")
	}
	if !s.ctl().(*control.Controller).CancelJob(job.ID) {
		t.Fatal("replacement cannot cancel gateway")
	}
	scope.Manager.WaitForSession(t.Context(), job.SessionID, []string{job.ID}, 2)
	if len(scope.Manager.Running()) != 0 {
		t.Fatal("cancel did not drain")
	}
}

func TestModelApplicationServeBlockedChoiceAndEventDrivenApply(t *testing.T) {
	s, scope, builds := modelApplicationServer(t)
	built := make(chan struct{})
	factory := s.buildControllerWithOptions
	s.buildControllerWithOptions = func(ctx context.Context, ref string, opts boot.Options) (*control.Controller, error) {
		defer close(built)
		return factory(ctx, ref, opts)
	}
	entered, unwind := make(chan struct{}), make(chan struct{})
	ref, _ := sessionAPIRef(s.ctl())
	job := scope.Manager.StartForSession(ref.SessionID, "task", "dependent", func(ctx context.Context, _ io.Writer) (string, error) {
		close(entered)
		<-ctx.Done()
		<-unwind
		return "", ctx.Err()
	})
	<-entered
	s.bindMu.Lock()
	w := httptest.NewRecorder()
	if s.admitModelSettingsRunLocked(w, httptest.NewRequest(http.MethodPost, "/submit", nil)) {
		t.Error("dependent task admitted rebuild")
	}
	var response struct {
		Data struct {
			Outcome string                          `json:"submissionOutcome"`
			Details control.ModelApplicationDetails `json:"modelApplication"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	d := response.Data.Details
	choice := control.ModelApplicationChoice{Mode: "applied_once", ExpectedRuntimeIdentity: d.RuntimeIdentity, ExpectedAppliedRevision: d.AppliedRevision, ExpectedDesiredRevision: d.DesiredRevision}
	if response.Data.Outcome != "not_accepted" || len(d.BlockingJobs) != 1 || s.validateAppliedModelChoiceLocked(t.Context(), choice) != nil {
		t.Error("missing recovery contract")
	}
	s.bindMu.Unlock()
	scope.Manager.KillForSession(job.SessionID, job.ID)
	if !control.ModelReplacementBlocked(s.ctl()) {
		t.Error("cancelling task no longer blocks")
	}
	close(unwind)
	scope.Manager.WaitForSession(t.Context(), job.SessionID, []string{job.ID}, 2)
	// A lifecycle event wakes the existing coalesced owner; no polling loop.
	s.kickModelApplication()
	select {
	case <-built:
	case <-time.After(3 * time.Second):
		t.Fatal("deferred apply did not start")
	}
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if builds.Load() != 1 {
		t.Fatalf("builds=%d", builds.Load())
	}
	if s.validateAppliedModelChoiceLocked(t.Context(), choice) == nil {
		t.Fatal("old confirmation survived replacement")
	}
}
