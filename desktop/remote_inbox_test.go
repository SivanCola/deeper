package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/serve"
	"deepzora/internal/session"
	"deepzora/internal/sessioninbox"
)

type remoteInboxRunner struct {
	started chan string
	release chan struct{}
}

func (r remoteInboxRunner) Run(ctx context.Context, input string) error {
	r.started <- input
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type remoteInboxSink struct {
	states chan event.RuntimeStateSnapshot
}

func (*remoteInboxSink) Emit(event.Event) {}
func (s *remoteInboxSink) RuntimeStateChanged(state event.RuntimeStateSnapshot) {
	s.states <- state
}

func TestRemoteRuntimeInboxReceiptSnapshotAndConsumptionOverHTTP(t *testing.T) {
	isolateDesktopUserDirs(t)
	runner := remoteInboxRunner{started: make(chan string, 2), release: make(chan struct{}, 2)}
	sink := &remoteInboxSink{states: make(chan event.RuntimeStateSnapshot, 64)}
	ctrl, path := newRemoteCanonicalController(t, runner, sink)
	t.Cleanup(func() {
		defer ctrl.Close()
		before := ctrl.RuntimeStateSnapshot()
		if before.Phase != "executing" && before.Phase != "finishing" {
			return
		}
		_ = ctrl.SetInboxPaused(true)
		ctrl.Cancel()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case state := <-sink.states:
				if state.Phase == "idle" && state.Revision > before.Revision {
					return
				}
			case <-deadline.C:
				t.Error("isolated inbox runner did not settle before cleanup")
				return
			}
		}
	})
	server := httptest.NewServer(operatorServeHandler(serve.New(ctrl, nil, config.ServeConfig{})))
	defer server.Close()
	a, tab := remoteRuntimeTestApp(server.Client())
	tab.base, tab.routing.currentPath, tab.session.path = server.URL, path, path
	ctrl.Send("hold first turn")
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first turn did not start")
	}
	target, err := a.CaptureInboxTarget(tab.id, path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := a.EnqueueInboxFollowupForTarget(target, "rich follow-up", "next input", nil, "remote-inbox-key")
	if err != nil || receipt.ItemID == "" {
		t.Fatalf("enqueue = %+v, %v", receipt, err)
	}
	snapshot, err := a.InboxSnapshot(tab.id)
	if err != nil || snapshot.SessionPath != path || len(snapshot.Items) != 1 || snapshot.Items[0].ID != receipt.ItemID || snapshot.Items[0].Preview != "rich follow-up" || snapshot.MaxItems == 0 || snapshot.Bytes == 0 {
		t.Fatalf("receipt did not resolve to durable composer snapshot: %+v, %v", snapshot, err)
	}
	runner.release <- struct{}{}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("durable follow-up was not dispatched")
	}
	active, err := a.InboxSnapshot(tab.id)
	running := ctrl.RuntimeStateSnapshot()
	if err != nil || len(active.Items) != 1 || active.Items[0].ID != receipt.ItemID || active.Items[0].State != "running" || active.Revision <= snapshot.Revision || !running.Running {
		t.Fatalf("dispatched follow-up did not publish its composer-hidden state while still running: %+v, %v", active, err)
	}
	runner.release <- struct{}{}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case state := <-sink.states:
			// The buffered sink also contains idle states from the first turn.
			if state.Phase != "idle" || state.Revision <= running.Revision {
				continue
			}
			consumed, err := a.InboxSnapshot(tab.id)
			if err != nil || consumed.Items == nil || len(consumed.Items) != 0 || consumed.Revision <= snapshot.Revision {
				t.Fatalf("consumption did not remove durable item: %+v, %v", consumed, err)
			}
			return
		case <-deadline.C:
			t.Fatal("follow-up did not settle")
		}
	}
}

func TestRemoteRuntimeInboxRejectsStaleResponseIdentity(t *testing.T) {
	for _, change := range []string{"generation", "selection", "client", "base", "path", "rehydrating", "replaced", "closed"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != "/inbox" || req.URL.Query().Get("session") != runtimeRemoteTestPath {
					t.Errorf("unfenced inbox request: %s", req.URL)
				}
				close(started)
				<-release
				return remoteRuntimeTestResponse(req, http.StatusOK, `{"sessionPath":"`+runtimeRemoteTestPath+`","revision":4,"items":[]}`), nil
			})}
			a, tab := remoteRuntimeTestApp(client)
			done := make(chan error, 1)
			go func() { _, err := a.InboxSnapshot(tab.id); done <- err }()
			<-started
			a.remoteTabMu.Lock()
			switch change {
			case "generation":
				tab.gen++
			case "selection":
				tab.selectionRevision++
			case "client":
				tab.client = &http.Client{}
			case "base":
				tab.base = "http://other.invalid"
			case "path":
				tab.routing.currentPath = "session-id:other"
			case "rehydrating":
				tab.routing.rehydratingPath = runtimeRemoteTestPath
			case "replaced":
				a.remoteTabs[tab.id] = &remoteTab{id: tab.id}
			case "closed":
				delete(a.remoteTabs, tab.id)
			}
			a.remoteTabMu.Unlock()
			close(release)
			if err := <-done; err == nil {
				t.Fatal("stale remote inbox response was accepted")
			}
		})
	}
}

func newRemoteCanonicalController(t *testing.T, runner agent.Runner, sink event.Sink) (*control.Controller, string) {
	t.Helper()
	service, err := session.NewService("local", session.NewFilesystemPersistence(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	executor := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, sink)
	ctrl := control.New(control.Options{Executor: executor, Runner: runner, SessionService: service, Sink: sink})
	if _, err := ctrl.BindFreshSession(t.Context(), "remote-current"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctrl.Close(); <-ctrl.Closed(); _ = service.Shutdown(context.Background()) })
	return ctrl, "session-id:remote-current"
}

func TestRemoteRuntimeInboxRejectsMissingOrWrongResponseSession(t *testing.T) {
	for _, path := range []string{"", "session-id:other"} {
		t.Run(path, func(t *testing.T) {
			data, _ := json.Marshal(sessioninbox.InboxSnapshot{SessionPath: path})
			a, tab := remoteRuntimeTestApp(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return remoteRuntimeTestResponse(req, http.StatusOK, string(data)), nil
			})})
			if _, err := a.InboxSnapshot(tab.id); err == nil {
				t.Fatal("unscoped remote inbox response was accepted")
			}
		})
	}
}
