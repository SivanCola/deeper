package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHistoricalTranscriptSelectorsAreRejected(t *testing.T) {
	srv, _, _, _ := newExclusiveSessionServe(t)
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/resume", `{"path":"/old.jsonl"}`}, {"POST", "/delete-session", `{"name":"old"}`},
		{"POST", "/handoff", `{"sessionPath":"/old.jsonl","targetWriterId":"writer"}`},
		{"POST", "/adopt", `{"sessionPath":"/old.jsonl","writerId":"writer"}`},
		{"GET", "/ownership?session=/old.jsonl", ""}, {"GET", "/status?session=/old.jsonl", ""},
		{"GET", "/history?session=/old.jsonl", ""},
	} {
		r := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, strings.NewReader(tc.body))
		if tc.method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		operatorHandler(srv).ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s = %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
