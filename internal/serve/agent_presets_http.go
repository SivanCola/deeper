package serve

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"deepzora/internal/agentpreset"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func (s *Server) registerAgentPresetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /agent-presets", s.agentPresets)
	mux.HandleFunc("PUT /agent-presets/{id}", s.saveAgentPreset)
	mux.HandleFunc("DELETE /agent-presets/{id}", s.deleteAgentPreset)
	mux.HandleFunc("POST /agent-preset", s.selectAgentPreset)
}

func (s *Server) agentPresets(w http.ResponseWriter, r *http.Request) {
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) {
		return
	}
	api, ok := s.ctl().(control.AgentPresets)
	if !ok {
		http.Error(w, "agent presets are unavailable", http.StatusNotImplemented)
		return
	}
	list, err := api.ListAgentPresets()
	if err != nil {
		renderAgentPresetError(w, err)
		return
	}
	writeJSON(w, list)
}

func (s *Server) saveAgentPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope   string  `json:"scope"`
		Content *string `json:"content"`
	}
	if !decodeAgentPresetRequest(w, r, &body, false) {
		return
	}
	if body.Content == nil {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	api := s.agentPresetAuthor(w, r)
	if api == nil {
		return
	}
	preset, err := api.SaveAgentPreset(body.Scope, r.PathValue("id"), *body.Content)
	if err != nil {
		renderAgentPresetError(w, err)
		return
	}
	writeJSON(w, preset)
}

func (s *Server) deleteAgentPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope string `json:"scope"`
	}
	if !decodeAgentPresetRequest(w, r, &body, false) {
		return
	}
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	api := s.agentPresetAuthor(w, r)
	if api == nil {
		return
	}
	if err := api.DeleteAgentPreset(body.Scope, r.PathValue("id")); err != nil {
		renderAgentPresetError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentPresetAuthor(w http.ResponseWriter, r *http.Request) control.AgentPresets {
	if !s.validateSwitchExpectedLocked(w, r) {
		return nil
	}
	api, ok := s.ctl().(control.AgentPresets)
	if !ok {
		http.Error(w, "agent presets are unavailable", http.StatusNotImplemented)
		return nil
	}
	return api
}

func (s *Server) selectAgentPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentPreset *string `json:"agentPreset"`
	}
	if !decodeAgentPresetRequest(w, r, &body, false) {
		return
	}
	if body.AgentPreset == nil {
		http.Error(w, "agentPreset is required", http.StatusBadRequest)
		return
	}
	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) || s.rejectMirroredForegroundLocked(w) {
		return
	}
	if err := s.switchAgentPresetLocked(r.Context(), *body.AgentPreset); err != nil {
		renderAgentPresetError(w, err)
		return
	}
	writeSessionIDHeader(w, s.ctl())
	w.WriteHeader(http.StatusNoContent)
}

// Decode before bindMu so a slow upload cannot block unrelated session commands.
func decodeAgentPresetRequest(w http.ResponseWriter, r *http.Request, target any, optional bool) bool {
	if !bufferForegroundMutationBody(w, r) {
		return false
	}
	if r.Body == nil || r.Body == http.NoBody {
		if optional {
			return true
		}
		http.Error(w, "invalid agent preset request", http.StatusBadRequest)
		return false
	}
	if optional && r.ContentLength == 0 {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid agent preset request", http.StatusBadRequest)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "invalid agent preset request", http.StatusBadRequest)
		return false
	}
	return true
}

func renderAgentPresetError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var pathError *os.PathError
	switch {
	case errors.Is(err, control.ErrAgentPresetLocked), errors.Is(err, control.ErrModelChoiceStale), errors.Is(err, session.ErrWriterOwned), errors.Is(err, errReplacedDuringBind), errors.Is(err, errAgentPresetBusy), errors.Is(err, errSessionTagUnavailable):
		status = http.StatusConflict
	case errors.Is(err, agentpreset.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, agentpreset.ErrBuiltin):
		status = http.StatusForbidden
	case errors.As(err, &pathError):
		status = http.StatusInternalServerError
	}
	http.Error(w, err.Error(), status)
}

func validateAgentPresetSelection(raw string) (string, error) {
	id := agentpreset.NormalizeID(strings.TrimSpace(raw))
	return id, agentpreset.ValidID(id)
}
