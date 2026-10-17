package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"deepzora/internal/agentpreset"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

func desktopAgentPresetSource(ctx context.Context, service *session.Service, id string) *session.SessionRef {
	if service == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	ref := session.SessionRef{HostID: service.HostID(), SessionID: id}
	if _, err := service.Query().Stat(ctx, ref); errors.Is(err, session.ErrSessionNotFound) {
		return nil
	}
	return &ref
}

func desktopAgentPresetNeedsRefresh(ctrl control.SessionAPI) bool {
	presets, ok := ctrl.(control.AgentPresets)
	return ok && agentpreset.NormalizeID(presets.AgentPreset()) != agentpreset.DefaultID && !controllerHasActiveRuntimeWork(ctrl)
}

// ListAgentPresets also works before a provider is configured, so authoring a
// persona does not require creating a runnable conversation first.
func (a *App) ListAgentPresets(tabID string) (control.AgentPresetList, error) {
	if a.isRemoteTab(tabID) {
		return a.remoteAgentPresets(tabID)
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if presets, ok := ctrl.(control.AgentPresets); ok {
		return presets.ListAgentPresets()
	}
	root, err := a.agentPresetWorkspace(tabID, tab)
	if err != nil {
		return control.AgentPresetList{}, err
	}
	list, err := control.ListAgentPresetsForRoot(root)
	if err != nil || tab == nil {
		return list, err
	}
	a.mu.RLock()
	id := tab.SessionID
	a.mu.RUnlock()
	if id != "" {
		service := a.desktopSessionService("")
		snapshot, err := service.Query().Snapshot(a.bootContext(), session.SessionRef{HostID: service.HostID(), SessionID: id})
		if err != nil {
			return list, err
		}
		list.Current = agentpreset.NormalizeID(snapshot.Projection.AgentPreset)
		list.Locked = snapshot.Projection.HasTurns()
	}
	return list, nil
}

// SaveAgentPreset saves a definition for the next session build. Running
// conversations retain their assembled prompt and tool schemas.
func (a *App) SaveAgentPreset(tabID, scope, id, content string) (agentpreset.Preset, error) {
	if a.isRemoteTab(tabID) {
		return a.saveRemoteAgentPreset(tabID, scope, id, content)
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return agentpreset.Preset{}, readOnlyChannelErr()
	}
	if presets, ok := ctrl.(control.AgentPresets); ok {
		return presets.SaveAgentPreset(scope, id, content)
	}
	root, err := a.agentPresetWorkspace(tabID, tab)
	if err != nil {
		return agentpreset.Preset{}, err
	}
	return control.SaveAgentPresetForRoot(root, scope, id, content)
}

func (a *App) DeleteAgentPreset(tabID, scope, id string) error {
	if a.isRemoteTab(tabID) {
		return a.deleteRemoteAgentPreset(tabID, scope, id)
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if presets, ok := ctrl.(control.AgentPresets); ok {
		return presets.DeleteAgentPreset(scope, id)
	}
	root, err := a.agentPresetWorkspace(tabID, tab)
	if err != nil {
		return err
	}
	return control.DeleteAgentPresetForRoot(root, scope, id)
}

func (a *App) agentPresetWorkspace(tabID string, tab *WorkspaceTab) (string, error) {
	if tab == nil && strings.TrimSpace(tabID) != "" {
		return "", fmt.Errorf("unknown tab %q", tabID)
	}
	if tab == nil {
		return "", nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return tab.WorkspaceRoot, nil
}

// SetAgentPresetForTab replaces a blank session's assembled runtime. The
// candidate carries the durable selection in its staged session transaction.
func (a *App) SetAgentPresetForTab(tabID, id string) error {
	if a.isRemoteTab(tabID) {
		return a.setRemoteAgentPreset(tabID, id)
	}
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if tab == nil || ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	if err := a.ensureTabControllerWorkspace(tab); err != nil {
		return err
	}
	old, ok := a.controllerForTab(tab).(*control.Controller)
	if !ok {
		return fmt.Errorf("this session does not support agent presets")
	}
	if old.AgentPresetLocked() {
		return control.ErrAgentPresetLocked
	}
	if err := rebuildControllerActiveWorkErrorFor(old, "agent preset"); err != nil {
		return err
	}
	id = agentpreset.NormalizeID(id)
	if err := agentpreset.ValidID(id); err != nil {
		return err
	}
	if err := a.snapshotSettingsRebuildSource(tab, old, old.SessionPath(), "agent preset"); err != nil {
		return err
	}
	snap := a.tabRuntimeSnapshot(tab)
	cfg, err := config.LoadForRoot(snap.workspaceRoot)
	if err != nil {
		return err
	}
	generation := a.currentExtensionGeneration()
	opts := a.sessionOpenBootOptions(tab, snap, cfg, a.desktopSessionService(""), a.lookupSharedHost(snap.sharedHostKey), snap.workspaceRoot, snap.model)
	opts.AgentPresetOverride = &id
	a.extensionBuildMu.RLock()
	if a.currentExtensionGeneration() != generation {
		a.extensionBuildMu.RUnlock()
		return errTabControllerExtensionsChanged
	}
	res, err := rebuildTabRuntime(a, tab, old, opts)
	a.extensionBuildMu.RUnlock()
	if err != nil {
		return err
	}
	next := res.Controller
	defer func() {
		if a.controllerForTab(tab) != next {
			discardReplacementController(next, old)
		}
	}()
	a.bindControllerDisplayRecorder(next)
	configureControllerRuntime(next, old, snap.normalizedRuntime())
	release, current := a.lockTabControllerPublication(generation, snap.scope, snap.workspaceRoot)
	if !current {
		return errTabControllerExtensionsChanged
	}
	defer release()
	a.mu.Lock()
	if err := a.authorizeTabReplacementLocked(tab, next, "switching agent preset", "agent-preset"); err != nil {
		a.mu.Unlock()
		return err
	}
	if err := activateReplacementController(old, next); err != nil {
		a.mu.Unlock()
		return err
	}
	tab.Ctrl = next
	tab.Label = next.Label()
	tab.Ready = true
	clearTabStartupError(tab)
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	retireReplacedController(old, next)
	if err := next.Snapshot(); err != nil {
		a.noticeForTab(tab.ID, "agent preset selected; session persistence will retry: "+err.Error())
	}
	a.notifyTabRuntimeRebuilt(tab)
	a.emitReady(a.ctx, tab.ID)
	return nil
}
