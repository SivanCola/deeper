package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteHandshakeRequiresCurrentBaseline(t *testing.T) {
	for _, header := range []string{"", "execution-v2", "deepzora-baseline-v1,execution-v2"} {
		t.Run(header, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(serveCapabilitiesHeader, header)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			_, err := serveHandshakeCapabilities(context.Background(), server.Client(), server.URL, "token")
			if (err == nil) != (header == "deepzora-baseline-v1,execution-v2") {
				t.Fatalf("header %q: %v", header, err)
			}
		})
	}
}
