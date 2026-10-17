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

// busySwitchIdentity backgrounds a busy exclusive-session controller and
// brings the target identity session to the foreground. The replacement opens
// the target through the shared session service; the detached registry keeps
// the running turn observable until it is re-attached. A host that cannot build
// an identity-capable replacement retains the historical refusal.
func (s *Server) busySwitchIdentity(ctx context.Context, cur *control.Controller, ref session.SessionRef) error {
	if s.tagFor(cur) == nil {
		return errSessionTagUnavailable
	}
	return s.switchIdentityCandidate(ctx, cur, ref)
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
	if bound, ok := next.SessionRef(); ok {
		tag.PrimeIdentity("", bound.SessionID)
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
