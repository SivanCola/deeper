package control

import (
	"context"
	"errors"

	"deepzora/internal/provider"
	"deepzora/internal/session"
)

// EnsureSession retains the bound identity or creates a current session before
// accepting user input. Creation failures are returned to the caller; no file
// path or secondary persistence format is allocated.
func (c *Controller) EnsureSession(ctx context.Context, options session.CreateOptions) (session.SessionRef, error) {
	if c == nil {
		return session.SessionRef{}, session.ErrSessionNotRunning
	}
	c.sessionInitMu.Lock()
	defer c.sessionInitMu.Unlock()
	if err := ctx.Err(); err != nil {
		return session.SessionRef{}, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return session.SessionRef{}, session.ErrSessionNotRunning
	}
	if ref, ok := c.SessionRef(); ok {
		return ref, nil
	}
	if service, _, exclusive := c.v3Binding(); !exclusive || service == nil || c.executor == nil {
		return session.SessionRef{}, errors.New("current session service is unavailable")
	}
	if c.executor.Session().HasContent() {
		return session.SessionRef{}, errors.New("cannot create a current session over an unbound conversation")
	}
	return c.BindFreshSessionWithOptions(ctx, options)
}

// AdoptRebuiltModelContext changes only the provider-visible context of a
// bound canonical session; display history stays on its original event stream.
func (c *Controller) AdoptRebuiltModelContext(msgs []provider.Message) error {
	if !c.sessionEngineEnabled() {
		return errors.New("model-context adoption requires an exclusive v3 session")
	}
	if len(msgs) == 0 {
		if snapshot, ok := c.sessionEventSnapshot(); ok {
			msgs = snapshot.Projection.ModelMessages
		}
	}
	return c.replaceSessionModelContext(context.Background(), msgs, "agent-rebuild")
}
