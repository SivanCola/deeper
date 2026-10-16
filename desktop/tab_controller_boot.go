package main

import (
	"context"
	"errors"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/boot"
	"deepzora/internal/control"
	"deepzora/internal/history"
	"deepzora/internal/session"
)

var errTabControllerExtensionsChanged = errors.New("desktop: controller extensions changed during build")

// buildTabControllerBoot is a thin wrapper around boot.Build so the large
// controller assembly path can stay under function-size / complexity budgets.
func (a *App) buildTabControllerBoot(ctx context.Context, opts boot.Options) (control.SessionAPI, error) {
	opts.SessionToolScope = a.sessionToolScope(opts.SessionDir)
	if opts.SessionService == nil {
		opts.SessionService = a.desktopSessionService(opts.SessionDir)
	}
	opts.HistorySources = a.globalHistorySources(opts.SessionDir)
	if opts.SessionCreateService == nil {
		opts.SessionCreateService = a.desktopSessionService(opts.SessionDir)
	}
	if opts.OnSessionRotation == nil {
		opts.OnSessionRotation = a.prepareDesktopSessionRotation
	}
	// Every controller on this host watches through one shared service, so a
	// rebuild reuses the helper process instead of spawning another one.
	if opts.SharedSkillWatchService == nil {
		opts.SharedSkillWatchService = a.sharedSkillWatchService()
	}
	return boot.Build(ctx, opts)
}

func (a *App) globalHistorySources(dir string) []history.Source {
	query := a.desktopSessionService(dir).Query()
	return []history.Source{{Query: query, Scope: "global", Membership: func(ctx context.Context) (map[string]bool, error) {
		state, err := a.workspaceRegistry().Load(ctx)
		if err != nil {
			return nil, err
		}
		ids := map[string]bool{}
		for _, id := range state.Workspaces[workspacestate.GlobalWorkspaceID].SessionIDs {
			if state.SessionStates[id].Lifecycle == workspacestate.Active {
				ids[id] = true
			}
		}
		return ids, nil
	}}, {Query: query, Scope: "archive", Membership: func(ctx context.Context) (map[string]bool, error) {
		state, err := a.workspaceRegistry().Load(ctx)
		if err != nil {
			return nil, err
		}
		ids := map[string]bool{}
		for _, id := range state.ArchivedSessionIDs {
			if state.SessionStates[id].Lifecycle == workspacestate.Archived {
				ids[id] = true
			}
		}
		return ids, nil
	}}}
}

func desktopSessionRoot(sessionDir string) string {
	return session.RootForSessionDir(sessionDir)
}

func (a *App) desktopSessionService(sessionDir string) *session.Service {
	if a == nil {
		return nil
	}
	a.sessionServicesMu.Lock()
	defer a.sessionServicesMu.Unlock()
	root := a.desktopSessions.root
	// Zero-value Apps in narrow tests use an isolated session root.
	// NewApp supplies the production root.
	if root == "" {
		root = desktopSessionRoot(sessionDir)
		a.desktopSessions.root = root
	}
	if root == "" {
		return nil
	}
	if a.sessionServices == nil {
		a.sessionServices = map[string]*session.Service{}
	}
	for _, service := range a.sessionServices {
		// Local desktop sessions share a single runtime owner.
		if service != nil {
			return service
		}
	}
	if service := a.sessionServices[root]; service != nil {
		return service
	}
	service, err := session.NewService("local", session.NewFilesystemPersistence(root))
	if err != nil {
		return nil
	}
	a.sessionServices[root] = service
	service.UseIdlePool(&a.historyIdlePool)
	service.ConfigureHistoryMaintenance(&a.historyMaintenance)
	return service
}

// buildTabControllerBootFenced keeps optimistic builds concurrent with each
// other but excludes live MCP mutation. The generation check happens after the
// gate so a build that loaded stale configuration never launches extensions.
func (a *App) buildTabControllerBootFenced(ctx context.Context, generation uint64, opts boot.Options) (control.SessionAPI, error) {
	a.extensionBuildMu.RLock()
	defer a.extensionBuildMu.RUnlock()
	if a.currentExtensionGeneration() != generation {
		return nil, errTabControllerExtensionsChanged
	}
	return a.buildTabControllerBoot(ctx, opts)
}

// lockTabControllerPublication makes extension generation and project
// maintenance reservations part of the same publication admission. An MCP
// writer bumps the generation before releasing runtimeAdmissionMu, while a
// worktree mutation publishes its canonical reservation before releasing the
// write side, so neither stale registries nor a late project controller can be
// installed afterward.
func (a *App) lockTabControllerPublication(generation uint64, scope, workspaceRoot string) (func(), bool) {
	a.runtimeAdmissionMu.RLock()
	if a.currentExtensionGeneration() != generation {
		a.runtimeAdmissionMu.RUnlock()
		return nil, false
	}
	if scope != "project" {
		return a.runtimeAdmissionMu.RUnlock, true
	}
	key := canonicalRuntimeRoot(workspaceRoot)
	if key == "" || a.workspaceMergeReservedSnapshot(key) {
		a.runtimeAdmissionMu.RUnlock()
		return nil, false
	}
	return a.runtimeAdmissionMu.RUnlock, true
}

func (a *App) handleTabControllerBootError(
	tab *WorkspaceTab,
	registration *sharedHostMCPRegistration,
	rootKey string,
	buildGeneration uint64,
	wailsCtx context.Context,
	err error,
) bool {
	if err == nil {
		return false
	}
	registration.rollback()
	if errors.Is(err, errTabControllerExtensionsChanged) {
		a.abandonSupersededBuild(nil, rootKey)
		a.scheduleDeferredStartupBuild(tab.ID)
		return true
	}
	a.mu.Lock()
	if a.tabBuildSupersededLocked(tab, buildGeneration) {
		a.mu.Unlock()
		a.abandonSupersededBuild(nil, rootKey)
		return true
	}
	leaseHeld, save := a.markTabStartupFailureLocked(tab, err, keepStartupRestore)
	hostKey := takeTabSharedHostKey(tab)
	a.mu.Unlock()
	a.writeTabsSaveRequest(save)
	if hostKey != "" {
		a.releaseSharedHost(hostKey)
	}
	if leaseHeld {
		a.scheduleDeferredStartupBuild(tab.ID)
	}
	a.emitReady(wailsCtx, tab.ID)
	return true
}
