package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/session"
	"deepzora/internal/sessioninbox"
)

type runtimeStateServeRunner struct{ started chan struct{} }

type runtimeStateServeSink struct {
	states chan event.RuntimeStateSnapshot
}

func (*runtimeStateServeSink) Emit(event.Event) {}
func (s *runtimeStateServeSink) RuntimeStateChanged(state event.RuntimeStateSnapshot) {
	s.states <- state
}

func (r runtimeStateServeRunner) Run(ctx context.Context, _ string) error {
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

func runtimeStateServeController(t *testing.T, dir, name string, runner interface {
	Run(context.Context, string) error
}) *control.Controller {
	t.Helper()
	service, err := session.NewService("serve-test", session.NewFilesystemPersistence(filepath.Join(dir, name)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	runtime, err := service.Create(t.Context(), session.CreateOptions{SessionID: name, Origin: session.SessionOriginNew})
	if err != nil {
		t.Fatal(err)
	}
	sink := &runtimeStateServeSink{states: make(chan event.RuntimeStateSnapshot, 64)}
	c := control.New(control.Options{Label: name, Runner: runner, Sink: sink, SessionService: service, SessionCreateService: service, SessionRuntime: runtime, ExclusiveSession: true})
	t.Cleanup(func() { c.Close(); <-c.Closed() })
	return c
}

func runtimeStateHTTPGet(t *testing.T, endpoint string, target any) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status=%d", endpoint, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStateHTTPIncludesDetachedAndKeepsSnapshotImmutable(t *testing.T) {
	dir := t.TempDir()
	foreground := runtimeStateServeController(t, dir, "foreground", nil)
	detached := runtimeStateServeController(t, dir, "detached", nil)
	server := New(foreground, nil, config.ServeConfig{})
	detachedPath := sessionRouteForController(detached)
	server.detached[detachedPath] = &detachedSession{path: detachedPath, ctrl: detached}
	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	var first, second runtimeStatesView
	runtimeStateHTTPGet(t, httpServer.URL+"/runtime-states", &first)
	runtimeStateHTTPGet(t, httpServer.URL+"/runtime-states", &second)
	if first.SchemaVersion != 1 || first.Epoch == "" || first.Revision == 0 || len(first.Sessions) != 2 {
		t.Fatalf("invalid full snapshot: %+v", first)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("unchanged memory produced different versions/content: first=%+v second=%+v", first, second)
	}
	byPath := map[string]runtimeSessionView{}
	for _, session := range first.Sessions {
		byPath[session.SessionPath] = session
	}
	if got := byPath[sessionRouteForController(foreground)]; !got.Current || !reflect.DeepEqual(got.State, foreground.RuntimeStateSnapshot()) {
		t.Fatalf("foreground snapshot mismatch: %+v", got)
	}
	if got := byPath[detachedPath]; got.Current || !reflect.DeepEqual(got.State, detached.RuntimeStateSnapshot()) {
		t.Fatalf("detached snapshot mismatch: %+v", got)
	}
	read := server.runtimeStatesSnapshot()
	read.Sessions[0].State.Phase = "corrupt-test-copy"
	if reflect.DeepEqual(read, server.runtimeStatesSnapshot()) {
		t.Fatal("caller mutated the stored projection through its returned slice")
	}
}

func TestRuntimeStateHTTPStatusUsesRequestedDetachedController(t *testing.T) {
	server, foreground, service, _ := newExclusiveSessionServe(t)
	runner := runtimeStateServeRunner{started: make(chan struct{})}
	detached := control.New(control.Options{Executor: agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard), SessionService: service, ExclusiveSession: true, Runner: runner})
	if _, err := detached.BindFreshSession(t.Context(), "detached"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		detached.Close()
		select {
		case <-detached.Closed():
		case <-time.After(5 * time.Second):
			t.Error("detached controller teardown timed out")
		}
	})
	detachedPath := remoteSessionIDQueryPrefix + "detached"
	server.detached[detachedPath] = &detachedSession{path: detachedPath, ctrl: detached}
	detached.Send("isolated running turn")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("detached runner did not start")
	}
	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	var status struct {
		SessionID    string                     `json:"sessionId"`
		SessionPath  string                     `json:"sessionPath"`
		Running      bool                       `json:"running"`
		RuntimeState event.RuntimeStateSnapshot `json:"runtimeState"`
	}
	runtimeStateHTTPGet(t, httpServer.URL+"/status?runtime=1&session="+url.QueryEscape(detachedPath), &status)
	if status.SessionID != "detached" || status.SessionPath != "" {
		t.Fatalf("detached status path = %q, want canonical identity %q", status.SessionPath, detachedPath)
	}
	if !status.Running || status.RuntimeState.Phase != "executing" || status.RuntimeState.RuntimeEpoch != detached.RuntimeStateSnapshot().RuntimeEpoch {
		t.Fatalf("detached status was borrowed from foreground: %+v", status)
	}
	if foreground.RuntimeStateSnapshot().Running {
		t.Fatal("fixture foreground unexpectedly running")
	}
}

func readRuntimeSSEFrame(t *testing.T, reader *bufio.Reader) eventwire.Event {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame eventwire.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			t.Fatal(err)
		}
		return frame
	}
}

// Runtime snapshots are an additive host-only channel. Existing transcript
// tests still assert their exact protocol sequence after filtering that kind.
func nextServeProtocolFrame(t *testing.T, frames <-chan []byte, beforeRuntime func(eventwire.Event)) eventwire.Event {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case raw := <-frames:
			var frame eventwire.Event
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Kind == "runtime_state" {
				if beforeRuntime != nil {
					beforeRuntime(frame)
				}
				continue
			}
			return frame
		case <-timer.C:
			t.Fatal("expected protocol frame was not delivered")
		}
	}
}

func assertNoServeProtocolFrames(t *testing.T, frames <-chan []byte) {
	t.Helper()
	for {
		select {
		case raw := <-frames:
			var frame eventwire.Event
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Kind != "runtime_state" {
				t.Fatalf("unexpected duplicate protocol frame: %s", raw)
			}
		default:
			return
		}
	}
}

func TestRuntimeStateSSERoutesCurrentIdentitiesAndPreservesHostOnlyPayload(t *testing.T) {
	dir := t.TempDir()
	foreground := runtimeStateServeController(t, dir, "foreground", nil)
	broadcaster := NewBroadcaster()
	server := New(foreground, broadcaster, config.ServeConfig{})
	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	currentResponse, err := client.Get(httpServer.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer currentResponse.Body.Close()
	allResponse, err := client.Get(httpServer.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer allResponse.Body.Close()
	backgroundPath := "session-id:background"
	background := event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "background", Revision: 2, Phase: "idle"}
	current := event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "foreground", Revision: 3, Phase: "finishing", Running: true}
	backgroundSink := newSessionTagSink(broadcaster)
	backgroundSink.SetIdentity("background")
	backgroundSink.RuntimeStateChanged(background)
	foregroundSink := newSessionTagSink(broadcaster)
	foregroundSink.SetIdentity("foreground")
	foregroundSink.RuntimeStateChanged(current)
	currentReader := bufio.NewReader(currentResponse.Body)
	routedBackground := readRuntimeSSEFrame(t, currentReader)
	if routedBackground.SessionPath != backgroundPath || routedBackground.SessionCurrent || routedBackground.RuntimeState == nil || !reflect.DeepEqual(*routedBackground.RuntimeState, background) {
		t.Fatalf("identity-routed background payload changed: %+v", routedBackground)
	}
	currentFrame := readRuntimeSSEFrame(t, currentReader)
	if currentFrame.Kind != "runtime_state" || !currentFrame.SessionCurrent || currentFrame.RuntimeState == nil || !reflect.DeepEqual(*currentFrame.RuntimeState, current) {
		t.Fatalf("current stream received background or changed payload: %+v", currentFrame)
	}
	allReader := bufio.NewReader(allResponse.Body)
	backgroundFrame := readRuntimeSSEFrame(t, allReader)
	if backgroundFrame.Kind != "runtime_state" || backgroundFrame.SessionCurrent || backgroundFrame.SessionPath != backgroundPath || backgroundFrame.RuntimeState == nil || !reflect.DeepEqual(*backgroundFrame.RuntimeState, background) {
		t.Fatalf("all-session background frame mismatch: %+v", backgroundFrame)
	}
	if next := readRuntimeSSEFrame(t, allReader); next.SessionPath != sessionRouteForController(foreground) || !next.SessionCurrent {
		t.Fatalf("all-session foreground frame mismatch: %+v", next)
	}
}

func TestRuntimeStateInboxReceiptRetainsRichFollowupAndSessionFence(t *testing.T) {
	runner := runtimeStateServeRunner{started: make(chan struct{})}
	server, ctrl, _, ref := newExclusiveSessionServeWithOptions(t, func(o *control.Options) { o.Runner = runner })
	route := remoteSessionIDQueryPrefix + ref.SessionID
	t.Cleanup(func() {
		ctrl.Close()
		select {
		case <-ctrl.Closed():
		case <-time.After(5 * time.Second):
			t.Error("foreground controller teardown timed out")
		}
	})
	ctrl.Send("hold isolated foreground turn")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("foreground runner did not start")
	}
	httpServer := httptest.NewServer(operatorHandler(server))
	defer httpServer.Close()
	request := map[string]any{
		"input": "run fixture-skill", "display": "rich display", "intent": "followup", "idempotencyKey": "stable-key",
		"invocations": []control.InvocationRequest{{Name: "fixture-skill", Kind: "skill", Offset: 4}},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/inbox/items", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(expectedSessionIDHeader, ref.SessionID)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("enqueue status=%d", response.StatusCode)
	}
	var enqueued sessioninbox.InboxReceipt
	if err := json.NewDecoder(response.Body).Decode(&enqueued); err != nil {
		t.Fatal(err)
	}
	_, envelope, err := ctrl.ReadInboxItem(enqueued.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.DisplayText != "rich display" || len(envelope.Invocations) != 1 || envelope.Invocations[0].Name != "fixture-skill" || envelope.Invocations[0].Kind != "skill" || envelope.Invocations[0].Offset != 4 {
		t.Fatalf("HTTP enqueue lost rich prompt: %+v", envelope)
	}
	var recovered sessioninbox.InboxReceipt
	runtimeStateHTTPGet(t, httpServer.URL+"/inbox/receipt?key=stable-key&session="+url.QueryEscape(route), &recovered)
	if recovered.ItemID != enqueued.ItemID {
		t.Fatalf("idempotency receipt changed item: enqueued=%+v recovered=%+v", enqueued, recovered)
	}
	var snapshot sessioninbox.InboxSnapshot
	runtimeStateHTTPGet(t, httpServer.URL+"/inbox?session="+url.QueryEscape(route), &snapshot)
	if snapshot.SessionPath != route || len(snapshot.Items) != 1 || snapshot.Items[0].ID != enqueued.ItemID {
		t.Fatalf("remote inbox snapshot does not match receipt: %+v", snapshot)
	}
	for _, endpoint := range []string{"/inbox", "/inbox/receipt?key=stable-key"} {
		for _, fence := range []string{"query", "header", "conflicting"} {
			t.Run(endpoint+"/"+fence, func(t *testing.T) {
				u, err := url.Parse(httpServer.URL + endpoint)
				if err != nil {
					t.Fatal(err)
				}
				query := u.Query()
				wrongPath := remoteSessionIDQueryPrefix + "wrong"
				if fence == "query" || fence == "conflicting" {
					query.Set("session", wrongPath)
				} else {
					query.Set("session", route)
				}
				u.RawQuery = query.Encode()
				req, _ := http.NewRequest(http.MethodGet, u.String(), nil)
				switch fence {
				case "header":
					req.Header.Set(expectedSessionIDHeader, "wrong")
				case "conflicting":
					req.Header.Set(expectedSessionIDHeader, ref.SessionID)
				}
				response, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusConflict {
					t.Fatalf("inbox read crossed %s fence: status=%d", fence, response.StatusCode)
				}
			})
		}
	}
	wrong, err := http.Get(httpServer.URL + "/inbox/receipt?key=stable-key&session=" + url.QueryEscape(remoteSessionIDQueryPrefix+"wrong"))
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Body.Close()
	if wrong.StatusCode != http.StatusConflict {
		t.Fatalf("receipt lookup crossed session fence: status=%d", wrong.StatusCode)
	}
}

func TestRuntimeStateIdentityFrameKeepsBackgroundRoute(t *testing.T) {
	bc := NewBroadcaster()
	bc.SetCurrentSession("session-id:foreground")
	frames, unsubscribe := bc.Subscribe()
	defer unsubscribe()
	tag := newSessionTagSink(bc)
	tag.SetIdentity("background")
	tag.RuntimeStateChanged(event.RuntimeStateSnapshot{SchemaVersion: 1, RuntimeEpoch: "background-runtime", Revision: 2, SessionID: "background", Phase: "idle"})
	select {
	case raw := <-frames:
		var frame eventwire.Event
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.SessionID != "background" || frame.SessionCurrent || frame.SessionPath != "session-id:background" {
			t.Fatalf("background runtime lost identity: %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("background runtime was not published")
	}
}
