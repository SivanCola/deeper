package main

import (
	"fmt"
	"strings"

	"deepzora/internal/session"
)

type sessionLocatorKind uint8

const (
	sessionLocatorEmpty sessionLocatorKind = iota
	sessionLocatorCanonical
	sessionLocatorInvalid
)

type sessionLocator struct {
	kind   sessionLocatorKind
	ref    session.SessionRef
	reason string
}

type sessionLocatorError struct {
	reason string
}

func validateTabSessionIdentity(id string) error {
	if id == "" {
		return nil
	}
	if err := session.ValidateSessionID(id); err != nil {
		return &sessionLocatorError{reason: "invalid_canonical_session_id"}
	}
	return nil
}

func (e *sessionLocatorError) Error() string {
	if e == nil || e.reason == "" {
		return "invalid session identity"
	}
	return fmt.Sprintf("invalid session identity: %s", e.reason)
}

// classifySessionLocator is deliberately path-I/O free. A value with the
// canonical route prefix is always a route candidate: malformed IDs never
// fall through to filesystem interpretation.
func classifySessionLocator(raw string) sessionLocator {
	value := strings.TrimSpace(raw)
	if value == "" {
		return sessionLocator{kind: sessionLocatorEmpty}
	}
	if id, route := strings.CutPrefix(value, remoteSessionIDRoutePrefix); route {
		id = strings.TrimSpace(id)
		if err := session.ValidateSessionID(id); err != nil {
			return sessionLocator{kind: sessionLocatorInvalid, reason: "invalid_canonical_route"}
		}
		return sessionLocator{
			kind: sessionLocatorCanonical,
			ref:  session.SessionRef{HostID: localDesktopHostID, SessionID: id},
		}
	}
	return sessionLocator{kind: sessionLocatorInvalid, reason: "invalid_session_route"}
}
