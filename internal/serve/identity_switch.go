package serve

import (
	"context"
	"deepzora/internal/control"
	"deepzora/internal/session"
	"log/slog"
)

// sessionAPIRef reads the immutable v3 identity off any session API without
// requiring the concrete controller type.
func sessionAPIRef(ctrl control.SessionAPI) (session.SessionRef, bool) {
	identity, ok := ctrl.(interface {
		SessionRef() (session.SessionRef, bool)
	})
	if !ok {
		return session.SessionRef{}, false
	}
	return identity.SessionRef()
}

func sessionRouteForController(ctrl control.SessionAPI) string {
	if ref, bound := sessionAPIRef(ctrl); bound {
		return remoteSessionIDQueryPrefix + ref.SessionID
	}
	return ""
}

// replaceBusySession backgrounds the current controller and publishes either
// an existing identity or a fresh current session. Callers hold bindMu; failed
// construction or registry publication leaves the outgoing controller usable.
func (s *Server) replaceBusySession(ctx context.Context, cur *control.Controller, target *session.SessionRef) error {
	if s.tagFor(cur) == nil {
		return errSessionTagUnavailable
	}
	next, tag, err := s.buildTagged(ctx, currentModelRef(cur), false)
	if err != nil {
		return err
	}
	if next.SessionService() == nil {
		s.closeTaggedController(next)
		return errIdentityServiceUnavailable
	}
	var ref session.SessionRef
	if target == nil {
		ref, err = next.EnsureSession(ctx, session.CreateOptions{})
	} else {
		ref, err = next.OpenSession(ctx, *target)
	}
	if err != nil {
		s.closeTaggedController(next)
		return err
	}
	if bound, ok := next.SessionRef(); ok {
		tag.PrimeIdentity(bound.SessionID)
	}
	tag.PauseRuntime()
	next.EnableInteractiveApproval()
	if !s.publishControllerSwap(cur, next) {
		s.closeTaggedController(next)
		return errReplacedDuringBind
	}
	if _, err := s.registerDetached(cur, nil); err != nil {
		// bindMu prevents another foreground swap here. Roll publication back so
		// a registry failure cannot strand a running controller.
		_ = s.publishControllerSwap(next, cur)
		s.closeTaggedController(next)
		return err
	}
	tag.Activate()
	slog.Info("serve: busy identity session detached", "session", ref.SessionID)
	return nil
}
