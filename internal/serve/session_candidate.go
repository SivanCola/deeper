package serve

import (
	"context"

	"deepzora/internal/control"
	"deepzora/internal/session"
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
	if _, err := next.EnsureSession(ctx, session.CreateOptions{}); err != nil {
		s.closeTaggedController(next)
		return err
	}
	return s.commitSessionCandidate(ctx, cur, next, tag, true)
}

// commitSessionCandidate publishes a prepared identity before releasing its boot
// frames. bindMu keeps foreground publication and background registration atomic.
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
	if ref, bound := next.SessionRef(); bound {
		tag.PrimeIdentity(ref.SessionID)
	}
	tag.PauseRuntime()
	next.EnableInteractiveApproval()
	if !s.publishControllerSwap(cur, next) {
		s.closeTaggedController(next)
		return errReplacedDuringBind
	}
	if busy {
		if _, err := s.registerDetached(cur, nil); err != nil {
			_ = s.publishControllerSwap(next, cur)
			s.closeTaggedController(next)
			return err
		}
	} else {
		cur.Close()
		s.forgetSessionTag(cur)
	}
	tag.Activate()
	if reset {
		s.bc.ResetSessionPath(sessionRouteForController(next))
	}
	return nil
}
