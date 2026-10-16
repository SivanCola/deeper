package control

import (
	"context"

	"deepzora/internal/event"
	"deepzora/internal/session"
)

func (c *Controller) submitCommandOrTurn(trimmed, input, display string, scopedRefsOnly bool, editedOriginal, format string, admission turnAdmission) {
	if err := c.ensureWriteAuthorityReady(); err != nil {
		c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "input was not accepted: this session is no longer writable — reopen it and try again"})
		return
	}
	c.submitCommandOrTurnReady(trimmed, input, display, scopedRefsOnly, editedOriginal, format, admission)
}

// Run verifies the live write generation before synchronous headless turns.
func (c *Controller) Run(ctx context.Context, input string) error {
	prepared, failures := c.prepareSubmissionImagesContext(ctx, SubmissionRequest{Input: input})
	if len(failures) > 0 {
		return ImageReferenceFailures(failures)
	}
	ctx = contextWithPreparedImageReferences(ctx, prepared)
	err := c.runSynchronousTurn(ctx, nil, func(runCtx context.Context) error {
		return c.runReady(runCtx, input)
	})
	if err != nil {
		return err
	}
	return c.waitForGoalTerminal(ctx)
}

// ensureWriteAuthorityReady validates the host runtime before turn admission.
// Ephemeral controllers have no persistence authority to acquire.
func (c *Controller) ensureWriteAuthorityReady() error {
	if c == nil || c.executor == nil {
		return nil
	}
	if service, runtime, exclusive := c.v3Binding(); exclusive {
		if runtime == nil {
			if service == nil {
				return session.ErrSessionNotRunning
			}
			if _, err := c.BindFreshSession(context.Background(), ""); err != nil {
				return err
			}
			_, runtime, _ = c.v3Binding()
			if runtime == nil {
				return session.ErrSessionNotRunning
			}
		}
		phase := runtime.StateSnapshot().Phase
		if phase == session.RuntimeRecoveryRequired {
			return session.ErrRecoveryRequired
		}
		if phase == session.RuntimeClosed {
			return session.ErrSessionNotRunning
		}
		return nil
	}
	return nil
}
