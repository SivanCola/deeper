package reporttransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostPreservesProtocolWithOptionalToken(t *testing.T) {
	for _, token := range []string{"", strings.Repeat("a", 64)} {
		t.Run("token="+token[:min(1, len(token))], func(t *testing.T) {
			t.Setenv("DEEPZORA_REPORT_TOKEN", token)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get(TokenHeader) != token {
					t.Error("report request contract changed")
				}
				var report map[string]string
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil || report["kind"] != "feedback" || report["message"] != "你好" {
					t.Errorf("report payload = %#v, err = %v", report, err)
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()
			if err := Post(context.Background(), srv.Client(), srv.URL, map[string]string{"kind": "feedback", "message": "你好"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostDoesNotForwardTokenOrReportOnRedirect(t *testing.T) {
	t.Setenv("DEEPZORA_REPORT_TOKEN", strings.Repeat("b", 64))
	for _, status := range []int{301, 302, 307, 308} {
		destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("report followed a redirect")
		}))
		collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, destination.URL, status)
		}))
		if err := Post(context.Background(), collector.Client(), collector.URL, map[string]string{"message": "private"}); err == nil {
			t.Fatal("redirect treated as a successful upload")
		}
		collector.Close()
		destination.Close()
	}
}

func TestPostRejectsUnsafeTokenTransportAndFailedUploads(t *testing.T) {
	t.Setenv("DEEPZORA_REPORT_TOKEN", strings.Repeat("c", 64))
	for _, endpoint := range []string{"http://example.com/report", "http://127.0.0.1.example.com/report", "ftp://localhost/report"} {
		if err := Post(context.Background(), &http.Client{}, endpoint, nil); err == nil || !strings.Contains(err.Error(), "requires HTTPS") {
			t.Fatalf("unsafe endpoint accepted: %s, err=%v", endpoint, err)
		}
	}
	for _, status := range []int{400, 401, 413, 429, 503} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		if err := Post(context.Background(), srv.Client(), srv.URL, nil); err == nil {
			t.Fatalf("failed upload (%d) accepted", status)
		}
		srv.Close()
	}
}
