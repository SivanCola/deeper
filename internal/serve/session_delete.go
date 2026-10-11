package serve

import (
	"encoding/json"
	"errors"
	"net/http"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

var deleteSessionBeforeOwnershipLockHookForTest func()

// deleteSession deletes one immutable session identity.
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || session.ValidateSessionID(req.SessionID) != nil {
		http.Error(w, "valid sessionId required", http.StatusBadRequest)
		return
	}
	if deleteSessionBeforeOwnershipLockHookForTest != nil {
		deleteSessionBeforeOwnershipLockHookForTest()
	}
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.deleteCanonicalSession(w, r, req.SessionID) {
		http.Error(w, "session service unavailable", http.StatusConflict)
	}
}

// deleteCanonicalSession deletes one canonical identity and reports whether
// the request was handled. It is called with bindMu held so a concurrent
// /resume or /new cannot change the foreground identity between the active
// check and the filesystem tombstone.
func (s *Server) deleteCanonicalSession(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	identity, ok := s.ctl().(control.IdentityLifecycle)
	if !ok || !identity.UsesExclusiveSession() {
		return false
	}
	service := identity.SessionService()
	if service == nil {
		http.Error(w, "canonical sessions disabled", http.StatusBadRequest)
		return true
	}
	ref := session.SessionRef{HostID: service.HostID(), SessionID: sessionID}
	if current, bound := identity.SessionRef(); bound && current == ref {
		http.Error(w, "cannot delete active session", http.StatusConflict)
		return true
	}
	if err := service.Delete(r.Context(), ref); err != nil {
		switch {
		case errors.Is(err, session.ErrSessionNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		case errors.Is(err, session.ErrRuntimeBusy), errors.Is(err, session.ErrRuntimeBound):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}
