package agent

import (
	"context"
	"deepzora/internal/provider"
)

// SessionCacheIdentity belongs to the current persistence owner. It survives
// reopening the same incarnation without exposing paths in provider headers.
type SessionCacheIdentity interface {
	ProviderCacheSessionIdentity() string
}

func (a *Agent) withProviderCacheSession(ctx context.Context) context.Context {
	return provider.WithCacheSession(ctx, a.cacheSessionIdentity())
}

func (a *Agent) cacheSessionIdentity() string {
	if a == nil {
		return ""
	}
	identity := ""
	if owner, ok := a.svc.sessionCheckpointer.(SessionCacheIdentity); ok {
		identity = owner.ProviderCacheSessionIdentity()
	}
	// Ephemeral agents have no persistence owner. Each conversation gets its
	// own identity instead of inheriting a parent's transport cache header.
	if identity == "" {
		s := a.Session()
		if s == nil {
			return ""
		}
		s.mu.Lock()
		if s.cacheSessionID == "" {
			s.cacheSessionID = provider.NewCacheSessionID()
		}
		identity = s.cacheSessionID
		s.mu.Unlock()
	}
	return identity
}
