package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type remoteTabSessionRouting struct {
	currentPath string
	// rehydratingPath is the provisional route epoch (Serve has not confirmed
	// currentPath yet). It opens when an identity is committed ahead of its
	// /resume and closes on that resume's outcome or the generation's retirement.
	rehydratingPath   string
	rehydratingFrames []json.RawMessage
	running           map[string]bool
	revision          uint64
	// pathRevision changes only when foreground identity changes. Unlike
	// revision, background running/listing refreshes do not advance it.
	pathRevision uint64
}

func lockRemoteTabRoute(tab *remoteTab) func() {
	if tab == nil {
		return func() {}
	}
	tab.routeEventMu.Lock()
	return tab.routeEventMu.Unlock
}

// preflightRemoteSessionTarget resolves the foreground identity without
// mutating Serve. Attach uses it to publish routing before the event pump can
// observe an immediate replay from a detached controller.
func preflightRemoteSessionTarget(ctx context.Context, client *http.Client, base string, opts RemoteTabOpenOptions) (serveSessionEntry, error) {
	if id := strings.TrimSpace(opts.SessionID); id != "" {
		return serveSessionEntry{Name: id, SessionID: id, Title: strings.TrimSpace(opts.SessionTitle), Current: true}, nil
	}
	return serveCurrentSession(ctx, client, base)
}

// remoteSessionResumeBody uses the durable session identity for the current protocol.
func remoteSessionResumeBody(target serveSessionEntry) ([]byte, error) {
	id := strings.TrimSpace(target.SessionID)
	if id == "" {
		return nil, fmt.Errorf("remote session %q has no durable identity", target.Name)
	}
	return json.Marshal(map[string]string{"hostId": strings.TrimSpace(target.HostID), "sessionId": id})
}

func enterRemoteSessionTarget(ctx context.Context, client *http.Client, base string, opts RemoteTabOpenOptions) (serveSessionEntry, error) {
	if opts.NewSession {
		identity, err := servePostSessionIdentityForSession(ctx, client, serveURL(base, "/new"), nil, "")
		if err != nil {
			return serveSessionEntry{}, err
		}
		if identity.SessionID == "" {
			return serveSessionEntry{}, fmt.Errorf("remote new session returned no durable identity")
		}
		return serveSessionEntry{Name: identity.SessionID, SessionID: identity.SessionID, Current: true}, nil
	}
	id := strings.TrimSpace(opts.SessionID)
	if id == "" {
		return serveCurrentSession(ctx, client, base)
	}
	body, err := remoteSessionResumeBody(serveSessionEntry{SessionID: id})
	if err != nil {
		return serveSessionEntry{}, err
	}
	identity, err := servePostSessionIdentityForSession(ctx, client, serveURL(base, "/resume"), body, "")
	if err != nil {
		return serveSessionEntry{}, err
	}
	if identity.SessionID != id {
		return serveSessionEntry{}, fmt.Errorf("remote resume returned another session identity")
	}
	return serveSessionEntry{Name: id, SessionID: id, Title: strings.TrimSpace(opts.SessionTitle), Current: true, TakenOver: identity.TakenOver}, nil
}

func serveCurrentSession(ctx context.Context, client *http.Client, base string) (serveSessionEntry, error) {
	sessions, err := serveSessions(ctx, client, base)
	if err != nil {
		return serveSessionEntry{}, err
	}
	for _, session := range sessions {
		if session.Current {
			return session, nil
		}
	}
	return serveSessionEntry{}, nil
}

// installRemoteTabAttachRoute fences listings both before a target is installed
// and after Serve commits it, preventing either stale epoch from restoring the
// former current session.
func installRemoteTabAttachRoute(tab *remoteTab, path string) {
	path = strings.TrimSpace(path)
	if tab.routing.currentPath != path {
		tab.routing.currentPath = path
		tab.routing.pathRevision++
	}
	tab.routing.rehydratingPath = ""
	tab.routing.rehydratingFrames = nil
	if sessionID, ok := strings.CutPrefix(tab.routing.currentPath, remoteSessionIDRoutePrefix); ok {
		tab.session.path = ""
		tab.session.sessionID = sessionID
	} else {
		tab.session.path = tab.routing.currentPath
		tab.session.sessionID = ""
	}
	tab.routing.revision++
}

func commitRemoteTabAttachRoute(tab *remoteTab, path string, reset bool) {
	path = strings.TrimSpace(path)
	if reset || tab.routing.currentPath != path {
		resetRemoteTabForegroundRuntimeLocked(tab)
	}
	installRemoteTabAttachRoute(tab, path)
}

// routeRemoteTabFrame tracks background runtime without leaking its frames
// into the foreground reducer. Controller-level frames may be unbound.
func (a *App) routeRemoteTabFrame(tabID string, gen uint64, sessionPath, kind string) bool {
	a.remoteTabMu.Lock()
	tab := a.remoteTabs[tabID]
	if tab == nil || tab.gen != gen {
		a.remoteTabMu.Unlock()
		return false
	}
	if tab.routing.running == nil {
		tab.routing.running = map[string]bool{}
	}
	changed := false
	if sessionPath != "" {
		switch kind {
		case "turn_started":
			tab.routing.revision++
			changed = !tab.routing.running[sessionPath]
			tab.routing.running[sessionPath] = true
		case "turn_done":
			tab.routing.revision++
			changed = tab.routing.running[sessionPath]
			tab.routing.running[sessionPath] = false
		}
	}
	foreground := sessionPath == "" || tab.routing.currentPath != "" && sessionPath == tab.routing.currentPath
	_, knownBackground := tab.routing.running[sessionPath]
	// A detached turn can finish while background jobs remain. Its later notice
	// makes /sessions authoritative again, so refresh project-tree rows without
	// forwarding that background notice to the foreground reducer.
	backgroundChanged := !foreground && (changed || kind == "turn_done" || kind == "notice" && knownBackground)
	meta := remoteTabMetaLocked(tab)
	a.remoteTabMu.Unlock()
	if backgroundChanged {
		a.emitRemoteEvent("remote-tab:updated", meta)
	}
	return foreground
}

// adoptRemoteTabFrameCurrent consumes Serve's publication-time foreground
// marker. Unlike the running cache, this marker follows switches initiated by
// other HTTP clients and slash/recovery path changes while a tab stays open.
func (a *App) adoptRemoteTabFrameCurrent(tabID string, gen uint64, sessionPath string, reset bool) {
	if sessionPath == "" {
		return
	}
	a.remoteTabMu.Lock()
	tab := a.remoteTabs[tabID]
	a.remoteTabMu.Unlock()
	if tab == nil {
		return
	}
	tab.routeEventMu.Lock()
	defer tab.routeEventMu.Unlock()
	resetTitle := ""
	if reset {
		resetTitle = a.localizedDefaultTopicTitle()
	}
	a.remoteTabMu.Lock()
	current := a.remoteTabs[tabID]
	if current != tab || current.gen != gen {
		a.remoteTabMu.Unlock()
		return
	}
	// A spectator watches the session it explicitly selected; the serve's
	// foreground keeps emitting current markers for other sessions, and
	// adopting one silently re-routes the tab while the spectator banner
	// (takenOver) stays pinned to the old session — reclaim and submit then
	// target a session the tab is not displaying.
	if sessionPath != current.routing.currentPath && current.session.takenOver {
		a.remoteTabMu.Unlock()
		return
	}
	if !adoptRemoteTabSessionPathLocked(current, sessionPath) {
		a.remoteTabMu.Unlock()
		return
	}
	if reset {
		current.session.name = ""
		current.session.newSession = true
		current.session.reset = true
		current.topicTitle = resetTitle
	} else {
		// The routing marker has no title. Stop displaying the previous
		// session's title while the authoritative /sessions row is fetched.
		current.topicTitle = remoteWorkspaceName(current.ref.Workspace)
	}
	meta := remoteTabMetaLocked(current)
	ready := current.state == "ready"
	a.remoteTabMu.Unlock()
	a.emitRemoteEvent("remote-tab:updated", meta)
	if !reset {
		a.goRemoteTabSafe("remoteTabAdoptedTitle", func() { a.refreshRemoteTabTitle(tabID) })
	}
	if ready {
		// The frontend treats ready -> ready as a new surface generation. Emit it
		// before the triggering frame is forwarded so that frame is buffered until
		// the newly current session snapshot replaces the old transcript.
		a.emitRemoteEvent(fmt.Sprintf("remote-tab:%s:state", tabID), RemoteTabStateView{State: "ready"})
	}
}

// resetRemoteTabForegroundRuntimeLocked drops controller-local prompts and
// runtime state before a fresh session becomes visible. Caller holds
// remoteTabMu.
func resetRemoteTabForegroundRuntimeLocked(tab *remoteTab) {
	tab.pendingEvents = nil
	tab.ownership.readyBarrierPending = false
	tab.runtime.revision++
	tab.runtime.running = false
	tab.runtime.turnStartedAt = 0
	tab.runtime.backgroundJobs = 0
	tab.runtime.pendingPrompt = false
	tab.runtime.cancelRequested = false
	tab.runtime.cancellable = false
}

// adoptRemoteTabSessionPathLocked moves foreground-only state to a new session.
// Actionable events belong to the previous controller and must never be replayed
// into the newly hydrated surface. Caller holds remoteTabMu.
func adoptRemoteTabSessionPathLocked(tab *remoteTab, sessionPath string) bool {
	sessionPath = strings.TrimSpace(sessionPath)
	if tab == nil || sessionPath == "" || tab.routing.currentPath == sessionPath {
		return false
	}
	running := tab.routing.running[sessionPath]
	resetRemoteTabForegroundRuntimeLocked(tab)
	tab.routing.currentPath = sessionPath
	tab.routing.pathRevision++
	tab.routing.rehydratingPath = ""
	tab.routing.rehydratingFrames = nil
	tab.routing.revision++
	if sessionID, ok := strings.CutPrefix(sessionPath, remoteSessionIDRoutePrefix); ok {
		tab.session.path = ""
		tab.session.sessionID = sessionID
	} else {
		tab.session.path = sessionPath
		tab.session.sessionID = ""
	}
	tab.session.newSession = false
	tab.session.reset = false
	tab.runtime.running = running
	tab.runtime.cancellable = running
	return true
}

// remoteTabFramePathUnknown distinguishes a possible foreground recovery or
// slash-command rotation from a path already observed as background work.
func (a *App) routeRemoteTabWireFrame(tabID string, gen uint64, sessionPath, kind string, current, reset bool) bool {
	if current {
		a.adoptRemoteTabFrameCurrent(tabID, gen, sessionPath, reset)
	}
	return a.routeRemoteTabFrame(tabID, gen, sessionPath, kind)
}
