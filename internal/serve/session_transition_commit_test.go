package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/session"
)

func TestBranchTransitionPublishesMustDeliverRoute(t *testing.T) {
	srv, ctrl, service, initial := newExclusiveSessionServe(t)
	defer srv.Close()
	runtime, ok := service.Runtime(initial)
	if !ok {
		t.Fatal("source runtime is not attached")
	}
	if _, err := runtime.Session().Append(t.Context(), session.Batch{
		OperationID: "source-turn", TurnID: "source-turn", Events: []session.Event{
			{Kind: "turn/start"},
			{Kind: "message/complete", Payload: json.RawMessage(`{"message":{"id":"reply","role":"assistant","content":"branch me"}}`)},
			{Kind: "turn/end", Payload: json.RawMessage(`{"status":"completed"}`)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	bc := srv.bc
	all, stop := bc.Subscribe()
	defer stop()
	for range subscriberBufferSize - subscriberPriorityReserve {
		bc.Emit(event.Event{Kind: event.Text, Text: "delta", SessionID: initial.SessionID})
	}
	for range subscriberPriorityReserve {
		bc.Emit(event.Event{Kind: event.Notice, Text: "priority", SessionID: initial.SessionID})
	}
	if got := len(all); got != subscriberBufferSize {
		t.Fatalf("saturated subscriber length = %d, want %d", got, subscriberBufferSize)
	}

	response := httptest.NewRecorder()
	srv.submit(response, httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(`{"input":"/branch child"}`)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("branch status = %d: %s", response.Code, response.Body.String())
	}
	child, bound := ctrl.SessionRef()
	if !bound || child == initial || response.Header().Get(sessionIDHeader) != child.SessionID {
		t.Fatal("branch response preceded current identity publication")
	}
	childID := child.SessionID
	found := false
	for len(all) > 0 {
		var frame eventwire.Event
		if err := json.Unmarshal(<-all, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Kind == "session_changed" && frame.SessionID == childID && frame.SessionPath == "" && frame.SessionCurrent {
			found = true
		}
	}
	if !found {
		t.Fatalf("saturated subscriber lost branch route to %q", childID)
	}
}
