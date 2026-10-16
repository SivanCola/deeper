package control

import (
	"errors"
	"fmt"
)

// ClearSession discards the current conversation without preserving it in
// resume/history, then rotates to a clean session carrying the same base system
// prompt and no pinned context.
func (c *Controller) ClearSession() error {
	if c.executor == nil {
		return nil
	}
	// Same rotation gate as NewSession: hold it across the whole
	// destroy-then-swap so a turn cannot start during the sequence and have its
	// live session replaced.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return fmt.Errorf("cannot clear while a turn is running: %w", err)
		}
		return err
	}
	defer c.endRotation()
	return c.rotateExclusiveSession(true)
}
