package control

import (
	"context"

	"deepzora/internal/extension"
	"deepzora/internal/extension/dispatch"
	"deepzora/internal/session"
)

// Snapshot flushes the current session's event sequence. Content and runtime
// state have already been committed by their owners; no transcript is rewritten.
func (c *Controller) Snapshot() error {
	_, err := c.snapshotWithDurability()
	return err
}

func (c *Controller) snapshotWithDurability() (bool, error) {
	c.snapshotMu.Lock()
	defer c.snapshotMu.Unlock()
	if c.executor == nil || !c.executor.Session().HasContent() {
		return false, nil
	}
	_, runtime, _ := c.v3Binding()
	if runtime == nil || c.sessionEventStore() == nil {
		return false, session.ErrSessionNotRunning
	}
	payload, err := c.extensionSessionStrategy(context.Background(), extension.PointSessionSave, dispatch.PhaseSave, runtime.Ref().SessionID)
	if err != nil {
		return false, err
	}
	if _, err := c.flushSessionEvents(context.Background()); err != nil {
		return false, err
	}
	c.extensionSessionPayloadEvent(extension.PointSessionSave, payload)
	return true, nil
}
