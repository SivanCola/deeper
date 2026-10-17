package serve

import (
	"context"
	"fmt"

	"deepzora/internal/control"
)

func (s *Server) replaceFreshSession(ctx context.Context, cur *control.Controller, preset *string) error {
	if preset != nil {
		id, err := validateAgentPresetSelection(*preset)
		if err != nil {
			return err
		}
		preset = &id
	}
	if controllerHasActiveRuntimeWork(cur) && s.tagFor(cur) == nil {
		return errSessionTagUnavailable
	}
	next, tag, err := s.buildTaggedPreset(ctx, currentModelRef(cur), false, preset, nil)
	if err != nil {
		return err
	}
	if next.UsesExclusiveSession() {
		if _, err := next.BindFreshSession(ctx, ""); err != nil {
			s.closeTaggedController(next)
			return err
		}
	} else {
		next.EnsureSessionPath()
		if next.SessionPath() == "" {
			s.closeTaggedController(next)
			return fmt.Errorf("replacement session has no path")
		}
	}
	return s.commitSessionCandidate(ctx, cur, next, tag, true)
}

// commitSessionCandidate publishes a prepared identity without exposing its boot
// frames until the foreground and its lease have moved together. bindMu is held.
func (s *Server) commitSessionCandidate(ctx context.Context, cur, next *control.Controller, tag *sessionTagSink, reset bool) error {
	if err := validateModelCandidate(ctx, next, s.managedModels); err != nil {
		s.closeTaggedController(next)
		return err
	}
	busy := controllerHasActiveRuntimeWork(cur)
	if busy && s.tagFor(cur) == nil {
		s.closeTaggedController(next)
		return errSessionTagUnavailable
	}
	path := next.SessionPath()
	demoted, err := s.leases.RebindDetaching(path)
	if err != nil {
		s.closeTaggedController(next)
		return err
	}
	if ref, bound := next.SessionRef(); bound {
		tag.PrimeIdentity(path, ref.SessionID)
	} else {
		tag.PrimePath(path)
	}
	next.EnableInteractiveApproval()
	next.SetOnSessionRecovered(s.sessionRecoveryHandler(next, s.leases))
	if err := s.leases.BindControllerAuthority(next); err != nil {
		s.closeTaggedController(next)
		s.rollbackDetach(demoted, cur)
		return err
	}
	if !s.publishControllerSwap(cur, next, path) {
		s.closeTaggedController(next)
		s.rollbackDetach(demoted, cur)
		return errReplacedDuringBind
	}
	if busy {
		if _, err := s.registerDetached(cur, demoted, nil); err != nil {
			_ = s.publishControllerSwap(next, cur, cur.SessionPath())
			s.closeTaggedController(next)
			s.rollbackDetach(demoted, cur)
			return err
		}
	} else {
		cur.Close()
		s.forgetSessionTag(cur)
		if demoted != nil {
			demoted.Release()
		}
	}
	tag.Activate()
	if reset {
		s.bc.ResetSessionPath(path)
	}
	return nil
}
