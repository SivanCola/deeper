package serve

import (
	"context"

	"deepzora/internal/control"
	"deepzora/internal/session"
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
	if target == nil {
		return s.replaceFreshSession(ctx, cur, nil)
	}
	return s.switchIdentityCandidate(ctx, cur, *target)
}

func (s *Server) switchIdentityCandidate(ctx context.Context, cur *control.Controller, ref session.SessionRef) error {
	next, tag, err := s.buildTaggedPreset(ctx, currentModelRef(cur), false, nil, &ref)
	if err != nil {
		return err
	}
	if next.SessionService() == nil {
		s.closeTaggedController(next)
		return errIdentityServiceUnavailable
	}
	if err := validateModelCandidate(ctx, next, s.managedModels); err != nil {
		s.closeTaggedController(next)
		return err
	}
	if _, err := next.OpenSession(ctx, ref); err != nil {
		s.closeTaggedController(next)
		return err
	}
	return s.commitSessionCandidate(ctx, cur, next, tag, false)
}

func identityPresetNeedsAssembly(ctx context.Context, cur *control.Controller, ref session.SessionRef) (bool, error) {
	snapshot, err := cur.SessionService().Query().Snapshot(ctx, ref)
	if err != nil {
		return false, err
	}
	return cur.AgentPreset() != "" || snapshot.Projection.AgentPreset != "", nil
}
