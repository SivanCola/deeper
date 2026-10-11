package serve

import (
	"net/http"

	"deepzora/internal/control"
)

func (s *Server) registerRuntimeRecoveryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /tool-recovery", s.toolRecovery)
}

type toolRecoveryController interface {
	ToolRecoverySnapshot() control.ToolRecoverySnapshot
}

func (s *Server) toolRecovery(w http.ResponseWriter, r *http.Request) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) {
		return
	}
	ctrl, ok := s.ctl().(toolRecoveryController)
	if !ok {
		http.Error(w, "tool recovery unavailable", http.StatusNotImplemented)
		return
	}
	writeJSON(w, ctrl.ToolRecoverySnapshot())
}
