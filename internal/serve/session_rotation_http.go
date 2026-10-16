package serve

import (
	"encoding/json"
	"net/http"
	"strings"

	"deepzora/internal/control"
	"deepzora/internal/event"
)

func (s *Server) planDecision(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string                     `json:"id"`
		Action   control.PlanDecisionAction `json:"action"`
		Feedback string                     `json:"feedback"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := s.ctl().ResolvePlanDecisionWithFeedback(body.ID, body.Action, body.Feedback); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	s.ctl().SetPlanMode(body.On)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCommand(w, r, false)
}

func (s *Server) clearSessionFromSubmit(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCommand(w, r, true)
}

func (s *Server) clearSessionCommand(w http.ResponseWriter, r *http.Request, emitNotice bool) {
	// Clear rotates the session path just like /new, but also removes the old
	// transcript artifacts. Keep controller mutation and lease rebinding under
	// one binding lock so remote clients never observe split ownership.
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateSwitchExpectedLocked(w, r) {
		return
	}
	// A mirrored foreground cannot rotate in place (its write authority is
	// gone and its artifacts belong to the local writer). Publish a fresh
	// replacement instead, and keep the mirrored transcript on disk.
	if s.foregroundMirroredLocked() {
		s.mirroredForegroundReplacement(w, r, emitNotice, "cleared (mirrored session kept)")
		return
	}
	if ctrl, ok := s.ctl().(*control.Controller); ok {
		if tag := s.tagFor(ctrl); tag != nil {
			tag.PauseRuntime()
			defer tag.ActivateRuntime()
		}
	}
	if err := s.ctl().ClearSession(); err != nil {
		if control.IsSessionRotationBusy(err) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ctrl, ok := s.ctl().(*control.Controller); ok {
		s.setControllerIdentity(ctrl)
	}
	s.bc.ResetSessionPath(sessionRouteForController(s.ctl()))
	path := sessionRouteForController(s.ctl())
	w.Header().Set(sessionPathHeader, path)
	writeSessionIDHeader(w, s.ctl())
	s.announceSessionChanged(path, true)
	if emitNotice {
		s.emitSessionRotationNotice("context cleared")
	}
	w.WriteHeader(http.StatusNoContent)
}

// mirroredForegroundReplacement swaps the open-but-released foreground
// controller for a fresh session. Used by /new and /clear when the current
// session was handed to a local writer: the outgoing controller cannot
// snapshot or rotate (its write authority is gone), so publish a replacement
// the same way a busy switch does and retire the old controller in the
// background. The mirrored transcript stays untouched on disk. Callers hold
// bindMu and pass the notice text for the newly created session.
func (s *Server) mirroredForegroundReplacement(w http.ResponseWriter, r *http.Request, emitNotice bool, noticeText string) {
	curCtrl, ok := s.ctl().(*control.Controller)
	if !ok {
		http.Error(w, "cannot rotate a mirrored session for this controller implementation", http.StatusConflict)
		return
	}
	if err := s.replaceBusySession(r.Context(), curCtrl, nil); err != nil {
		s.renderBindError(w, err)
		return
	}
	path := sessionRouteForController(s.ctl())
	w.Header().Set(sessionPathHeader, path)
	writeSessionIDHeader(w, s.ctl())
	s.announceSessionChanged(path, true)
	if emitNotice {
		s.emitSessionRotationNotice(noticeText)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) newSession(w http.ResponseWriter, r *http.Request) {
	s.newSessionCommand(w, r, false)
}

func (s *Server) newSessionFromSubmit(w http.ResponseWriter, r *http.Request) {
	s.newSessionCommand(w, r, true)
}

func (s *Server) newSessionCommand(w http.ResponseWriter, r *http.Request, emitNotice bool) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateSwitchExpectedLocked(w, r) {
		return
	}
	cur := s.ctl()
	if controllerHasActiveRuntimeWork(cur) {
		curCtrl, ok := cur.(*control.Controller)
		if !ok {
			http.Error(w, "cannot start a new session while active work or background jobs are running", http.StatusConflict)
			return
		}
		if err := s.replaceBusySession(r.Context(), curCtrl, nil); err != nil {
			s.renderBindError(w, err)
			return
		}
		path := sessionRouteForController(s.ctl())
		w.Header().Set(sessionPathHeader, path)
		writeSessionIDHeader(w, s.ctl())
		s.announceSessionChanged(path, true)
		if emitNotice {
			s.emitSessionRotationNotice("new session")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// A mirrored foreground cannot NewSession() in place — the rotation
	// snapshots the old session first, which fails closed without write
	// authority. Publish a fresh replacement controller instead.
	if s.foregroundMirroredLocked() {
		s.mirroredForegroundReplacement(w, r, emitNotice, "new session")
		return
	}
	if ctrl, ok := cur.(*control.Controller); ok {
		if tag := s.tagFor(ctrl); tag != nil {
			tag.PauseRuntime()
			defer tag.ActivateRuntime()
		}
	}
	if err := cur.NewSession(); err != nil {
		if control.IsSessionRotationBusy(err) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ctrl, ok := cur.(*control.Controller); ok {
		s.setControllerIdentity(ctrl)
	}
	s.bc.ResetSessionPath(sessionRouteForController(cur))
	path := sessionRouteForController(cur)
	w.Header().Set(sessionPathHeader, path)
	writeSessionIDHeader(w, cur)
	s.announceSessionChanged(path, true)
	if emitNotice {
		s.emitSessionRotationNotice("new session")
	}
	w.WriteHeader(http.StatusNoContent)
}

// The binding lock held by rotation callers keeps the completion notice on
// the identity just published, including sessions with no transcript path.
func (s *Server) emitSessionRotationNotice(text string) {
	ctrl := s.ctl()
	frame := event.Event{Kind: event.Notice, Text: text, SessionPath: sessionRouteForController(ctrl)}
	if ref, bound := sessionAPIRef(ctrl); bound {
		frame.SessionID = ref.SessionID
	}
	s.bc.Emit(frame)
}
