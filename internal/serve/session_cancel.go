package serve

import (
	"deeper/internal/agent"
	"deeper/internal/control"
	"encoding/json"
	"net/http"
)

func (s *Server) cancelSession(w http.ResponseWriter, _ *http.Request) {
	ctrl := s.ctl()
	receipt := control.CancelReceipt{SessionRef: ctrl.SessionPath(), HeadID: agent.BranchID(ctrl.SessionPath()), Accepted: true}
	if concrete, ok := ctrl.(*control.Controller); ok {
		receipt = concrete.CancelSessionFrom("user_stop")
	} else if cancellable, ok := ctrl.(interface{ CancelSession() control.CancelReceipt }); ok {
		receipt = cancellable.CancelSession()
	} else {
		status := ctrl.RuntimeStatus()
		receipt.AlreadyIdle = !status.Running && !status.PendingPrompt
		ctrl.Cancel()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(receipt)
}
