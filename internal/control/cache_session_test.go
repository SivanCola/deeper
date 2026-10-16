package control

import (
	"context"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
)

func TestCurrentProviderCacheIdentitySurvivesControllerReopen(t *testing.T) {
	c, service, runtime := exclusiveTestController(t, event.Discard)
	ref := runtime.Ref()
	identity := c.ProviderCacheSessionIdentity()
	if identity == "" {
		t.Fatal("current session has no cache identity")
	}
	if _, err := c.OpenSession(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if c.ProviderCacheSessionIdentity() != identity {
		t.Fatal("same-session open changed provider identity")
	}
	c.Close()
	<-c.Closed()
	binding, err := service.Open(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	reopened := newOwnedTestController(t, Options{Executor: exec, SessionService: service, SessionRuntime: binding.Runtime(), ExclusiveSession: true})
	if err := binding.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reopened.ProviderCacheSessionIdentity() != identity {
		t.Fatal("cold runtime changed provider cache identity")
	}
	if _, err := reopened.BindFreshSession(t.Context(), "different"); err != nil {
		t.Fatal(err)
	}
	if reopened.ProviderCacheSessionIdentity() == identity {
		t.Fatal("new session reused parent provider identity")
	}
}
