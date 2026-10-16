package main

import (
	"net/http"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/sessioninbox"
)

func TestInboxReceiptSurvivesLocalTabReopen(t *testing.T) {
	a, ctrl, path := currentInboxTestApp(t, control.Options{Sink: event.Discard})
	ref, _ := ctrl.SessionRef()
	if err := ctrl.SetInboxPaused(true); err != nil {
		t.Fatal(err)
	}
	a.tabs = map[string]*WorkspaceTab{"old": {ID: "old", Ctrl: ctrl, SessionID: ref.SessionID, SessionGeneration: 1, Ready: true}}
	target, err := a.CaptureInboxTarget("old", path)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := a.EnqueueInboxFollowupForTarget(target, "original", "original", nil, "original-key")
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Close()
	<-ctrl.Closed()
	service := ctrl.SessionService()
	runtime, ok := service.Runtime(ref)
	if !ok {
		t.Fatal("current runtime disappeared during tab close")
	}
	reopened := control.New(control.Options{SessionService: service, SessionRuntime: runtime, ExclusiveSession: true, Sink: event.Discard})
	defer func() { reopened.Close(); <-reopened.Closed() }()
	a.tabs = map[string]*WorkspaceTab{"new": {ID: "new", Ctrl: reopened, SessionID: ref.SessionID, SessionGeneration: 1, Ready: true}}
	actual, err := a.LookupInboxFollowupForTarget(target, "original-key")
	if err != nil || actual.ItemID != receipt.ItemID {
		t.Fatalf("reopened receipt = %+v, %v", actual, err)
	}
	if _, err := a.EnqueueInboxFollowupForTarget(target, "repeat", "repeat", nil, "repeat"); err == nil {
		t.Fatal("old write fence accepted new tab")
	}
	if got := len(reopened.InboxSnapshot().Items); got != 1 {
		t.Fatalf("replayed submission: %d items", got)
	}
	a.detachedSessions = map[string]*WorkspaceTab{sessionRuntimeKey(path): a.tabs["new"]}
	a.tabs = nil
	if actual, err := a.LookupInboxFollowupForTarget(target, "original-key"); err != nil || actual.ItemID != receipt.ItemID {
		t.Fatalf("detached owner receipt = %+v, %v", actual, err)
	}
	a.detachedSessions = nil
	if _, err := a.LookupInboxFollowupForTarget(target, "original-key"); err == nil || len(a.tabs)+len(a.detachedSessions) != 0 {
		t.Fatal("lookup created an owner for an unavailable session")
	}
}

type receiptOwnerHook struct {
	control.SessionAPI
	beforeReturn func()
}

func (c *receiptOwnerHook) LookupInboxReceiptForSession(_, _ string) (sessioninbox.InboxReceipt, bool, error) {
	c.beforeReturn()
	return sessioninbox.InboxReceipt{ItemID: "original"}, true, nil
}

func TestInboxReceiptRejectsLocalOwnerChangedDuringRead(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := "session-id:receipt-current"
	for _, change := range []string{"generation", "controller", "path", "removed", "readonly"} {
		t.Run(change, func(t *testing.T) {
			a := NewApp()
			a.desktopSessions.root = t.TempDir()
			ctrl := newNamedCurrentRebuildController(t, a, "receipt-current", control.Options{Sink: event.Discard})
			hook := &receiptOwnerHook{SessionAPI: ctrl}
			tab := &WorkspaceTab{ID: "new", Ctrl: hook, SessionID: "receipt-current", SessionGeneration: 2}
			a.tabs = map[string]*WorkspaceTab{"new": tab}
			read := false
			hook.beforeReturn = func() {
				read = true
				a.mu.Lock()
				defer a.mu.Unlock()
				switch change {
				case "generation":
					tab.SessionGeneration++
				case "controller":
					tab.Ctrl = ctrl
				case "path":
					tab.SessionID = "other-current"
				case "removed":
					delete(a.tabs, "new")
				case "readonly":
					tab.ReadOnly = true
				}
			}
			if got, err := a.LookupInboxFollowupForTarget(InboxTargetView{TabID: "old", SessionPath: path}, "original"); err == nil || got.ItemID != "" {
				t.Fatalf("stale receipt escaped %s fence: %+v %v", change, got, err)
			}
			if !read {
				t.Fatal("receipt read did not reach the ownership change")
			}
		})
	}
}

func TestInboxReceiptRebindsRemoteTabWithoutReplaying(t *testing.T) {
	isolateDesktopUserDirs(t)
	reads := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Query().Get("session") != runtimeRemoteTestPath || req.URL.Query().Get("key") != "original" {
			t.Errorf("wrong receipt route: %s %s", req.Method, req.URL.Path)
		}
		reads++
		return remoteRuntimeTestResponse(req, 200, `{"itemId":"original","position":0,"disposition":"idempotent_hit","paused":false}`), nil
	})}
	a, tab := remoteRuntimeTestApp(client)
	target, err := a.CaptureInboxTarget(tab.id, runtimeRemoteTestPath)
	if err != nil {
		t.Fatal(err)
	}
	delete(a.remoteTabs, tab.id)
	tab.id = "reopened"
	tab.gen++
	a.remoteTabs[tab.id] = tab
	if got, err := a.LookupInboxFollowupForTarget(target, "original"); err != nil || got.ItemID != "original" {
		t.Fatalf("lookup = %+v %v", got, err)
	}
	if reads != 1 {
		t.Fatalf("reads = %d", reads)
	}
	if _, err := a.EnqueueInboxFollowupForTarget(target, "repeat", "repeat", nil, "repeat"); err == nil {
		t.Fatal("old write target was rebound")
	}
	tab.ref.Workspace = "different-workspace"
	if _, err := a.LookupInboxFollowupForTarget(target, "original"); err == nil || reads != 1 {
		t.Fatal("receipt crossed workspace identity")
	}
}
