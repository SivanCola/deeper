package cli

import (
	"context"
	"deepzora/internal/control"
	"strings"
)

// prepareServeSession binds an existing identity or reserves the requested new identity.
func prepareServeSession(ctrl *control.Controller, target cliResumeTarget, sessionID string) error {
	if !target.empty() {
		_, err := ctrl.OpenSessionFromService(context.Background(), resumeTargetService(target), target.ref)
		return err
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	_, err := ctrl.BindFreshSession(context.Background(), strings.TrimSpace(sessionID))
	return err
}
