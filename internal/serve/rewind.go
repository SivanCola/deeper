package serve

import (
	"encoding/json"
	"net/http"

	"deepzora/internal/control"
)

// rewind rewinds the session to a checkpoint.
func (s *Server) rewind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Turn  int    `json:"turn"`
		Scope string `json:"scope"` // "code", "conversation", "both"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Turn < 0 {
		http.Error(w, "missing turn", http.StatusBadRequest)
		return
	}
	scope := control.RewindBoth
	switch body.Scope {
	case "both":
	case "code":
		scope = control.RewindCode
	case "conversation":
		scope = control.RewindConversation
	default:
		http.Error(w, "scope must be code, conversation, or both", http.StatusBadRequest)
		return
	}
	if !s.rotateHistory(w, r, func(ctrl *control.Controller) error {
		return commitUnconfirmedRewind(ctrl, body.Turn, scope)
	}) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func commitUnconfirmedRewind(ctrl *control.Controller, turn int, scope control.RewindScope) error {
	plan, err := ctrl.PrepareRewind(turn, scope)
	if err != nil {
		return err
	}
	if err := control.ValidateUnconfirmedRewindPlan(plan); err != nil {
		return err
	}
	_, err = ctrl.CommitRewindInPlace(plan.PlanID)
	return err
}
