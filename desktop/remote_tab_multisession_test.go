package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRemoteTabAllSessionEventsRouteOnlyCurrentFrames(t *testing.T) {
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{
		{SessionID: remoteSessionIDFromRoute("session-id:current"), Name: "current", Path: "session-id:current", Current: true},
		{SessionID: remoteSessionIDFromRoute("session-id:background"), Name: "background", Path: "session-id:background", Running: true},
	})
	fs.mu.Lock()
	fs.eventFrames = []string{
		`{"kind":"turn_started","sessionPath":"session-id:background"}`,
		`{"kind":"turn_started","sessionPath":"session-id:current"}`,
		`{"kind":"ready","sessionPath":"session-id:current"}`,
	}
	fs.mu.Unlock()
	kernel := &fakeRemoteKernel{
		statuses:    []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView:  RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL},
		ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	log := &eventLog{}
	a := &App{remoteRuntime: kernel, remoteEventHook: log.add}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{SessionID: "current", SessionTitle: "Current"})
	events := log.recorded()
	for _, event := range events {
		if strings.HasPrefix(event, "remote-tab:"+meta.ID+":event") && strings.Contains(event, "session-id:background") {
			t.Fatalf("background frame leaked to foreground reducer: %v", events)
		}
	}
	if !slices.ContainsFunc(events, func(event string) bool {
		return strings.HasPrefix(event, "remote-tab:"+meta.ID+":event") && strings.Contains(event, "session-id:current")
	}) {
		t.Fatalf("current-session frame was not forwarded: %v", events)
	}
	a.remoteTabMu.Lock()
	backgroundRunning := a.remoteTabs[meta.ID].routing.running["session-id:background"]
	a.remoteTabMu.Unlock()
	if !backgroundRunning {
		t.Fatal("background running state was not retained for the project tree")
	}
	sessions, err := a.RemoteProjectSessions("box", "~/app")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(sessions, func(session RemoteSessionView) bool {
		return session.SessionID == "background" && session.Running
	}) {
		t.Fatalf("background session is not marked running: %+v", sessions)
	}
	fs.mu.Lock()
	fs.sessions[1].Running = false
	fs.mu.Unlock()
	sessions, err = a.RemoteProjectSessions("box", "~/app")
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(sessions, func(session RemoteSessionView) bool {
		return session.SessionID == "background" && session.Running
	}) {
		t.Fatalf("authoritative idle listing did not clear stale running state: %+v", sessions)
	}
}

func TestBackgroundCompletionNoticeRefreshesRemoteRows(t *testing.T) {
	const currentPath = "session-id:current"
	const backgroundPath = "session-id:background"
	log := &eventLog{}
	a := &App{remoteEventHook: log.add, remoteTabs: map[string]*remoteTab{
		"remote-1": {
			id: "remote-1", gen: 7,
			routing: remoteTabSessionRouting{
				currentPath: currentPath,
				running:     map[string]bool{backgroundPath: true},
			},
		},
	}}
	if a.routeRemoteTabFrame("remote-1", 7, backgroundPath, "notice") {
		t.Fatal("background completion notice was routed to the foreground")
	}
	if got := log.count("remote-tab:updated"); got != 1 {
		t.Fatalf("background completion emitted %d row refreshes, want 1", got)
	}
}

func TestRecoveredBackgroundTerminalFrameRefreshesRemoteRows(t *testing.T) {
	const currentPath = "session-id:current"
	const originalPath = "session-id:background"
	const recoveredPath = "session-id:background-recovered"
	log := &eventLog{}
	a := &App{remoteEventHook: log.add, remoteTabs: map[string]*remoteTab{
		"remote-1": {
			id: "remote-1", gen: 7,
			routing: remoteTabSessionRouting{
				currentPath: currentPath,
				running:     map[string]bool{originalPath: true},
			},
		},
	}}
	if a.routeRemoteTabFrame("remote-1", 7, recoveredPath, "turn_done") {
		t.Fatal("recovered background terminal frame was routed to the foreground")
	}
	if got := log.count("remote-tab:updated"); got != 1 {
		t.Fatalf("recovered terminal frame emitted %d row refreshes, want 1", got)
	}
}

func TestFocusOnlyAttachRoutesImmediatePendingPrompt(t *testing.T) {
	const currentPath = "session-id:current"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(currentPath), Name: "current", Path: currentPath, Current: true}})
	fs.mu.Lock()
	fs.eventFrames = []string{
		`{"kind":"approval_request","sessionPath":"session-id:current","approval":{"id":"approval-1"}}`,
		`{"kind":"ready","sessionPath":"session-id:current"}`,
	}
	fs.mu.Unlock()
	kernel := &fakeRemoteKernel{
		statuses:    []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView:  RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL},
		ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	log := &eventLog{}
	a := &App{remoteRuntime: kernel, remoteEventHook: log.add}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{})
	events := log.recorded()
	if !slices.ContainsFunc(events, func(event string) bool {
		return strings.HasPrefix(event, "remote-tab:"+meta.ID+":event") && strings.Contains(event, `"approval-1"`)
	}) {
		t.Fatalf("focus-only attach dropped the immediate pending prompt: %v", events)
	}
	a.remoteTabMu.Lock()
	gotPath := a.remoteTabs[meta.ID].routing.currentPath
	a.remoteTabMu.Unlock()
	if gotPath != currentPath {
		t.Fatalf("focus-only path = %q, want %q", gotPath, currentPath)
	}
}

func TestNamedAttachPublishesResolvedRouteBeforeResume(t *testing.T) {
	const targetPath = "session-id:target"
	feed := make(chan string, 1)
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(targetPath), Name: "target", Path: targetPath}})
	fs.mu.Lock()
	fs.eventFeed = feed
	fs.resumeStarted = make(chan string, 1)
	fs.resumeRelease = make(chan struct{})
	started, release := fs.resumeStarted, fs.resumeRelease
	fs.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	kernel := &fakeRemoteKernel{
		statuses:   []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView: RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL}, ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	log := &eventLog{}
	a := &App{remoteRuntime: kernel, remoteEventHook: log.add}
	cleanupRemoteTabPumps(t, a)
	meta, err := a.OpenRemoteProjectTab("box", "~/app", RemoteTabOpenOptions{SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("named resume request did not start")
	}
	feed <- `{"kind":"approval_request","sessionPath":"session-id:target","approval":{"id":"approval-target"}}`
	deadline := time.Now().Add(time.Second)
	for !slices.ContainsFunc(log.recorded(), func(event string) bool {
		return strings.HasPrefix(event, "remote-tab:"+meta.ID+":event") && strings.Contains(event, "approval-target")
	}) {
		if time.Now().After(deadline) {
			t.Fatalf("named target prompt was dropped while /resume was pending: %v", log.recorded())
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	waitForTabState(t, a, meta.ID, "ready")
}

func TestForegroundRecoveryPathUsesFrameMarker(t *testing.T) {
	const oldPath = "session-id:current"
	const recoveryPath = "session-id:current-recovery"
	feed := make(chan string, 1)
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(oldPath), Name: "current", Path: oldPath, Current: true}})
	fs.mu.Lock()
	fs.eventFrames = []string{`{"kind":"ready","sessionPath":"session-id:current"}`}
	fs.eventFeed = feed
	fs.statusPayload = `{"running":false,"sessionName":"current","sessionPath":"session-id:current"}`
	fs.mu.Unlock()
	kernel := &fakeRemoteKernel{
		statuses:   []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView: RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL}, ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	log := &eventLog{}
	a := &App{remoteRuntime: kernel, remoteEventHook: log.add}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{})
	fs.mu.Lock()
	fs.statusPayload = `{"running":false,"sessionName":"current-recovery","sessionPath":"session-id:current-recovery"}`
	fs.mu.Unlock()
	feed <- `{"kind":"notice","text":"continued on recovery","sessionPath":"session-id:current-recovery","sessionCurrent":true}`
	deadline := time.Now().Add(2 * time.Second)
	for {
		events := log.recorded()
		if slices.ContainsFunc(events, func(event string) bool {
			return strings.HasPrefix(event, "remote-tab:"+meta.ID+":event") && strings.Contains(event, "continued on recovery")
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery frame was not routed after foreground marker: %v", events)
		}
		time.Sleep(time.Millisecond)
	}
	a.remoteTabMu.Lock()
	got := a.remoteTabs[meta.ID].routing.currentPath
	a.remoteTabMu.Unlock()
	if got != recoveryPath {
		t.Fatalf("foreground route = %q, want recovered path %q", got, recoveryPath)
	}
}

func TestUnmarkedBackgroundPathNeverProbesOrAdoptsForeground(t *testing.T) {
	const currentPath = "session-id:current"
	const backgroundPath = "session-id:background"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(currentPath), Name: "current", Path: currentPath, Current: true}})
	fs.mu.Lock()
	fs.statusPayload = `{"running":false,"sessionName":"current","sessionPath":"session-id:current"}`
	fs.mu.Unlock()
	kernel := &fakeRemoteKernel{
		statuses:   []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView: RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL}, ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	log := &eventLog{}
	a := &App{remoteRuntime: kernel, remoteEventHook: log.add}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{})
	a.remoteTabMu.Lock()
	gen := a.remoteTabs[meta.ID].gen
	a.remoteTabMu.Unlock()
	countStatusRequests := func() int {
		count := 0
		for _, call := range fs.recorded() {
			if strings.HasPrefix(call, "GET /status") {
				count++
			}
		}
		return count
	}
	before := countStatusRequests()
	beforeRefreshes := log.count("remote-tab:updated")
	if a.routeRemoteTabWireFrame(meta.ID, gen, backgroundPath, "approval_request", false, false) {
		t.Fatal("unknown background frame was routed to the foreground")
	}
	if a.routeRemoteTabWireFrame(meta.ID, gen, backgroundPath, "turn_done", false, false) {
		t.Fatal("known background frame was routed to the foreground")
	}
	after := countStatusRequests()
	if after-before != 0 {
		t.Fatalf("unknown background path triggered %d status requests, want 0", after-before)
	}
	if got := log.count("remote-tab:updated") - beforeRefreshes; got != 1 {
		t.Fatalf("unknown background notice and terminal frame emitted %d row refreshes, want 1", got)
	}
}

func TestKnownBackgroundPathAdoptsFrameForegroundMarker(t *testing.T) {
	const currentPath = "session-id:current"
	const resumedPath = "session-id:background"
	log := &eventLog{}
	a := &App{remoteEventHook: log.add, remoteTabs: map[string]*remoteTab{
		"remote-1": {
			id: "remote-1", gen: 7,
			session: remoteTabSessionState{sessionID: remoteSessionIDFromRoute(currentPath), path: currentPath},
			routing: remoteTabSessionRouting{
				currentPath: currentPath,
				running:     map[string]bool{resumedPath: true},
			},
		},
	}}
	a.adoptRemoteTabFrameCurrent("remote-1", 7, resumedPath, false)
	if !a.routeRemoteTabFrame("remote-1", 7, resumedPath, "text") {
		t.Fatal("publication-time foreground marker did not reclassify a cached background path")
	}
	a.remoteTabMu.Lock()
	path := a.remoteTabs["remote-1"].routing.currentPath
	revision := a.remoteTabs["remote-1"].routing.revision
	a.remoteTabMu.Unlock()
	if path != resumedPath || revision != 1 {
		t.Fatalf("adopted route = %q revision %d, want %q revision 1", path, revision, resumedPath)
	}
	if got := log.count("remote-tab:updated"); got != 1 {
		t.Fatalf("foreground adoption emitted %d row refreshes, want 1", got)
	}
}

func TestForegroundMarkerPublishesRehydrateBeforeForwardedFrame(t *testing.T) {
	const currentPath = "session-id:current"
	const resumedPath = "session-id:resumed"
	log := &eventLog{}
	a := &App{remoteEventHook: log.add, remoteTabs: map[string]*remoteTab{
		"remote-1": {
			id: "remote-1", gen: 7, state: "ready",
			session: remoteTabSessionState{sessionID: remoteSessionIDFromRoute(currentPath), path: currentPath},
			routing: remoteTabSessionRouting{currentPath: currentPath, running: map[string]bool{}},
		},
	}}
	if !a.routeRemoteTabWireFrame("remote-1", 7, resumedPath, "text", true, false) {
		t.Fatal("new foreground frame was not routed")
	}
	a.emitRemoteEvent("remote-tab:remote-1:event", map[string]any{"kind": "text", "sessionPath": resumedPath})
	events := log.recorded()
	stateIndex, frameIndex := -1, -1
	for i, got := range events {
		if strings.HasPrefix(got, "remote-tab:remote-1:state ") {
			stateIndex = i
		}
		if strings.HasPrefix(got, "remote-tab:remote-1:event ") {
			frameIndex = i
		}
	}
	if stateIndex < 0 || frameIndex < 0 || stateIndex >= frameIndex {
		t.Fatalf("session rehydrate was not published before the frame: %v", events)
	}
}

func TestProvisionalResumeRouteFencesStaleSessionListing(t *testing.T) {
	const currentPath = "session-id:current"
	const targetPath = "session-id:target"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{
		{SessionID: remoteSessionIDFromRoute(currentPath), Name: "current", Path: currentPath, Current: true},
		{SessionID: remoteSessionIDFromRoute(targetPath), Name: "target", Path: targetPath},
	})
	kernel := &fakeRemoteKernel{
		statuses:   []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView: RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL}, ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	a := &App{remoteRuntime: kernel}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{SessionID: remoteSessionIDFromRoute(currentPath)})

	fs.mu.Lock()
	fs.sessionsStarted = make(chan struct{}, 1)
	fs.sessionsRelease = make(chan struct{})
	sessionsStarted, sessionsRelease := fs.sessionsStarted, fs.sessionsRelease
	fs.resumeStarted = make(chan string, 1)
	fs.resumeRelease = make(chan struct{})
	resumeStarted, resumeRelease := fs.resumeStarted, fs.resumeRelease
	fs.mu.Unlock()
	t.Cleanup(func() {
		for _, ch := range []chan struct{}{sessionsRelease, resumeRelease} {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	})

	listingDone := make(chan error, 1)
	go func() {
		_, err := a.RemoteProjectSessions("box", "~/app")
		listingDone <- err
	}()
	select {
	case <-sessionsStarted:
	case <-time.After(time.Second):
		t.Fatal("session listing did not start")
	}
	resumeDone := make(chan struct{})
	go func() {
		a.resumeRemoteTabSessionPathForOpenSelection(meta.ID, "target", targetPath, "Target", 0, nil)
		close(resumeDone)
	}()
	select {
	case <-resumeStarted:
	case <-time.After(time.Second):
		t.Fatal("resume request did not start")
	}
	close(sessionsRelease)
	if err := <-listingDone; err != nil {
		t.Fatal(err)
	}
	a.remoteTabMu.Lock()
	got := a.remoteTabs[meta.ID].routing.currentPath
	a.remoteTabMu.Unlock()
	if got != targetPath {
		t.Fatalf("stale /sessions response replaced provisional route with %q, want %q", got, targetPath)
	}
	close(resumeRelease)
	select {
	case <-resumeDone:
	case <-time.After(time.Second):
		t.Fatal("resume did not finish after release")
	}
}

func TestRemoteNewSessionFencesStaleSessionListing(t *testing.T) {
	const currentPath = "session-id:current"
	const rotatedPath = "session-id:rotated"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(currentPath), Name: "current", Path: currentPath, Current: true}})
	kernel := &fakeRemoteKernel{
		statuses:   []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView: RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL}, ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	a := &App{remoteRuntime: kernel}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{SessionID: remoteSessionIDFromRoute(currentPath)})

	fs.mu.Lock()
	fs.sessionsStarted = make(chan struct{}, 1)
	fs.sessionsRelease = make(chan struct{})
	fs.newSessionPath = rotatedPath
	started, release := fs.sessionsStarted, fs.sessionsRelease
	fs.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	listingDone := make(chan error, 1)
	go func() {
		_, err := a.RemoteProjectSessions("box", "~/app")
		listingDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session listing did not start")
	}
	if err := a.resetRemoteTabSession(meta.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-listingDone; err != nil {
		t.Fatal(err)
	}
	a.remoteTabMu.Lock()
	path := a.remoteTabs[meta.ID].routing.currentPath
	a.remoteTabMu.Unlock()
	if path != rotatedPath {
		t.Fatalf("stale /sessions response replaced rotated route with %q, want %q", path, rotatedPath)
	}
}

func TestFailedProvisionalResumeRestoresRouteWithNewRevision(t *testing.T) {
	const currentPath = "session-id:current"
	const targetPath = "session-id:target"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(currentPath), Name: "current", Path: currentPath, Current: true}})
	kernel := &fakeRemoteKernel{
		statuses:   []RemoteConnectionStatusView{{HostID: "box", State: "connected"}},
		ensureView: RemoteServerView{HostID: "box", State: "ready", LocalURL: fs.server.URL}, ensureToken: "s3cret",
	}
	seedBridgeTestHost(t, "box")
	a := &App{remoteRuntime: kernel}
	cleanupRemoteTabPumps(t, a)
	meta := openReadyRemoteTab(t, a, RemoteTabOpenOptions{SessionID: remoteSessionIDFromRoute(currentPath)})
	a.remoteTabMu.Lock()
	before := a.remoteTabs[meta.ID].routing.revision
	a.remoteTabMu.Unlock()
	fs.mu.Lock()
	fs.failEnter = "resume rejected"
	fs.mu.Unlock()
	a.resumeRemoteTabSessionPathForOpenSelection(meta.ID, "target", targetPath, "Target", 0, nil)
	a.remoteTabMu.Lock()
	tab := a.remoteTabs[meta.ID]
	path, revision := tab.routing.currentPath, tab.routing.revision
	a.remoteTabMu.Unlock()
	if path != currentPath || revision != before+2 {
		t.Fatalf("failed resume route = %q revision %d, want %q revision %d", path, revision, currentPath, before+2)
	}
}

func TestRemoteProjectSessionsAdoptsAuthoritativeCurrentRoute(t *testing.T) {
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute("session-id:a"), Name: "a", Path: "session-id:a"}, {SessionID: remoteSessionIDFromRoute("session-id:b"), Name: "b", Path: "session-id:b", Current: true}})
	seedBridgeTestHost(t, "box")
	client, _ := remoteSessionTestClient(t, fs)
	log := &eventLog{}
	tab := &remoteTab{
		id: "remote-1", ref: RemoteTabRef{HostID: "box", Workspace: "~/app"}, state: "ready",
		client: client, base: fs.server.URL, gen: 7,
		session:       remoteTabSessionState{sessionID: remoteSessionIDFromRoute("session-id:a"), name: "a", path: "session-id:a"},
		routing:       remoteTabSessionRouting{currentPath: "session-id:a", running: map[string]bool{}},
		pendingEvents: map[string]json.RawMessage{"approval_request:1": json.RawMessage(`{"kind":"approval_request"}`)},
		runtime:       remoteTabRuntimeState{pendingPrompt: true},
	}
	a := &App{remoteEventHook: log.add, remoteTabs: map[string]*remoteTab{tab.id: tab}}
	readyPrefix := "remote-tab:" + tab.id + ":state"
	sessions, err := a.RemoteProjectSessions("box", "~/app")
	if err != nil {
		t.Fatal(err)
	}
	current := slices.DeleteFunc(append([]RemoteSessionView(nil), sessions...), func(session RemoteSessionView) bool { return !session.Current })
	if len(current) != 1 || remoteSessionIdentityRoute("", current[0].SessionID) != "session-id:b" {
		t.Fatalf("current rows = %+v, want only authoritative session b", current)
	}
	a.remoteTabMu.Lock()
	name, path, route := tab.session.name, tab.session.path, tab.routing.currentPath
	pendingEvents, pendingPrompt := len(tab.pendingEvents), tab.runtime.pendingPrompt
	a.remoteTabMu.Unlock()
	if name != "b" || route != "session-id:b" {
		t.Fatalf("adopted identity = %q/%q/%q, want b//b.jsonl//b.jsonl", name, path, route)
	}
	if pendingEvents != 0 || pendingPrompt {
		t.Fatalf("listing adoption retained stale prompts: events=%d pending=%v", pendingEvents, pendingPrompt)
	}
	if got := log.count(readyPrefix); got != 1 {
		t.Fatalf("listing adoption emitted %d ready barriers, want 1", got)
	}
}

func TestExternalSessionResetPreservesBlankIdentity(t *testing.T) {
	const oldPath = "session-id:old"
	const freshPath = "session-id:fresh"
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute(oldPath), Name: "old", Path: oldPath}})
	seedBridgeTestHost(t, "box")
	client, _ := remoteSessionTestClient(t, fs)
	log := &eventLog{}
	tab := &remoteTab{
		id: "remote-1", ref: RemoteTabRef{HostID: "box", Workspace: "~/app"}, state: "ready",
		client: client, base: fs.server.URL, gen: 7,
		topicTitle: "Old title",
		session:    remoteTabSessionState{sessionID: remoteSessionIDFromRoute(oldPath), name: "old", path: oldPath},
		routing:    remoteTabSessionRouting{currentPath: oldPath, running: map[string]bool{}},
		pendingEvents: map[string]json.RawMessage{
			"approval_request:old": json.RawMessage(`{"kind":"approval_request"}`),
		},
		runtime: remoteTabRuntimeState{running: true, pendingPrompt: true, cancellable: true},
	}
	a := &App{remoteEventHook: log.add, remoteTabs: map[string]*remoteTab{tab.id: tab}}
	if !a.routeRemoteTabWireFrame(tab.id, tab.gen, freshPath, "session_changed", true, true) {
		t.Fatal("fresh external session barrier was not routed")
	}
	a.remoteTabMu.Lock()
	name, path, route, title := tab.session.name, remoteSessionIdentityRoute(tab.session.path, tab.session.sessionID), tab.routing.currentPath, tab.topicTitle
	reset, newSession := tab.session.reset, tab.session.newSession
	pending := len(tab.pendingEvents)
	running, pendingPrompt := tab.runtime.running, tab.runtime.pendingPrompt
	a.remoteTabMu.Unlock()
	if name != "" || path != freshPath || route != freshPath || title != a.localizedDefaultTopicTitle() || !reset || !newSession {
		t.Fatalf("external reset identity = %q/%q/%q/%q reset=%v new=%v", name, path, route, title, reset, newSession)
	}
	if pending != 0 || running || pendingPrompt {
		t.Fatalf("external reset retained old runtime: pending=%d running=%v prompt=%v", pending, running, pendingPrompt)
	}
	if got := log.count("remote-tab:" + tab.id + ":state"); got != 1 {
		t.Fatalf("external reset emitted %d ready barriers, want 1", got)
	}
	sessions, err := a.RemoteProjectSessions("box", "~/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || !slices.ContainsFunc(sessions, func(session RemoteSessionView) bool {
		return session.SessionID == remoteSessionIDFromRoute(freshPath) && session.Current
	}) {
		t.Fatalf("external blank session missing from listing: %+v", sessions)
	}
}

func remoteSessionTestClient(t *testing.T, fs *fakeServe) (*http.Client, context.Context) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	if err := serveHandshake(ctx, client, fs.server.URL, "s3cret"); err != nil {
		t.Fatal(err)
	}
	return client, ctx
}

func TestEnterRemoteSessionIdentitySkipsSessionCatalog(t *testing.T) {
	fs := newFakeServe(t, "s3cret", nil)
	client, ctx := remoteSessionTestClient(t, fs)
	target, err := enterRemoteSessionTarget(ctx, client, fs.server.URL, RemoteTabOpenOptions{SessionID: "known", SessionTitle: "Known"})
	if err != nil {
		t.Fatal(err)
	}
	if target.SessionID != "known" || target.Title != "Known" {
		t.Fatalf("target = %+v", target)
	}
	for _, call := range fs.recorded() {
		if strings.HasPrefix(call, "GET /sessions") {
			t.Fatalf("explicit path unnecessarily fetched the session catalog: %v", fs.recorded())
		}
	}
}

func TestEnterRemoteSessionIdentityReturnsSpectatorMountSynchronously(t *testing.T) {
	const path = "session-id:taken-over"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/resume" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Deepzora-Session-ID", remoteSessionIDFromRoute(path))
		w.Header().Set(sessionTakenOverHeader, "true")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target, err := enterRemoteSessionTarget(context.Background(), srv.Client(), srv.URL, RemoteTabOpenOptions{SessionID: remoteSessionIDFromRoute(path)})
	if err != nil {
		t.Fatal(err)
	}
	if !target.TakenOver {
		t.Fatalf("resume target = %+v, want synchronous spectator marker", target)
	}
	tab := &remoteTab{
		id: "remote-1", gen: 3,
		routing: remoteTabSessionRouting{currentPath: path, running: map[string]bool{}},
	}
	app := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
	if !app.commitRemoteTabAttachResponse(tab.id, tab, tab.gen, tab.routing.pathRevision, target, false) {
		t.Fatal("spectator attach response was not committed")
	}
	if !tab.session.takenOver {
		t.Fatal("spectator marker was not committed before ready publication")
	}
}

func TestRemoteTabResumeCommitsSpectatorBeforeReady(t *testing.T) {
	const path = "session-id:taken-over"
	client := &http.Client{}
	tab := &remoteTab{
		id: "remote-1", gen: 4, state: "ready", client: client,
		routing: remoteTabSessionRouting{currentPath: path, running: map[string]bool{}},
	}
	app := &App{remoteTabs: map[string]*remoteTab{tab.id: tab}}
	route := remoteTabProvisionalResume{targetPath: path, pathRevision: tab.routing.pathRevision}
	meta, committed := app.commitRemoteTabResume(tab.id, tab, client, tab.gen, route, serveSessionEntry{SessionID: remoteSessionIDFromRoute(path), Path: path, TakenOver: true}, "Taken over")
	if !committed || !tab.session.takenOver || !meta.TakenOver || !meta.ReadOnly {
		t.Fatalf("resume spectator commit = committed=%v tab=%v meta=%+v", committed, tab.session.takenOver, meta)
	}
}

func TestEnterRemoteSessionUnknownIdentity(t *testing.T) {
	fs := newFakeServe(t, "s3cret", []serveSessionEntry{{SessionID: remoteSessionIDFromRoute("session-id:x"), Name: "s1", Path: "session-id:x"}})
	client, ctx := remoteSessionTestClient(t, fs)
	fs.failEnter = `remote session "missing" not found`
	_, err := enterRemoteSessionTarget(ctx, client, fs.server.URL, RemoteTabOpenOptions{SessionID: "missing"})
	if err == nil || !strings.Contains(err.Error(), `"missing" not found`) {
		t.Fatalf("err = %v, want unknown session error", err)
	}
}

func TestRemoteSessionResumeBodyUsesCanonicalIdentity(t *testing.T) {
	body, err := remoteSessionResumeBody(serveSessionEntry{Name: "chat-title", SessionID: "stable-session"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["sessionId"] != "stable-session" || got["path"] != "" || got["name"] != "" {
		t.Fatalf("canonical resume body = %v", got)
	}
}

func TestRemoteSessionResumeBodyRejectsMissingIdentity(t *testing.T) {
	if _, err := remoteSessionResumeBody(serveSessionEntry{Name: "unresolved", Path: "session-id:unresolved"}); err == nil || !strings.Contains(err.Error(), "no durable identity") {
		t.Fatalf("missing identity error = %v", err)
	}
}
