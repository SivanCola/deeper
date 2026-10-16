package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/billing"
	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/session"
)

type replayStubController struct{ *control.Controller }

func (replayStubController) SessionRef() (session.SessionRef, bool) {
	return session.SessionRef{HostID: "serve-test", SessionID: "a"}, true
}
func (replayStubController) ReplayPendingPromptsWith(factory func() event.Sink) {
	factory().Emit(event.Event{Kind: event.ApprovalRequest})
}

func sessionPathFromFrame(t *testing.T, frame []byte) string {
	t.Helper()
	var wired eventwire.Event
	if err := json.Unmarshal(frame, &wired); err != nil {
		t.Fatalf("decode event frame: %v\nframe=%s", err, frame)
	}
	return wired.SessionPath
}

type replayRaceController struct {
	control.SessionAPI
	path     string
	onPath   func()
	replayed chan string
}

func (c *replayRaceController) SessionRef() (session.SessionRef, bool) {
	if c.onPath != nil {
		c.onPath()
	}
	return session.SessionRef{HostID: "serve-test", SessionID: strings.TrimPrefix(c.path, "session-id:")}, true
}

func (c *replayRaceController) ReplayPendingPromptsWith(factory func() event.Sink) {
	c.replayed <- c.path
	factory().Emit(event.Event{Kind: event.ApprovalRequest})
}

type closeProbeController struct {
	*control.Controller
	closed atomic.Bool
}

func (c *closeProbeController) Close() {
	c.closed.Store(true)
	c.Controller.Close()
}

type retiringCloseProbeController struct {
	*control.Controller
	closeStarted chan struct{}
	closeRelease chan struct{}
}

func (c *retiringCloseProbeController) Close() {
	close(c.closeStarted)
	<-c.closeRelease
	c.Controller.Close()
}

func TestDetachedSessionRemainsBusyUntilCloseFinishes(t *testing.T) {
	path := "session-id:retiring"
	ctrl := &retiringCloseProbeController{
		Controller:   runtimeStateServeController(t, t.TempDir(), "retiring", nil),
		closeStarted: make(chan struct{}),
		closeRelease: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-ctrl.closeRelease:
		default:
			close(ctrl.closeRelease)
		}
	})
	server := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{})
	tag := NewSessionTagSink(server.bc)
	tag.SetIdentity(strings.TrimPrefix(path, "session-id:"))
	detached, err := server.registerDetached(ctrl, tag)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctrl.closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("detached controller did not start retiring")
	}
	if !server.detachedBusy(path) {
		t.Fatal("retiring session disappeared before controller Close finished")
	}
	if got := server.takeDetached(path); got != nil {
		t.Fatal("retiring controller remained reattachable")
	}
	close(ctrl.closeRelease)
	select {
	case <-detached.done:
	case <-time.After(2 * time.Second):
		t.Fatal("detached retirement did not finish")
	}
	if server.detachedBusy(path) {
		t.Fatal("retired session remained busy after controller Close finished")
	}
}

func TestServerCloseClosesPublishedForegroundReplacement(t *testing.T) {
	bc := NewBroadcaster()
	first := &closeProbeController{Controller: control.New(control.Options{Sink: bc})}
	replacement := &closeProbeController{Controller: control.New(control.Options{Sink: bc})}
	server := New(first, bc, config.ServeConfig{})
	if !server.publishControllerSwap(first, replacement) {
		t.Fatal("replacement publication failed")
	}
	server.Close()
	if !replacement.closed.Load() {
		t.Fatal("server shutdown left the published foreground controller open")
	}
}

func TestBusyNewRejectsUntaggedLegacyController(t *testing.T) {
	dir := t.TempDir()
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Runner: blockingRunner{}, Sink: bc, SessionDir: dir})
	server := New(ctrl, bc, config.ServeConfig{})
	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	defer ctrl.Close()
	ctrl.Submit("keep running")
	waitRunning(t, ctrl)
	resp, err := http.Post(httpServer.URL+"/new", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("untagged busy /new = %d, want 409: %s", resp.StatusCode, body)
	}
	if server.ctl() != control.SessionAPI(ctrl) || !ctrl.Running() {
		t.Fatal("legacy controller was detached without a session tag")
	}
	ctrl.Cancel()
	waitNotRunning(t, ctrl)
}

func TestReplayPendingPromptsBroadcastTagsFrames(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	server := New(replayStubController{ctrl}, bc, config.ServeConfig{})
	all, stop := bc.Subscribe()
	defer stop()
	server.replayPendingPromptsBroadcast()
	select {
	case frame := <-all:
		if got, want := sessionPathFromFrame(t, frame), "session-id:a"; got != want {
			t.Fatalf("replayed frame session path = %q, want %q: %s", got, want, frame)
		}
	default:
		t.Fatal("pending prompt was not replayed")
	}
}

func TestEventsReplayUsesControllerCapturedWithPath(t *testing.T) {
	bc := NewBroadcaster()
	baseA := control.New(control.Options{Sink: bc})
	baseB := control.New(control.Options{Sink: bc})
	replayed := make(chan string, 2)
	a := &replayRaceController{SessionAPI: baseA, path: "session-id:a", replayed: replayed}
	b := &replayRaceController{SessionAPI: baseB, path: "session-id:b", replayed: replayed}
	server := New(a, bc, config.ServeConfig{})
	promotionStarted := make(chan struct{})
	promotionDone := make(chan struct{})
	a.onPath = func() {
		a.onPath = nil
		go func() {
			close(promotionStarted)
			server.bindMu.Lock()
			if !server.publishControllerSwap(a, b) {
				t.Error("controller promotion failed")
			}
			bc.Emit(event.Event{Kind: event.AskRequest, SessionPath: b.path})
			server.bindMu.Unlock()
			close(promotionDone)
		}()
		<-promotionStarted
	}

	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpServer.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	nextData := func() string {
		t.Helper()
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
				return strings.TrimPrefix(line, "data: ")
			}
		}
		t.Fatalf("event stream ended before the next frame: %v", scanner.Err())
		return ""
	}
	if first := nextData(); sessionPathFromFrame(t, []byte(first)) != a.path {
		t.Fatalf("first frame = %s, want controller A replay", first)
	}
	select {
	case <-promotionDone:
	case <-time.After(2 * time.Second):
		t.Fatal("controller promotion remained blocked after subscription")
	}
	if second := nextData(); sessionPathFromFrame(t, []byte(second)) != b.path {
		t.Fatalf("second frame = %s, want promoted controller B prompt", second)
	}
}

func TestSlashNewRefreshesControllerTagAndForegroundRoute(t *testing.T) {
	server, ctrl, _, initial := newExclusiveSessionServe(t)
	defer server.Close()
	bc, tag := server.bc, server.tagFor(ctrl)
	all, stop := bc.Subscribe()
	defer stop()
	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	resp, err := http.Post(httpServer.URL+"/submit", "application/json", strings.NewReader(`{"input":"/new"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("slash /new submit status = %d, want 204", resp.StatusCode)
	}
	select {
	case frame := <-all:
		deadline := time.After(2 * time.Second)
		for !strings.Contains(string(frame), `"kind":"notice"`) || !strings.Contains(string(frame), `"text":"new session"`) {
			select {
			case frame = <-all:
			case <-deadline:
				t.Fatal("slash /new did not publish its completion notice")
			}
		}
		ref, bound := ctrl.SessionRef()
		if !bound || ref == initial || tag.Path() != remoteSessionIDQueryPrefix+ref.SessionID || bc.CurrentSession() != remoteSessionIDQueryPrefix+ref.SessionID {
			t.Fatalf("slash /new did not publish its current identity: %+v, route=%q", ref, bc.CurrentSession())
		}
		var notice eventwire.Event
		if err := json.Unmarshal(frame, &notice); err != nil || notice.SessionID != ref.SessionID || notice.SessionPath != remoteSessionIDQueryPrefix+ref.SessionID || !notice.SessionCurrent {
			t.Fatalf("slash /new notice used another identity: %s, %v", frame, err)
		}

	case <-time.After(2 * time.Second):
		t.Fatal("slash /new emitted no event")
	}
}

type detachedProviderHealProbe struct {
	*control.Controller
	closed atomic.Bool
}

func (c *detachedProviderHealProbe) RuntimeStateSnapshot() event.RuntimeStateSnapshot {
	return event.RuntimeStateSnapshot{BackgroundJobs: 1, Cancellable: true}
}

func (c *detachedProviderHealProbe) Close() {
	c.closed.Store(true)
	c.Controller.Close()
}

func TestProviderHealSynchronouslyRetiresDetachedControllers(t *testing.T) {
	path := "session-id:background"
	ctrl := &detachedProviderHealProbe{Controller: runtimeStateServeController(t, t.TempDir(), "background", nil)}
	server := New(control.New(control.Options{}), NewBroadcaster(), config.ServeConfig{})
	tag := NewSessionTagSink(server.bc)
	tag.SetIdentity(strings.TrimPrefix(path, "session-id:"))
	if _, err := server.registerDetached(ctrl, tag); err != nil {
		t.Fatal(err)
	}
	server.retireDetachedForProviderHeal()
	if !ctrl.closed.Load() {
		t.Fatal("provider heal returned before the detached controller closed")
	}
	if server.detachedBusy(path) {
		t.Fatal("provider heal left the detached controller reattachable")
	}
}

func TestBuildTaggedInheritsWorkspacePlacement(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, SessionDir: sessionDir, WorkspaceRoot: root})
	server := New(ctrl, bc, config.ServeConfig{})
	server.SetControllerBuildOptions(boot.Options{MaxSteps: 7, MaxStepsKey: "--max-steps"})
	var got boot.Options
	server.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		got = opts
		return control.New(control.Options{Sink: opts.Sink, SessionDir: opts.SessionDir, WorkspaceRoot: opts.WorkspaceRoot}), nil
	}
	built, _, err := server.buildTagged(context.Background(), "provider/model", false)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	if got.SessionDir != sessionDir || got.WorkspaceRoot != root {
		t.Fatalf("build placement = (%q, %q), want (%q, %q)", got.SessionDir, got.WorkspaceRoot, sessionDir, root)
	}
	if got.MaxSteps != 7 || got.MaxStepsKey != "--max-steps" {
		t.Fatalf("process build options = max:%d key:%q, want 7/--max-steps", got.MaxSteps, got.MaxStepsKey)
	}
}

func TestSessionTagSinkBuffersUntilReplacementCommit(t *testing.T) {
	bc := NewBroadcaster()
	all, stop := bc.Subscribe()
	defer stop()
	tag := NewSessionTagSink(bc)
	const id = "replacement"

	tag.Emit(event.Event{Kind: event.Notice, Text: "booted"})
	tag.PrimeIdentity(id)
	if len(all) != 0 {
		t.Fatal("replacement boot event leaked before publication committed")
	}
	tag.Activate()
	if len(all) != 1 {
		t.Fatalf("activation flushed %d frames, want 1", len(all))
	}
	var frame eventwire.Event
	if err := json.Unmarshal(<-all, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Kind != "notice" || frame.SessionID != id {
		t.Fatalf("activated boot frame = %+v", frame)
	}
}

func TestBuildTaggedFailureDiscardsBufferedBootEvents(t *testing.T) {
	bc := NewBroadcaster()
	all, stop := bc.Subscribe()
	defer stop()
	server := New(control.New(control.Options{}), bc, config.ServeConfig{})
	server.buildControllerWithOptions = func(_ context.Context, _ string, opts boot.Options) (*control.Controller, error) {
		opts.Sink.Emit(event.Event{Kind: event.Notice, Text: "booting replacement"})
		return nil, errors.New("build failed")
	}
	if _, _, err := server.buildTagged(context.Background(), "provider/model", false); err == nil {
		t.Fatal("buildTagged unexpectedly succeeded")
	}
	if len(all) != 0 {
		t.Fatal("failed replacement leaked a buffered boot event")
	}
}

type balanceProbeController struct {
	*control.Controller
	calls atomic.Int32
}

func (c *balanceProbeController) Balance(context.Context) (*billing.Balance, error) {
	c.calls.Add(1)
	return &billing.Balance{Available: true}, nil
}

func (c *balanceProbeController) RuntimeStateSnapshot() event.RuntimeStateSnapshot {
	state := c.Controller.RuntimeStateSnapshot()
	state.Phase, state.Running, state.PendingPrompt, state.Cancellable = "executing", true, true, true
	return state
}

func TestStatusRuntimeQuerySkipsBalance(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := &balanceProbeController{Controller: control.New(control.Options{Sink: bc})}
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	full, err := http.Get(srv.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, full.Body)
	full.Body.Close()
	before := ctrl.calls.Load()
	if before == 0 {
		t.Fatal("full status did not fetch balance")
	}
	lite, err := http.Get(srv.URL + "/status?runtime=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(lite.Body)
	lite.Body.Close()
	if ctrl.calls.Load() != before {
		t.Fatal("runtime status fetched balance")
	}
	for _, want := range []string{`"running":true`, `"pendingPrompt":true`, `"cancellable":true`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("runtime status missing %s: %s", want, body)
		}
	}
}
