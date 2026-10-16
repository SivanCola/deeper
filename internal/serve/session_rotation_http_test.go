package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/eventwire"
)

func TestServePlanDecisionValidatesRequest(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/plan-decision", "application/json", strings.NewReader(`{"action":"revise_plan"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("plan decision missing id = %d, want 400", resp.StatusCode)
	}
}

func TestServeSilentRotationsPublishSessionChanged(t *testing.T) {
	for _, endpoint := range []string{"/clear", "/new"} {
		t.Run(endpoint, func(t *testing.T) {
			server, _, _, oldRef := newExclusiveSessionServe(t)
			bc := server.bc
			all, stop := bc.Subscribe()
			defer stop()
			httpServer := httptest.NewServer(operatorHandler(server))
			defer httpServer.Close()

			resp, err := http.Post(httpServer.URL+endpoint, "application/json", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("%s status = %d, want 204", endpoint, resp.StatusCode)
			}
			frame := nextServeProtocolFrame(t, all, func(state eventwire.Event) {
				if state.SessionID != oldRef.SessionID {
					t.Fatalf("new runtime state preceded session_changed routing barrier: %+v", state)
				}
			})
			if frame.Kind != "session_changed" || !frame.SessionCurrent || !frame.SessionReset || frame.SessionID == "" || frame.SessionID == oldRef.SessionID {
				t.Fatalf("%s routing frame = %+v, old identity %q", endpoint, frame, oldRef.SessionID)
			}
		})
	}
}

func TestServeClearSessionEndpoint(t *testing.T) {
	server, ctrl, _, previous := newExclusiveSessionServe(t)
	srv := httptest.NewServer(operatorHandler(server))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/clear", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("clear session = %d, want 204", resp.StatusCode)
	}
	ref, bound := ctrl.SessionRef()
	if got := resp.Header.Get(sessionIDHeader); !bound || got == previous.SessionID || got != ref.SessionID {
		t.Errorf("clear session ID header = %q, controller ID %q", got, ref.SessionID)
	}
}

func TestServeSubmitClearCompletesRotationBeforeReturning(t *testing.T) {
	server, ctrl, _, previous := newExclusiveSessionServe(t)
	srv := httptest.NewServer(operatorHandler(server))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{"input":"/clear"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("submit clear status = %d, want 204", resp.StatusCode)
	}
	ref, bound := ctrl.SessionRef()
	if got := resp.Header.Get(sessionIDHeader); !bound || got == previous.SessionID || got != ref.SessionID {
		t.Fatalf("submit clear returned ID %q, controller ID %q", got, ref.SessionID)
	}
}

func TestServeNewSessionEndpoint(t *testing.T) {
	server, ctrl, _, previous := newExclusiveSessionServe(t)
	srv := httptest.NewServer(operatorHandler(server))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/new", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("new session = %d, want 204", resp.StatusCode)
	}
	ref, bound := ctrl.SessionRef()
	if got := resp.Header.Get(sessionIDHeader); !bound || got == previous.SessionID || got != ref.SessionID {
		t.Errorf("new session ID header = %q, controller ID %q", got, ref.SessionID)
	}
}

func TestServeManagementSubmitReturnsNoContent(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc})
	srv := httptest.NewServer(operatorHandler(New(ctrl, bc, config.ServeConfig{})))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{"input":"/context"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("management submit = %d, want 204", resp.StatusCode)
	}
}
