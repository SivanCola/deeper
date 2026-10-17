package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"deepzora/internal/control"
)

var errAgentPresetBusy = errors.New("cannot change agent preset while active work or background jobs are running")

func (s *Server) switchAgentPresetLocked(ctx context.Context, raw string) error {
	id, err := validateAgentPresetSelection(raw)
	if err != nil {
		return err
	}
	cur, ok := s.ctl().(*control.Controller)
	if !ok {
		return fmt.Errorf("agent preset selection is unavailable")
	}
	if cur.AgentPresetLocked() {
		return control.ErrAgentPresetLocked
	}
	if controllerHasActiveRuntimeWork(cur) || control.ModelReplacementBlocked(cur) {
		return errAgentPresetBusy
	}
	next, err := s.rebuildPreset(ctx, cur, currentModelRef(cur), &id)
	if err != nil {
		return err
	}
	next.EnableInteractiveApproval()
	if err := validateModelCandidate(ctx, next, s.managedModels); err != nil {
		s.closeTaggedController(next)
		return err
	}
	if !s.publishControllerSwap(cur, next) {
		s.closeTaggedController(next)
		return errReplacedDuringBind
	}
	next.ActivateGoalDriverAfterRebuild()
	if err := next.Snapshot(); err != nil {
		slog.Warn("serve: flush committed agent preset", "err", err)
	}
	if tag := s.tagFor(next); tag != nil {
		if ref, bound := next.SessionRef(); bound {
			tag.PrimeIdentity(ref.SessionID)
		}
		tag.Activate()
		tag.ActivateRuntime()
	}
	cur.ReleaseResources()
	s.forgetSessionTag(cur)
	return nil
}
