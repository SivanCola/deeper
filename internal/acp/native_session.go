package acp

import (
	"context"
	"errors"
	"os"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

func canonicalSessionExists(ctx context.Context, ctrl *control.Controller, id string) (bool, error) {
	service := ctrl.SessionService()
	if service == nil {
		return false, nil
	}
	_, err := service.Query().Stat(ctx, session.SessionRef{HostID: service.HostID(), SessionID: id})
	if errors.Is(err, session.ErrSessionNotFound) || os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func openCanonicalSession(ctx context.Context, ctrl *control.Controller, id, method string) error {
	service := ctrl.SessionService()
	if service == nil {
		return &RPCError{Code: ErrInternal, Message: method + ": session service is unavailable"}
	}
	if _, err := ctrl.OpenSession(ctx, session.SessionRef{HostID: service.HostID(), SessionID: id}); err != nil {
		return &RPCError{Code: ErrInvalidParams, Message: method + ": unknown session " + id}
	}
	return nil
}

func (s *service) bindSessionClients(id string, params *SessionParams) {
	s.bindClientIO(params, id)
}
