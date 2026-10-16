package agent

import (
	"context"
	"deepzora/internal/provider"
	"net/http"
	"strings"
	"testing"
)

func TestProviderCacheSessionLifecycle(t *testing.T) {
	makeAgent := func() *Agent { a := &Agent{}; a.sess.conversation = NewSession(""); return a }
	id := func(a *Agent, ctx context.Context) string {
		req, _ := http.NewRequestWithContext(a.withProviderCacheSession(ctx), http.MethodPost, "https://opencode.ai/zen/go/v1/responses", nil)
		provider.ApplyOpenCodeGoHeaders(req, "https://opencode.ai/zen/go/v1", provider.NewClientIdentityHeaders())
		return req.Header.Get("x-opencode-session")
	}
	a := makeAgent()
	ctx := context.Background()
	first := id(a, ctx)
	if first != id(a, ctx) {
		t.Fatal("ephemeral conversation changed between turns")
	}
	if id(makeAgent(), a.withProviderCacheSession(ctx)) == first {
		t.Fatal("child reused parent session")
	}
	a.SetSessionCheckpointer(cacheIdentityOwner("current-conversation"))
	bound := id(a, ctx)
	b := makeAgent()
	b.SetSessionCheckpointer(cacheIdentityOwner("current-conversation"))
	if bound != id(b, ctx) || strings.Contains(bound, "private") {
		t.Fatal("resume identity changed or exposed private identity")
	}
	b.SetSessionCheckpointer(cacheIdentityOwner("new-conversation"))
	if bound == id(b, ctx) {
		t.Fatal("new session reused old identity")
	}
}

type cacheIdentityOwner string

func (c cacheIdentityOwner) ProviderCacheSessionIdentity() string { return "/private/" + string(c) }
func (cacheIdentityOwner) CheckpointSession(context.Context, SessionCheckpointBoundary) error {
	return nil
}
