package acp

import (
	"context"
	"deepzora/internal/control"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"fmt"
)

func modelOnlyConfigDeltas(deltas []sessionConfigDelta) bool {
	if len(deltas) == 0 {
		return false
	}
	for _, d := range deltas {
		if d.axis != "model" {
			return false
		}
	}
	return true
}

func configBackgroundBlocked(ctrl acpController, modelOnly bool) bool {
	return !modelOnly || control.ModelReplacementBlocked(ctrl)
}

func (s *service) buildConfigReplacement(ctx context.Context, old acpController, params SessionParams, modelOnly bool) (*control.Controller, error) {
	var sourceService *session.Service
	var sourceRuntime *session.Runtime
	if bound, ok := old.(interface {
		SessionBinding() (*session.Service, *session.Runtime, bool)
	}); ok {
		sourceService, sourceRuntime, _ = bound.SessionBinding()
	}
	if sourceRuntime == nil {
		for _, message := range old.History() {
			if message.Role != provider.RoleSystem {
				return nil, fmt.Errorf("rebuilding a conversation requires a current session runtime")
			}
		}
	}
	params.SessionRuntime = sourceRuntime
	finish, abort := func(*control.Controller) error { return nil }, func() {}
	if modelOnly {
		if c, ok := old.(*control.Controller); ok {
			params.SessionTemp, params.PersistentShell = c.SessionTemp(), c.PersistentShell()
		}
		var err error
		params.BackgroundScope, finish, abort, err = control.ReserveBackgroundReplacement(old)
		if err != nil {
			return nil, err
		}
	}
	defer abort()
	next, err := s.factory.NewSession(ctx, params)
	if err != nil {
		return nil, err
	}
	nextService, nextRuntime, _ := next.SessionBinding()
	if nextRuntime != sourceRuntime || (sourceRuntime != nil && nextService != sourceService) {
		next.ReleaseResources()
		return nil, fmt.Errorf("replacement changed the current session runtime")
	}
	if err := finish(next); err != nil {
		next.ReleaseResources()
		return nil, err
	}
	return next, nil
}
