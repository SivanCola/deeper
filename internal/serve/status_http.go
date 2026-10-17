package serve

import (
	"log/slog"
	"net/http"
	"strings"

	"deepzora/internal/agentpreset"
	"deepzora/internal/config"
	"deepzora/internal/control"
)

// status returns a combined status snapshot. The desktop's runtime-only path
// skips provider balance IO while retaining all reconciliation fields.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	identityRoute, answered := s.statusIdentityRoute(w, r)
	if answered {
		return
	}
	s.writeForegroundStatus(w, r, identityRoute)
}

// statusIdentityRoute answers a spectator watching a final-format identity a
// local runtime owns. A foreground-bound identity falls through, but the
// selector is remembered so the snapshot can state ownership explicitly: a
// spectator pin must clear the moment this serve owns the identity again.
func (s *Server) statusIdentityRoute(w http.ResponseWriter, r *http.Request) (route string, answered bool) {
	raw := r.URL.Query().Get("session")
	if strings.TrimSpace(raw) == "" {
		return "", false
	}
	if !isSessionIDRoute(raw) {
		http.Error(w, "immutable session identity required", http.StatusBadRequest)
		return "", true
	}
	if s.statusIdentityOverride(w, raw) {
		return "", true
	}
	return strings.TrimSpace(raw), false
}

func (s *Server) writeForegroundStatus(w http.ResponseWriter, r *http.Request, identityRoute string) {
	// Session rotations publish the controller path and executor Session while
	// holding bindMu. Read the combined snapshot in that same binding epoch so
	// callers can never pair a newly published path with the outgoing history.
	s.bindMu.Lock()
	ctrl := s.ctl()
	if identityRoute != "" {
		ctrl = s.resolveReadControllerLocked(identityRoute)
		if ctrl == nil {
			s.bindMu.Unlock()
			http.Error(w, "session binding changed", http.StatusConflict)
			return
		}
	}
	sess := sessionStatusFields(ctrl)
	sessionRoute := sessionRouteForController(ctrl)
	if identity, ok := ctrl.(control.IdentityLifecycle); ok {
		if ref, bound := identity.SessionRef(); bound {
			sess["hostId"] = ref.HostID
			sess["sessionId"] = ref.SessionID
		}
	}
	if identityRoute != "" {
		// The poller selected an identity explicitly: answer ownership both
		// ways. Without an explicit false after a reclaim, clients that only
		// apply present fields keep a stale spectator pin forever.
		sess["takenOver"] = s.sessionMirrored(identityRoute)
	}
	addEffortStatus(sess, ctrl)
	if sessionRoute != "" && s.sessionMirrored(sessionRoute) {
		s.applyMirroredStatus(sess, sessionRoute)
		s.bindMu.Unlock()
		s.maybeAutoReclaimMirrored(sessionRoute)
		writeJSON(w, sess)
		return
	}
	if u := ctrl.LastUsage(); u != nil {
		sess["lastUsage"] = u
	}
	sess["sessionCostQuote"] = s.bc.SessionCostQuoteFor(sessionRoute)
	if j := ctrl.Jobs(); len(j) > 0 {
		sess["jobs"] = j
	}
	// Balance can perform provider IO and does not participate in session
	// identity. Release the binding epoch before that optional slow request.
	s.bindMu.Unlock()
	if r.URL.Query().Get("runtime") != "1" && r.URL.Query().Get("lite") != "1" {
		s.addBalanceStatus(r, sess, ctrl)
	}
	writeJSON(w, sess)
}

func sessionStatusFields(ctrl control.SessionAPI) map[string]any {
	used, window := ctrl.ContextSnapshot()
	hit, miss := ctrl.SessionCache()
	state := runtimeStateOf(ctrl)
	sess := map[string]any{
		"runtimeState":     state,
		"label":            ctrl.Label(),
		"running":          state.Running,
		"plan":             ctrl.PlanMode(),
		"autoApproveTools": ctrl.AutoApproveTools(),
		"bypass":           ctrl.AutoApproveTools(),
		"toolApprovalMode": ctrl.ToolApprovalMode(),
		"goal":             ctrl.Goal(),
		"goalStatus":       ctrl.GoalStatus(),
		"cwd":              ctrl.SessionDir(),
		"used":             used,
		"window":           window,
		"cacheHit":         hit,
		"cacheMiss":        miss,
		// Runtime reconciliation fields for desktop running-state watchdogs:
		// the remote tab surface polls /status and maps these onto the same
		// reconciliation the local tabs get from ListTabs.
		"pendingPrompt":   state.PendingPrompt,
		"backgroundJobs":  state.BackgroundJobs,
		"cancelRequested": state.CancelRequested,
		"cancellable":     state.Cancellable,
	}
	if presets, ok := ctrl.(interface {
		AgentPreset() string
		AgentPresetLocked() bool
	}); ok {
		sess["agentPreset"] = agentpreset.NormalizeID(presets.AgentPreset())
		sess["agentPresetLocked"] = presets.AgentPresetLocked()
	}
	sess["goalView"] = state.Goal
	if ctrl.Goal() != "" {
		sess["goalRuntime"] = ctrl.GoalRuntime()
	}
	return sess
}

func addEffortStatus(sess map[string]any, ctrl control.SessionAPI) {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	entry, ok := cfg.ResolveModel(currentModelRef(ctrl))
	if !ok {
		return
	}
	capability := config.EffortCapabilityForEntry(entry)
	levels := capability.Levels
	if levels == nil {
		levels = []string{}
	}
	sess["effort"] = map[string]any{
		"supported": capability.Supported,
		"current":   config.EffortDisplay(entry),
		"default":   capability.Default,
		"levels":    levels,
	}
}

// applyMirroredStatus states that a local runtime owns the session. These
// fields are the authoritative ownership signal: a slow subscriber can drop
// notices, the status poll cannot.
func (s *Server) applyMirroredStatus(sess map[string]any, canonical string) {
	sess["running"] = false
	sess["pendingPrompt"] = false
	sess["takenOver"] = true
	if m, ok := s.mirroredEntry(canonical); ok {
		sess["reclaimRequested"] = m.reclaimRequested
	}
}

func (s *Server) addBalanceStatus(r *http.Request, sess map[string]any, ctrl control.SessionAPI) {
	b, err := ctrl.Balance(r.Context())
	if err != nil {
		slog.Warn("serve: balance fetch failed", "err", err)
		return
	}
	if b == nil {
		return
	}
	if cfg, loadErr := config.Load(); loadErr == nil && cfg.DisplayCurrencyPref() == "" {
		// Runtime-only hint: a single wallet currency may select an existing
		// valuation, but is never persisted as configuration or history.
		s.bc.SetDisplayCurrency(b.PrimaryCurrency())
	}
	sess["balance"] = map[string]any{
		"display":   b.Display(),
		"available": b.Available,
		"infos":     b.Infos,
	}
}
