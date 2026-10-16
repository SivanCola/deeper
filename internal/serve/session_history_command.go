package serve

import (
	"fmt"
	"net/http"
	"strings"

	"deepzora/internal/control"
)

// Session-changing commands finish under the host binding lock. Dispatching
// them asynchronously through Submit would publish a new controller identity
// without moving the event route or fencing the next HTTP request.
func (s *Server) submitHistoryCommand(w http.ResponseWriter, r *http.Request, input string) bool {
	fields := strings.Fields(input)
	if len(fields) == 0 || (fields[0] != "/branch" && fields[0] != "/switch" && fields[0] != "/rewind") {
		return false
	}
	args := strings.TrimSpace(strings.TrimPrefix(input, fields[0]))
	if !s.rotateHistory(w, r, func(ctrl *control.Controller) error {
		if fields[0] == "/rewind" {
			turn, scope, err := control.ParseRewindTarget(args, ctrl.Checkpoints())
			if err != nil {
				return err
			}
			return commitUnconfirmedRewind(ctrl, turn, scope)
		}
		if fields[0] == "/switch" {
			_, err := ctrl.SwitchBranch(args)
			return err
		}
		turn, name, fromTurn, err := control.ParseBranchTarget(args)
		if err != nil {
			return err
		}
		if fromTurn {
			_, err = ctrl.ForkNamed(turn-1, name)
		} else {
			_, err = ctrl.Branch(name)
		}
		return err
	}) {
		return true
	}
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (s *Server) rotateHistory(w http.ResponseWriter, r *http.Request, rotate func(*control.Controller) error) bool {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) || s.rejectMirroredForegroundLocked(w) {
		return false
	}
	ctrl, ok := s.ctl().(*control.Controller)
	if !ok {
		http.Error(w, "current session runtime is unavailable", http.StatusConflict)
		return false
	}
	before, bound := ctrl.SessionRef()
	if !bound {
		http.Error(w, "current session is not attached", http.StatusConflict)
		return false
	}
	if tag := s.tagFor(ctrl); tag != nil {
		tag.PauseRuntime()
		defer tag.ActivateRuntime()
	}
	err := rotate(ctrl)
	after, bound := ctrl.SessionRef()
	if bound && after != before {
		s.setControllerIdentity(ctrl)
		s.announceSessionChanged("", false)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("session operation failed: %v", err), http.StatusConflict)
		return false
	}
	writeSessionIDHeader(w, ctrl)
	return true
}
