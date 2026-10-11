package acp

import (
	"deepzora/internal/control"
	"fmt"
)

func snapshotACPController(_ *acpSession, ctrl acpController) error { return ctrl.Snapshot() }

func (s *service) prepareACPReplacementAuthority(_ *acpSession, next *control.Controller, _ acpController, _ string, action string) error {
	if err := next.Snapshot(); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}
