package main

import (
	"errors"
	"fmt"
	"net/http"

	"deepzora/internal/session"
)

type takeoverTabState uint8

const (
	takeoverTabUnavailable takeoverTabState = iota
	takeoverTabStartupBlocked
	takeoverTabLocalSpectator
)

func (a *App) takeoverTabState(tab *WorkspaceTab) takeoverTabState {
	if a == nil || tab == nil {
		return takeoverTabUnavailable
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.takeoverTabStateLocked(tab)
}

func (a *App) takeoverTabStateAt(tab *WorkspaceTab, epoch, path string) takeoverTabState {
	if a == nil || tab == nil {
		return takeoverTabUnavailable
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.runtimeEpochForTabLocked(tab) != epoch || sessionRuntimeKey(tab.currentSessionIdentity()) != sessionRuntimeKey(path) {
		return takeoverTabUnavailable
	}
	return a.takeoverTabStateLocked(tab)
}

func (a *App) takeoverTabStateLocked(tab *WorkspaceTab) takeoverTabState {
	if a.tabs[tab.ID] != tab || tab.removed {
		return takeoverTabUnavailable
	}
	if tab.Ctrl == nil && tab.StartupErrLeaseHeld {
		return takeoverTabStartupBlocked
	}
	if tab.Ctrl != nil && tab.ReadOnly && tab.Takeover.Spectator {
		return takeoverTabLocalSpectator
	}
	return takeoverTabUnavailable
}

func (a *App) markLocalTakeoverSpectator(tab *WorkspaceTab) {
	if a == nil || tab == nil {
		return
	}
	marked := false
	a.mu.Lock()
	if a.tabs[tab.ID] == tab && !tab.removed {
		tab.ReadOnly = true
		tab.Takeover.Spectator = true
		a.saveTabsLocked()
		marked = true
	}
	a.mu.Unlock()
	if marked {
		a.emitRuntimeEvent(tabMetaRefreshEventChannel, TabMetaRefreshEvent{TabID: tab.ID, Meta: a.MetaForTab(tab.ID)})
	}
}

// promoteLocalTakeoverSpectator prepares the current session runtime from the
// durable log, keeping the spectator published until replacement succeeds.
// The caller holds runtimeRebuildMu.
func (a *App) promoteLocalTakeoverSpectator(tab *WorkspaceTab, path, sourceEpoch string, record takeoverServeRecord, client *http.Client, grant takeoverGrant) error {
	a.runtimeAdmissionMu.Lock()
	defer a.runtimeAdmissionMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	if a.takeoverTabStateAt(tab, sourceEpoch, path) != takeoverTabLocalSpectator {
		a.endFailedTakeover(record, client, grant)
		return fmt.Errorf("tab changed while reclaiming the session; retry")
	}
	current := a.controllerForTab(tab)
	if controllerHasActiveRuntimeWork(current) {
		a.endFailedTakeover(record, client, grant)
		return fmt.Errorf("spectator still has active runtime work")
	}
	service := a.desktopSessionService("")
	ref, ok := sessionRefForRoute(service, path)
	if !ok {
		a.endFailedTakeover(record, client, grant)
		return fmt.Errorf("invalid session identity")
	}
	workspace, err := a.canonicalSessionWorkspace(a.bootContext(), ref)
	if err != nil {
		a.endFailedTakeover(record, client, grant)
		return err
	}
	snapshot, err := service.Query().Snapshot(a.bootContext(), ref)
	if err != nil {
		a.endFailedTakeover(record, client, grant)
		return err
	}
	err = a.runRebindCandidateHook("takeover_before_build")
	if err == nil {
		_, err = a.replaceControllerForSessionOpenLocked(a.bootContext(), tab, current, service, ref, snapshot.Projection.ModelRef, workspace)
	}
	if err != nil {
		if closeErr := service.Close(a.bootContext(), ref); closeErr != nil && !errors.Is(closeErr, session.ErrSessionNotRunning) {
			return fmt.Errorf("reclaim failed: %w; close unpublished runtime: %w", err, closeErr)
		}
		a.endFailedTakeover(record, client, grant)
		return err
	}
	a.mu.Lock()
	tab.Takeover.Spectator = false
	a.mu.Unlock()
	a.registerTakeoverMirror(sessionRuntimeKey(path), tab.ID, path, record, client, grant)
	a.setTabReadOnly(tab.ID, false)
	a.emitReady(a.ctx, tab.ID)
	return nil
}
