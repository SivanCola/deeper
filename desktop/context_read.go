package main

import (
	"strings"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

// contextRead binds every field in an overview response to one runtime. Reads
// must never re-key the tab's telemetry: an old read can finish after navigation.
type contextRead struct {
	tabID           string
	tab             *WorkspaceTab
	ctrl            control.SessionAPI
	generation      uint64
	sessionID       string
	controllerRef   session.SessionRef
	controllerBound bool
	workspaceRoot   string
	telemetry       tabTelemetrySnapshot
	runtime         sessionRuntime
}

// sessionRuntime splits runtime into finished turns and the running turn's
// start, so a reader can keep counting between snapshots.
type sessionRuntime struct {
	completedMs   int64
	turnStartedAt int64
}

func (a *App) captureContextRead(tabID string) contextRead {
	a.mu.RLock()
	if strings.TrimSpace(tabID) == "" {
		tabID = a.activeTabID
	}
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return contextRead{}
	}
	r := contextRead{tabID: tabID, tab: tab, ctrl: tab.Ctrl, generation: tab.SessionGeneration,
		sessionID: tab.SessionID, workspaceRoot: tab.WorkspaceRoot}
	tab.telemMu.Lock()
	key := tab.telemetrySessionKey
	r.telemetry = tab.displayTelemetrySnapshotLocked()
	r.runtime = sessionRuntime{completedMs: tab.usageTelemetry.ElapsedMs, turnStartedAt: tab.usageTelemetry.activeTurnStartedAt}
	tab.telemMu.Unlock()
	a.mu.RUnlock()
	r.controllerRef, r.controllerBound = contextControllerIdentity(r.ctrl)
	identity := sessionRoute(r.sessionID)
	if r.controllerBound {
		identity = sessionRoute(r.controllerRef.SessionID)
	}
	if identity != "" && sessionRuntimeKey(identity) != key {
		// A current identity can rotate before the event writer re-keys the
		// display counters. Read that owner's telemetry without changing the tab.
		r.telemetry = loadTelemetryFor(identity)
		r.runtime = sessionRuntime{completedMs: r.telemetry.Usage.ElapsedMs}
	}
	return r
}

func (r contextRead) current(a *App) bool {
	if r.tab == nil {
		return false
	}
	// Controller methods stay outside App.mu; some can re-enter the host.
	ref, bound := contextControllerIdentity(r.ctrl)
	if ref != r.controllerRef || bound != r.controllerBound {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tabs[r.tabID] == r.tab && !r.tab.removed && r.tab.Ctrl == r.ctrl &&
		r.tab.SessionGeneration == r.generation && r.tab.SessionID == r.sessionID
}

func contextControllerIdentity(ctrl control.SessionAPI) (session.SessionRef, bool) {
	if owner, ok := ctrl.(interface {
		SessionRef() (session.SessionRef, bool)
	}); ok {
		return owner.SessionRef()
	}
	return session.SessionRef{}, false
}
