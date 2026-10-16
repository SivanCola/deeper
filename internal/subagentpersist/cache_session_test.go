package subagentpersist

import (
	"testing"

	"deepzora/internal/agent"
)

func TestChildCacheIdentitySurvivesContinuationAndChangesOnCopy(t *testing.T) {
	store, _ := testStore(t, t.TempDir())
	handle, err := store.Create(t.Context(), agent.SubagentMeta{Ref: "sa_cache"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := handle.(agent.SessionCacheIdentity).ProviderCacheSessionIdentity()
	if identity == "" {
		t.Fatal("child has no cache identity")
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	resumed, err := store.Open(t.Context(), "sa_cache")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.(agent.SessionCacheIdentity).ProviderCacheSessionIdentity() != identity {
		t.Fatal("continuation changed identity")
	}
	if err := resumed.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	copy, err := store.Copy(t.Context(), "sa_cache", agent.SubagentMeta{Ref: "sa_copy"})
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close(t.Context())
	if copy.(agent.SessionCacheIdentity).ProviderCacheSessionIdentity() == identity {
		t.Fatal("copied child reused source identity")
	}
}
