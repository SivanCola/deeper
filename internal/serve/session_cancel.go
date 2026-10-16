package serve

import (
	"encoding/json"
	"net/http"
)

func (s *Server) cancelSession(w http.ResponseWriter, _ *http.Request) {
	ctrl := s.ctl()
	receipt := ctrl.CancelSessionFrom("user_stop")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(receipt)
}
