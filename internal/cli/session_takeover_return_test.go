package cli

import (
	"deepzora/internal/processidentity"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

type takeoverReturnServers struct {
	old, new       *httptest.Server
	mu             sync.Mutex
	oldEnd, newEnd []string
}

func (s *takeoverReturnServers) recordEnd(t *testing.T, r *http.Request, old bool) {
	var body struct {
		MirrorID string `json:"mirrorId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old {
		s.oldEnd = append(s.oldEnd, body.MirrorID)
	} else {
		s.newEnd = append(s.newEnd, body.MirrorID)
	}
}

func newTakeoverReturnServers(t *testing.T, path string) *takeoverReturnServers {
	t.Helper()
	s := &takeoverReturnServers{}
	s.new = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/token":
			w.WriteHeader(http.StatusNoContent)
		case "/adopt":
			_ = json.NewEncoder(w).Encode(cliTakeoverGrant{SessionPath: path, MirrorID: "mirror-new", ReturnHandoffID: "return-new", SourceWriterID: "serve-new", TargetWriterID: processidentity.WriterID()})
		case "/mirror-end":
			s.recordEnd(t, r, false)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	s.old = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror-end" {
			s.recordEnd(t, r, true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	previous := discoverCLIServesForTakeover
	discoverCLIServesForTakeover = func() []cliServeRecord { return []cliServeRecord{{base: s.new.URL, token: "fresh"}} }
	t.Cleanup(func() { discoverCLIServesForTakeover = previous; s.old.Close(); s.new.Close() })
	return s
}

type takeoverReturnEnv struct {
	source, target string
	servers        *takeoverReturnServers
	manager        *cliTakeoverManager
	oldBinding     *cliTakeoverBinding
}

func newTakeoverReturnEnv(t *testing.T) *takeoverReturnEnv {
	t.Helper()
	e := &takeoverReturnEnv{source: cliCanonicalRoute("source"), target: cliCanonicalRoute("target")}
	e.servers = newTakeoverReturnServers(t, e.source)
	e.oldBinding = &cliTakeoverBinding{path: e.source, record: cliServeRecord{base: e.servers.old.URL}, client: e.servers.old.Client(), grant: cliTakeoverGrant{MirrorID: "mirror-old", SourceWriterID: "serve-old", ReturnHandoffID: "return-old"}}
	e.manager = newCLITakeoverManager(&takeoverRecordSink{})
	e.manager.binding, e.manager.revision = e.oldBinding, 1
	e.manager.Emit(event.Event{Kind: event.Text, Text: "flush before return"})
	return e
}

func (e *takeoverReturnEnv) assertReturned(t *testing.T) {
	t.Helper()
	binding, _, _, _ := e.manager.snapshot()
	if binding != nil {
		t.Fatalf("active binding survived return: %+v", binding.grant)
	}
	e.servers.mu.Lock()
	defer e.servers.mu.Unlock()
	if len(e.servers.oldEnd) != 0 || len(e.servers.newEnd) != 1 || e.servers.newEnd[0] != "mirror-new" {
		t.Fatalf("mirror-end old=%v new=%v", e.servers.oldEnd, e.servers.newEnd)
	}
}

func TestCLITakeoverManagerReturnsRefreshedMirrorGeneration(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*takeoverReturnEnv) error
	}{
		{name: "leaveMirror", run: func(e *takeoverReturnEnv) error {
			return e.manager.leaveMirror(e.source)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTakeoverReturnEnv(t)
			if err := tc.run(e); err != nil {
				t.Fatal(err)
			}
			e.assertReturned(t)
		})
	}
}

type failingMirrorSnapshot struct {
	control.SessionAPI
	err error
}

func (c *failingMirrorSnapshot) Snapshot() error { return c.err }

func TestCLITakeoverManagerSnapshotFailureKeepsMirrorActive(t *testing.T) {
	e := newTakeoverReturnEnv(t)
	wantErr := errors.New("injected snapshot failure")
	e.manager.AttachController(&failingMirrorSnapshot{err: wantErr})
	err := e.manager.returnLeaseFor(e.oldBinding, 1)
	if !errors.Is(err, wantErr) {
		t.Fatalf("return error = %v", err)
	}
	binding, _, _, revision := e.manager.snapshot()
	e.manager.mu.Lock()
	queued := e.manager.queue.Len()
	e.manager.mu.Unlock()
	if binding != e.oldBinding || revision != 1 || e.manager.Returned() || queued != 1 {
		t.Fatalf("binding=%+v revision=%d returned=%v queued=%d", binding, revision, e.manager.Returned(), queued)
	}
	e.servers.mu.Lock()
	defer e.servers.mu.Unlock()
	if len(e.servers.oldEnd)+len(e.servers.newEnd) != 0 {
		t.Fatalf("mirror ended after snapshot failure")
	}
}

type mirrorIdentityController struct {
	control.SessionAPI
	control.IdentityLifecycle
	ref session.SessionRef
}

func (c *mirrorIdentityController) SessionRef() (session.SessionRef, bool) { return c.ref, true }

func TestTakeoverMirrorFollowsIdentityChangesOnly(t *testing.T) {
	e := newTakeoverReturnEnv(t)
	ctrl := &mirrorIdentityController{ref: session.SessionRef{SessionID: "source"}}
	m := chatTUI{ctrl: ctrl, takeover: e.manager}
	m.syncTakeoverIdentity()
	if binding, _, _, revision := e.manager.snapshot(); binding != e.oldBinding || revision != 1 {
		t.Fatal("same-session rebuild retired the mirror")
	}
	ctrl.ref.SessionID = "target"
	m.syncTakeoverIdentity()
	e.assertReturned(t)
}

func TestCLITakeoverManagerReclaimAndCloseReturnRefreshedGeneration(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*takeoverReturnEnv) error
	}{
		{name: "reclaim", run: func(e *takeoverReturnEnv) error {
			e.manager.reclaiming.Store(true)
			return e.manager.returnLeaseFor(e.oldBinding, 1)
		}},
		{name: "close", run: func(e *takeoverReturnEnv) error {
			return e.manager.Close()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTakeoverReturnEnv(t)
			if err := tc.run(e); err != nil {
				t.Fatal(err)
			}
			e.assertReturned(t)
		})
	}
}
