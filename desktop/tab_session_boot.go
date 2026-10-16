package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/plugin"
	"deepzora/internal/session"
)

type tabControllerBootResult struct {
	controller   control.SessionAPI
	ctx          context.Context
	registration *sharedHostMCPRegistration
	model        string
	fallback     bool
	err          error
}

func (a *App) sessionOpenBootOptions(
	tab *WorkspaceTab,
	snap tabRuntimeSnapshot,
	cfg *config.Config,
	service *session.Service,
	sharedHost *plugin.Host,
	root, model string,
) boot.Options {
	return boot.Options{
		Model:                   model,
		RequireKey:              false,
		StatsSource:             "desktop",
		TaskStore:               a.taskStore(),
		OnConfigLoadWarnings:    a.configLoadWarningsHandler(),
		Sink:                    a.desktopControllerSink(snap.sink, cfg.Notifications),
		WorkspaceRoot:           root,
		SessionDir:              sessionDirForSnapshot(snap),
		SessionService:          service,
		EffortOverride:          cloneStringPtr(snap.effort),
		EffortModel:             snap.model,
		SharedHost:              sharedHost,
		BrowserExecutor:         a.browserExecutorForRuntime(tab.ID, snap.sink),
		SharedSkillWatchService: a.sharedSkillWatchService(),
		MCPHostProfile:          plugin.HostProfileDesktopApps,
		PinnedContextLoader:     pinnedContextLoader(root),
		BeforeInboxDispatch:     a.beforeInboxDispatch,
		OnSessionTitleChanged:   a.onSessionTitleChanged,
	}
}

func (a *App) bootTabControllerWithModelFallback(
	baseCtx context.Context,
	tab *WorkspaceTab,
	cfg *config.Config,
	sharedHost *plugin.Host,
	options boot.Options,
	extensionGeneration, buildGeneration uint64,
	sessionID, requestedModel string,
) tabControllerBootResult {
	buildCtx, registration := beginSharedHostMCPRegistration(baseCtx, sharedHost)
	controller, err := a.buildTabControllerBootFenced(buildCtx, extensionGeneration, options)
	result := tabControllerBootResult{controller: controller, ctx: buildCtx, registration: registration, model: options.Model, err: err}
	a.mu.RLock()
	draftCreate := tab != nil && strings.TrimSpace(tab.PendingCreateOperationID) != ""
	a.mu.RUnlock()
	if !errors.Is(err, boot.ErrUnknownModel) || strings.TrimSpace(sessionID) == "" || draftCreate {
		return result
	}
	fallbackModel, _, ok := cfg.ResolveDesktopNewSessionModel()
	if !ok || fallbackModel == options.Model {
		return result
	}
	registration.rollback()
	result.ctx, result.registration = beginSharedHostMCPRegistration(baseCtx, sharedHost)
	options.Model = fallbackModel
	result.controller, result.err = a.buildTabControllerBootFenced(result.ctx, extensionGeneration, options)
	if result.err != nil {
		return result
	}
	result.model, result.fallback = fallbackModel, true
	a.noticeForTab(tab.ID, fmt.Sprintf("model %q is no longer available; switched to %s", requestedModel, fallbackModel))
	a.mu.Lock()
	if !a.tabBuildSupersededLocked(tab, buildGeneration) {
		tab.model, tab.Label = fallbackModel, fallbackModel
	}
	a.mu.Unlock()
	return result
}

func (a *App) bindTabCanonicalSession(
	ctx context.Context,
	identity control.IdentityLifecycle,
	cfg *config.Config,
	scope, workspaceRoot, sessionID, model string,
	modelFallback bool,
) (session.SessionRef, string, error) {
	var ref session.SessionRef
	var workspaceID string
	var err error
	switch {
	case strings.TrimSpace(sessionID) != "":
		service := identity.SessionService()
		if service == nil {
			return ref, "", errors.New("v3 session service is unavailable")
		}
		if err := identity.InitializeEmptySession(ctx, session.SessionRef{HostID: service.HostID(), SessionID: sessionID}); err != nil && !errors.Is(err, session.ErrSessionNotFound) {
			return ref, "", err
		}
		ref, err = identity.OpenSession(ctx, session.SessionRef{HostID: service.HostID(), SessionID: strings.TrimSpace(sessionID)})
		if errors.Is(err, session.ErrSessionNotFound) {
			operationID := ""
			if tab := a.tabForSessionBoot(scope, workspaceRoot, sessionID); tab != nil {
				operationID = tab.PendingCreateOperationID
			}
			if operationID != "" {
				ref, workspaceID, err = a.bindFreshDesktopSessionWithIDs(ctx, scope, workspaceRoot, identity, sessionID, operationID)
			}
		}
	default:
		ref, workspaceID, err = a.bindFreshDesktopSession(ctx, scope, workspaceRoot, identity)
	}
	if err == nil && workspaceID == "" {
		workspaceID, err = a.attachDesktopSession(ctx, scope, workspaceRoot, ref)
	}
	if err == nil && modelFallback {
		err = identity.SessionService().SetModel(ctx, ref, model, cfg.ModelSelectionIdentity(model))
	}
	return ref, workspaceID, err
}

func (a *App) tabForSessionBoot(scope, workspaceRoot, sessionID string) *WorkspaceTab {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab != nil && tab.Scope == scope && tab.SessionID == sessionID &&
			(scope != "project" || sameProjectRoot(tab.WorkspaceRoot, workspaceRoot)) {
			return tab
		}
	}
	return nil
}

func (a *App) buildSessionOpenControllerCandidate(
	ctx context.Context,
	extensionGeneration uint64,
	cfg *config.Config,
	options boot.Options,
) (control.SessionAPI, string, bool, error) {
	if hook := a.sessionOpenBuildHook; hook != nil {
		hook(ctx)
		if err := ctx.Err(); err != nil {
			return nil, options.Model, false, err
		}
	}
	requestedModel := options.Model
	candidate, err := a.buildTabControllerBootFenced(ctx, extensionGeneration, options)
	if !errors.Is(err, boot.ErrUnknownModel) {
		return candidate, requestedModel, false, err
	}
	fallbackModel, _, ok := cfg.ResolveDesktopNewSessionModel()
	if !ok || fallbackModel == requestedModel {
		return candidate, requestedModel, false, err
	}
	options.Model = fallbackModel
	candidate, err = a.buildTabControllerBootFenced(ctx, extensionGeneration, options)
	return candidate, fallbackModel, err == nil, err
}
