package cli

import (
	"net/http/httptest"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/serve"
)

func TestRemoteServeBrowserURLUsesFragmentForCurrentServe(t *testing.T) {
	ctrl := newOwnedTestController(t, control.Options{SessionDir: t.TempDir()})
	t.Cleanup(ctrl.Close)
	srv := serve.New(ctrl, serve.NewBroadcaster(), config.ServeConfig{AuthMode: "token", Token: "current secret/+"})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	bound := strings.TrimPrefix(ts.URL, "http://")
	got := remoteServeBrowserURL(bound, "current secret/+")
	want := ts.URL + "/#token=current+secret%2F%2B"
	if got != want {
		t.Fatalf("remoteServeBrowserURL() = %q, want %q", got, want)
	}
}
