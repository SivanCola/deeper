package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/agent/testutil"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/session"
	"deepzora/internal/tool"
)

func newHistoryCommandServe(t *testing.T) (*Server, *control.Controller, *session.Service, session.SessionRef) {
	t.Helper()
	srv, ctrl, service, ref := newExclusiveSessionServeWithOptions(t, func(opts *control.Options) {
		exec := agent.New(testutil.NewMock("history", testutil.Turn{Text: "first"}, testutil.Turn{Text: "second"}), tool.NewRegistry(), agent.NewSession("system"), agent.Options{}, opts.Sink)
		opts.Runner, opts.Executor = exec, exec
	})
	t.Cleanup(srv.Close)
	for _, input := range []string{"one", "two"} {
		if err := ctrl.RunTurn(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	return srv, ctrl, service, ref
}

func TestCurrentHistoryRotationsPublishIdentityBeforeRuntime(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/submit", `{"input":"/branch child"}`},
		{"/submit", `{"input":"/branch 2 child"}`},
		{"/submit", `{"input":"/switch saved"}`},
		{"/submit", `{"input":"/rewind 1 conversation"}`},
		{"/fork", `{"turn":1}`},
		{"/rewind", `{"turn":1,"scope":"conversation"}`},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			srv, ctrl, service, source := newHistoryCommandServe(t)
			saved, err := service.Create(t.Context(), session.CreateOptions{SessionID: "saved"})
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Close(t.Context(), saved.Ref()); err != nil {
				t.Fatal(err)
			}
			all, stop := srv.bc.Subscribe()
			defer stop()
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			operatorHandler(srv).ServeHTTP(response, request)
			if response.Code != http.StatusOK && response.Code != http.StatusNoContent {
				t.Fatalf("rotation = %d: %s", response.Code, response.Body.String())
			}
			current, bound := ctrl.SessionRef()
			if !bound || current == source || response.Header().Get(sessionIDHeader) != current.SessionID {
				t.Fatal("response did not publish the new immutable identity")
			}
			if tc.path == "/fork" {
				var result map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result) != 1 || result["sessionId"] != current.SessionID {
					t.Fatalf("fork identity contract = %+v, %v", result, err)
				}
			}
			var frame eventwire.Event
			for {
				frame = nextServeProtocolFrame(t, all, func(state eventwire.Event) {
					if state.SessionID != source.SessionID {
						t.Fatal("runtime state preceded the routing barrier")
					}
				})
				if frame.Kind != "notice" {
					break
				}
				if frame.SessionID != source.SessionID {
					t.Fatal("new session notice preceded the routing barrier")
				}
			}
			if frame.Kind != "session_changed" || frame.SessionID != current.SessionID || !frame.SessionCurrent || frame.SessionPath != "" {
				t.Fatalf("routing barrier = %+v", frame)
			}
			for len(all) > 0 {
				<-all
			}
			srv.tagFor(ctrl).Emit(event.Event{Kind: event.Notice, Text: "after rotation"})
			if err := json.Unmarshal(<-all, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.SessionID != current.SessionID || !frame.SessionCurrent {
				t.Fatalf("subsequent event retained the source route: %+v", frame)
			}
		})
	}
}

func TestCurrentHistoryRotationRefusalPreservesRoute(t *testing.T) {
	srv, ctrl, service, source := newHistoryCommandServe(t)
	before, err := service.Query().List(t.Context(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, expected string }{
		{"/branch child", "foreign-session"},
		{"/branch 0", source.SessionID},
		{"/switch missing", source.SessionID},
	} {
		request := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{"input":"`+tc.input+`"}`))
		request.Header.Set(expectedSessionIDHeader, tc.expected)
		response := httptest.NewRecorder()
		srv.submit(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("refusal for %s = %d: %s", tc.input, response.Code, response.Body.String())
		}
		if current, _ := ctrl.SessionRef(); current != source {
			t.Fatal("refused rotation changed the source binding")
		}
	}
	after, err := service.Query().List(t.Context(), "", 100)
	if err != nil || len(after.Sessions) != len(before.Sessions) {
		t.Fatalf("refused rotation created a session: %+v, %v", after, err)
	}
	all, stop := srv.bc.Subscribe()
	defer stop()
	srv.tagFor(ctrl).Emit(event.Event{Kind: event.Notice, Text: "source remains active"})
	var frame eventwire.Event
	if err := json.Unmarshal(<-all, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.SessionID != source.SessionID || !frame.SessionCurrent {
		t.Fatalf("refused rotation left the wrong event route: %+v", frame)
	}
}
