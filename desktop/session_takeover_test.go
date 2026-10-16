package main

import (
	"context"
	"deepzora/internal/processidentity"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/remote/bootstrap"
	"deepzora/internal/servecontract"
	"deepzora/internal/session"
)

func TestTakeoverOwnershipEncodesOpaqueSessionPath(t *testing.T) {
	want := "session-id:identity-with-dashes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("session"); got != want {
			t.Errorf("session query = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(SessionTakeoverView{Holder: "serve"})
	}))
	defer srv.Close()
	if _, err := takeoverOwnership(context.Background(), srv.Client(), srv.URL, want); err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverGrantCannotCommitAfterTabRuntimeChanges(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := sessionRoute("taken-over")
	sourceWriter := processidentity.WriterID()
	var sourceReleased atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/handoff":
			if !sourceReleased.CompareAndSwap(false, true) {
				t.Error("handoff called more than once")
				w.WriteHeader(http.StatusConflict)
				return
			}
			_ = json.NewEncoder(w).Encode(takeoverGrant{
				SessionPath: path, MirrorID: "mirror",
				SourceWriterID: sourceWriter, TargetWriterID: sourceWriter,
			})
		case "/mirror-end":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "takeover-tab")
	tab.SessionID = "taken-over"
	tab.StartupErrLeaseHeld = true
	tab.StartupErr = (&sessionLeaseBusyError{}).Error()
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.mu.Unlock()

	originalFind := takeoverFindTargetForTest
	takeoverFindTargetForTest = func(context.Context, *App, string) (takeoverServeRecord, *http.Client, SessionTakeoverView, error) {
		return takeoverServeRecord{base: srv.URL}, srv.Client(), SessionTakeoverView{Holder: "serve"}, nil
	}
	grantSeen := make(chan struct{})
	runtimeChanged := make(chan struct{})
	originalHook := takeoverAfterGrantHookForTest
	takeoverAfterGrantHookForTest = func() {
		close(grantSeen)
		<-runtimeChanged
	}
	t.Cleanup(func() {
		takeoverFindTargetForTest = originalFind
		takeoverAfterGrantHookForTest = originalHook
	})

	newPath := sessionRoute("replacement")
	service := app.desktopSessionService("")
	t.Cleanup(app.closeSessionServices)
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: "replacement", CWD: globalTabWorkspaceRoot(), Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	replacement := control.New(control.Options{
		Executor:       agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard),
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true,
		Sink: event.Discard,
	})
	defer replacement.Close()
	done := make(chan error, 1)
	go func() { done <- app.TakeoverSession(tab.ID, "wait") }()
	select {
	case <-grantSeen:
	case err := <-done:
		t.Fatalf("takeover returned before requesting its grant: %v", err)
	}
	app.runtimeRebuildMu.Lock()
	app.mu.Lock()
	tab.SessionID = runtime.Ref().SessionID
	tab.Ctrl = replacement
	tab.StartupErrLeaseHeld = false
	app.mu.Unlock()
	app.runtimeRebuildMu.Unlock()
	close(runtimeChanged)
	if err := <-done; err == nil {
		t.Fatal("stale takeover unexpectedly committed")
	}
	if tab.Ctrl != replacement || tab.currentSessionIdentity() != newPath {
		t.Fatalf("replacement runtime was overwritten: ctrl=%p path=%q", tab.Ctrl, tab.currentSessionIdentity())
	}
	if mirror := app.takeoverMirrorForKey(sessionRuntimeKey(path)); mirror != nil {
		t.Fatal("stale takeover installed a mirror")
	}
}

func TestDirectAdoptGrantCannotAttachAfterTabRuntimeChanges(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	sessionDir := config.ProjectSessionDir(workspace)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := sessionRoute("adopted")
	mirrorEnded := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/token":
			w.Header().Set("X-Deepzora-Serve-Capabilities", servecontract.BaselineV1)
			w.WriteHeader(http.StatusNoContent)
		case "/ownership":
			_ = json.NewEncoder(w).Encode(SessionTakeoverView{Holder: "free"})
		case "/adopt":
			_ = json.NewEncoder(w).Encode(takeoverGrant{
				SessionPath: path, MirrorID: "stale-mirror",
				SourceWriterID: "serve-writer", TargetWriterID: processidentity.WriterID(),
			})
		case "/mirror-end":
			mirrorEnded <- struct{}{}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := app.createTabEntryWithID("global", globalTabWorkspaceRoot(), "", "adopt-tab")
	tab.SessionID = "adopted"
	oldSink := &tabEventSink{tabID: tab.ID, app: app}
	tab.sink = oldSink
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.newSessionRuntimeLocked(tab, sessionRuntimeKey(path))
	app.advanceSessionRuntimeEpochLocked(tab)
	app.mu.Unlock()

	originalDiscover := discoverLocalTakeoverServesForAdopt
	discoverLocalTakeoverServesForAdopt = func() []takeoverServeRecord {
		return []takeoverServeRecord{{base: srv.URL, token: "fresh", state: bootstrap.ServeState{Workspace: workspace}}}
	}
	grantSeen := make(chan struct{})
	runtimeChanged := make(chan struct{})
	originalHook := takeoverAfterAdoptGrantHookForTest
	takeoverAfterAdoptGrantHookForTest = func() {
		close(grantSeen)
		<-runtimeChanged
	}
	t.Cleanup(func() {
		discoverLocalTakeoverServesForAdopt = originalDiscover
		takeoverAfterAdoptGrantHookForTest = originalHook
	})

	done := make(chan bool, 1)
	go func() { done <- app.adoptSessionFromLocalServeOnce(tab.ID, path) }()
	select {
	case <-grantSeen:
	case result := <-done:
		t.Fatalf("adoption returned before requesting its grant: %v", result)
	}
	newSink := &tabEventSink{tabID: tab.ID, app: app}
	app.runtimeRebuildMu.Lock()
	app.mu.Lock()
	tab.SessionID = "replacement"
	tab.sink = newSink
	app.advanceSessionRuntimeEpochLocked(tab)
	app.mu.Unlock()
	app.runtimeRebuildMu.Unlock()
	close(runtimeChanged)
	if !<-done {
		t.Fatal("stale adoption did not reach a serve")
	}
	if mirror := app.takeoverMirrorForKey(sessionRuntimeKey(path)); mirror != nil {
		t.Fatal("stale adoption installed a mirror")
	}
	if oldSink.takeoverMirror.Load() != nil || newSink.takeoverMirror.Load() != nil {
		t.Fatal("stale adoption attached to an old or replacement event sink")
	}
	select {
	case <-mirrorEnded:
	case <-time.After(3 * time.Second):
		t.Fatal("stale mirror generation was not ended")
	}
}

type failingTakeoverRelease struct{ *control.Controller }

func (c failingTakeoverRelease) ReleaseSessionForHandoff() error {
	return errors.New("injected release failure")
}

func TestTakeoverMirrorKeepsBindingWhenReleaseFails(t *testing.T) {
	app, ctrl, runtime, _, route := newCanonicalTitleFixture(t)
	tab := app.tabs["test"]
	tab.Ctrl = failingTakeoverRelease{ctrl}
	var ended atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ended.Store(true); w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	m := newTakeoverMirror(app, sessionRuntimeKey(route), tab.ID, route, tab.sink, takeoverServeRecord{base: srv.URL}, srv.Client(), takeoverGrant{SessionPath: route, MirrorID: "mirror"})
	if err := m.returnLeaseForDemotion(tab); err == nil {
		t.Fatal("release failure ignored")
	}
	if ended.Load() || m.returned.Load() {
		t.Fatal("failed release retired mirror")
	}
	ref, ok := ctrl.SessionRef()
	if !ok || ref != runtime.Ref() {
		t.Fatal("failed release changed current identity")
	}
}

func TestTakeoverMirrorRetriesSameDrainedFramesInOrder(t *testing.T) {
	var mu sync.Mutex
	var batches [][]eventwire.Event
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Frames []eventwire.Event `json:"frames"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		batches = append(batches, append([]eventwire.Event(nil), body.Frames...))
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()

	m := &takeoverMirror{
		sessionPath: "session.jsonl", client: srv.Client(),
		record: takeoverServeRecord{base: srv.URL}, grant: takeoverGrant{MirrorID: "mirror-1"},
		wake: make(chan struct{}, 1),
	}
	m.forwardEvent(event.Event{Kind: event.Text, Text: "first"})
	m.forwardEvent(event.Event{Kind: event.Text, Text: "second"})
	if !m.pushOnce(false) {
		t.Fatal("forwarder stopped unexpectedly")
	}
	if !m.pushOnce(false) {
		t.Fatal("forwarder stopped unexpectedly")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 2 {
		t.Fatalf("batches = %+v, want two complete batches", batches)
	}
	for i := range batches[0] {
		if batches[0][i].Text != batches[1][i].Text {
			t.Fatalf("retry reordered frame %d: %q != %q", i, batches[0][i].Text, batches[1][i].Text)
		}
	}
}

func TestTakeoverMirrorReadoptsAfterServeMoves(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	sessionDir := config.ProjectSessionDir(workspace)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "session.jsonl")
	deadURL := "http://unreachable.invalid"
	deadClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("injected stale serve connection")
	})}
	var delivered atomic.Bool
	newServe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/token":
			w.Header().Set("X-Deepzora-Serve-Capabilities", servecontract.BaselineV1)
			w.WriteHeader(http.StatusNoContent)
		case "/adopt":
			_ = json.NewEncoder(w).Encode(takeoverGrant{
				SessionPath: path, MirrorID: "new", SourceWriterID: "source", TargetWriterID: processidentity.WriterID(),
			})
		case "/external/frames":
			delivered.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer newServe.Close()
	originalDiscover := discoverLocalTakeoverServesForMirror
	discoverLocalTakeoverServesForMirror = func() []takeoverServeRecord {
		return []takeoverServeRecord{{base: newServe.URL, token: "fresh", state: bootstrap.ServeState{Workspace: workspace}}}
	}
	t.Cleanup(func() { discoverLocalTakeoverServesForMirror = originalDiscover })
	m := &takeoverMirror{
		sessionPath: path,
		client:      deadClient,
		record:      takeoverServeRecord{base: deadURL},
		grant:       takeoverGrant{MirrorID: "old"},
		wake:        make(chan struct{}, 1),
	}
	m.bindingRevision = 1
	m.forwardEvent(event.Event{Kind: event.Text, Text: "recover"})
	if !m.pushOnce(false) {
		t.Fatal("desktop mirror stopped during re-adoption")
	}
	if !m.pushOnce(false) {
		t.Fatal("desktop mirror stopped after re-adoption")
	}
	if !delivered.Load() {
		t.Fatal("desktop mirror did not re-adopt and deliver through the new serve")
	}
	_, record, _, grant, revision := m.snapshotBinding()
	if record.base != newServe.URL || grant.MirrorID != "new" || revision <= 1 {
		t.Fatalf("re-adopted binding = base %q grant %+v revision %d", record.base, grant, revision)
	}
}

func TestTakeoverMirrorDemotionReturnsRefreshedGeneration(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	sessionDir := config.ProjectSessionDir(workspace)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	app, ctrl, runtime, _, path := newCanonicalTitleFixture(t)
	oldStarted := make(chan struct{})
	oldRelease := make(chan struct{})
	var oldEnds atomic.Int32
	oldServe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/external/frames":
			close(oldStarted)
			<-oldRelease
			w.WriteHeader(http.StatusUnauthorized)
		case "/mirror-end":
			oldEnds.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer oldServe.Close()
	newEnds := make(chan string, 1)
	newServe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/token":
			w.Header().Set("X-Deepzora-Serve-Capabilities", servecontract.BaselineV1)
			w.WriteHeader(http.StatusNoContent)
		case "/adopt":
			_ = json.NewEncoder(w).Encode(takeoverGrant{
				SessionPath: path, MirrorID: "mirror-new",
				SourceWriterID: "serve-new", TargetWriterID: processidentity.WriterID(),
			})
		case "/mirror-end":
			var body struct {
				MirrorID string `json:"mirrorId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			newEnds <- body.MirrorID
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer newServe.Close()
	originalDiscover := discoverLocalTakeoverServesForMirror
	discoverLocalTakeoverServesForMirror = func() []takeoverServeRecord {
		return []takeoverServeRecord{{base: newServe.URL, token: "fresh", state: bootstrap.ServeState{Workspace: workspace}}}
	}
	t.Cleanup(func() { discoverLocalTakeoverServesForMirror = originalDiscover })

	tab := app.tabs["test"]
	m := newTakeoverMirror(app, sessionRuntimeKey(path), tab.ID, path, nil,
		takeoverServeRecord{base: oldServe.URL}, oldServe.Client(),
		takeoverGrant{SessionPath: path, MirrorID: "mirror-old", SourceWriterID: "serve-old", TargetWriterID: processidentity.WriterID()},
	)
	m.forwardEvent(event.Event{Kind: event.Text, Text: "generation fence"})
	pushDone := make(chan bool, 1)
	go func() { pushDone <- m.pushOnce(false) }()
	<-oldStarted
	returnDone := make(chan error, 1)
	go func() { returnDone <- m.returnLeaseForDemotion(tab) }()
	close(oldRelease)
	if !<-pushDone {
		t.Fatal("forwarder stopped while re-adopting")
	}
	if err := <-returnDone; err != nil {
		t.Fatal(err)
	}
	if _, ok := ctrl.SessionRef(); ok {
		t.Fatal("returned controller retained identity")
	}
	if _, ok := ctrl.SessionService().Runtime(runtime.Ref()); ok {
		t.Fatal("returned runtime retained writer")
	}
	select {
	case mirrorID := <-newEnds:
		if mirrorID != "mirror-new" {
			t.Fatalf("mirror-end id = %q, want refreshed generation", mirrorID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("refreshed mirror generation was not ended")
	}
	if oldEnds.Load() != 0 || !m.returned.Load() {
		t.Fatalf("old ends=%d returned=%v", oldEnds.Load(), m.returned.Load())
	}
}

func TestTakeoverMirrorChunksWithoutDroppingFrames(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Frames []eventwire.Event `json:"frames"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Frames) > takeoverMirrorMaxQueue {
			t.Fatalf("batch size = %d", len(body.Frames))
		}
		for _, frame := range body.Frames {
			got = append(got, frame.Text)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"reclaimRequested": false})
	}))
	defer srv.Close()
	m := &takeoverMirror{
		sessionPath: "session.jsonl", client: srv.Client(), record: takeoverServeRecord{base: srv.URL},
		grant: takeoverGrant{MirrorID: "mirror-1"}, wake: make(chan struct{}, 1),
	}
	for i := range takeoverMirrorMaxQueue + 37 {
		m.forwardEvent(event.Event{Kind: event.Text, Text: strconv.Itoa(i)})
	}
	if !m.pushOnce(false) {
		t.Fatal("chunked frame push stopped unexpectedly")
	}
	if !m.pushOnce(false) {
		t.Fatal("chunked frame push stopped unexpectedly")
	}
	if len(got) != takeoverMirrorMaxQueue+37 {
		t.Fatalf("received %d frames", len(got))
	}
	for i, text := range got {
		if text != strconv.Itoa(i) {
			t.Fatalf("frame %d = %q", i, text)
		}
	}
}
