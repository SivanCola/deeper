package control

import (
	"context"
	"errors"
	"os"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

// EnsureSessionPath pins a fresh auto-save file for this controller when none is
// set yet and a session dir is configured — the "fresh session" branch every
// surface runs right after building a controller. It is a no-op once a resume or
// continue has already pinned a path (SessionPath() != ""), so callers can run a
// conditional Resume and then invoke this unconditionally. Centralises the
// per-surface copies of this logic (the CLI chat/serve fresh branches and the
// bot's former ensureControllerSessionPath).
func (c *Controller) EnsureSessionPath() {
	c.ensureSessionPath(session.CreateOptions{})
}

// EnsureHeadlessRunSessionPath is EnsureSessionPath for a one-shot run: a
// fresh store records that kind so conversation lists can leave it out.
func (c *Controller) EnsureHeadlessRunSessionPath() {
	c.ensureSessionPath(session.CreateOptions{Kind: session.SessionKindHeadlessRun})
}

func (c *Controller) ensureSessionPath(options session.CreateOptions) {
	if _, ok := c.SessionRef(); ok {
		return
	}
	if service, _, exclusive := c.v3Binding(); exclusive && service != nil {
		if _, err := c.BindFreshSessionWithOptions(context.Background(), options); err != nil {
			c.failTurnEventLedger(err)
		}
		return
	}
	if c.SessionPath() != "" || c.SessionDir() == "" {
		return
	}
	c.SetFreshSessionPath(agent.NewSessionPath(c.SessionDir(), c.Label()))
}

func (c *Controller) AdoptHistory(msgs []provider.Message, path string) error {
	if c.sessionEngineEnabled() {
		if _, bound := c.SessionRef(); bound {
			if len(msgs) > 0 {
				return c.AdoptRebuiltModelContext(msgs)
			} else {
				c.restoreExecutorFromSessionEvents()
			}
			return nil
		}
	}
	if path != "" {
		loaded, err := agent.LoadSession(path)
		if os.IsNotExist(err) {
			c.Resume(agent.NewSession("").CloneWithMessages(msgs), path)
			return nil
		}
		if err != nil {
			c.failTurnEventLedger(err)
			return err
		}
		if len(msgs) > 0 {
			if adopted, ok := loaded.CloneWithMessagesIfCompatible(msgs); ok {
				loaded = adopted
			}
		}
		c.Resume(loaded, path)
		return nil
	}
	if c.executor != nil && len(msgs) > 0 {
		c.executor.SetSession(agent.NewSession("").CloneWithMessages(msgs))
	}
	return nil
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
