package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

type savedTabReconcileOutcome string

const (
	restoreTab            savedTabReconcileOutcome = "restore"
	dropStalePresentation savedTabReconcileOutcome = "drop_stale_presentation"
	preserveRecovery      savedTabReconcileOutcome = "preserve_recovery"
	preserveError         savedTabReconcileOutcome = "preserve_error"
)

type savedTabReconcileDecision struct {
	outcome           savedTabReconcileOutcome
	reason            string
	identityKind      string
	waitedForRecovery bool
	hadPending        bool
	hadRecoveryOwner  bool
	repairedIdentity  bool
}

type savedTabReconcileEvidence struct {
	registry    workspacestate.State
	registryErr error
}

func (a *App) reconcileTabsBeforeRestore(ctx context.Context, file desktopTabsFile, version uint64) (desktopTabsFile, uint64, bool) {
	original := file
	reconciled, changed := a.reconcileSavedTabs(ctx, file)
	if !a.tabsSnapshotCurrent(version) {
		return file, version, false
	}
	if changed {
		committedVersion, err := a.persistReconciledTabsFile(reconciled, version)
		if committedVersion != 0 {
			version = committedVersion
		}
		if errors.Is(err, errTabsSnapshotChanged) {
			return file, version, false
		}
		if err != nil {
			slog.Warn("desktop_saved_tab_reconcile_persist_failed", "reason", "write_failed")
			fallback := original
			return blockSavedTabUnsafeRestore(fallback, "identity_repair_write_failed"), version, a.tabsSnapshotCurrent(version)
		}
	}
	return reconciled, version, a.tabsSnapshotCurrent(version)
}

func (a *App) reconcileSavedTabs(ctx context.Context, file desktopTabsFile) (desktopTabsFile, bool) {
	file.Tabs = append([]desktopTabEntry(nil), file.Tabs...)
	evidence := a.loadSavedTabReconcileEvidence(ctx)
	for _, entry := range file.Tabs {
		if savedTabHasMatchingPendingCreate(entry, evidence) {
			if !a.waitForDesktopRecovery(ctx) {
				return blockSavedTabUnsafeRestore(file, "reservation_recovery_interrupted"), false
			}
			evidence = a.loadSavedTabReconcileEvidence(ctx)
			break
		}
	}
	decisions := make([]savedTabReconcileDecision, len(file.Tabs))
	for i, entry := range file.Tabs {
		decisions[i], _ = a.classifySavedTab(entry, evidence, true)
	}
	var removed map[string]bool
	file.Tabs, removed = filterReconciledSavedTabs(file.Tabs, decisions)
	if len(removed) == 0 {
		return file, false
	}
	repairReconciledTabSelection(&file, removed)
	return file, true
}

func filterReconciledSavedTabs(tabs []desktopTabEntry, decisions []savedTabReconcileDecision) ([]desktopTabEntry, map[string]bool) {
	filtered := make([]desktopTabEntry, 0, len(tabs))
	removed := map[string]bool{}
	for index, entry := range tabs {
		decision := decisions[index]
		if decision.outcome == dropStalePresentation {
			removed[entry.ID] = true
		} else {
			if (entry.SessionPath != "" || entry.SessionID != "") && (decision.outcome == preserveError || decision.outcome == preserveRecovery) {
				entry.restoreBlocked = true
				entry.restoreBlockReason = decision.reason
			}
			filtered = append(filtered, entry)
		}
		if decision.outcome == restoreTab && !decision.waitedForRecovery && !decision.repairedIdentity {
			continue
		}
		identityKind := decision.identityKind
		if identityKind == "" {
			identityKind = savedTabIdentityKind(entry)
		}
		slog.Info("desktop_saved_tab_reconciled",
			"outcome", decision.outcome,
			"reason", decision.reason,
			"identity_kind", identityKind,
			"waited_for_recovery", decision.waitedForRecovery,
			"had_pending_operation", decision.hadPending,
			"had_recovery_owner", decision.hadRecoveryOwner,
		)
	}
	return filtered, removed
}

func (a *App) waitForDesktopRecovery(ctx context.Context) bool {
	if a == nil || a.desktopRecoveryDone == nil {
		return false
	}
	if a.beforeSavedTabRecoveryWait != nil {
		a.beforeSavedTabRecoveryWait()
	}
	select {
	case <-a.desktopRecoveryDone:
		return !a.desktopRecoveryFailed.Load()
	case <-ctx.Done():
		return false
	}
}

func (a *App) loadSavedTabReconcileEvidence(ctx context.Context) savedTabReconcileEvidence {
	evidence := savedTabReconcileEvidence{}
	evidence.registry, evidence.registryErr = a.workspaceRegistry().Load(ctx)
	return evidence
}

func (a *App) classifySavedTab(entry desktopTabEntry, evidence savedTabReconcileEvidence, afterRecovery bool) (savedTabReconcileDecision, bool) {
	if !afterRecovery {
		if evidence.registryErr != nil {
			return savedTabReconcileDecision{}, false
		}
		if savedTabHasMatchingPendingCreate(entry, evidence) {
			return savedTabReconcileDecision{outcome: restoreTab, reason: "pending_create", hadPending: true}, true
		}
	}

	if strings.HasPrefix(entry.SessionPath, embeddedBotSessionPrefix) {
		id := strings.TrimPrefix(entry.SessionPath, embeddedBotSessionPrefix)
		if entry.SessionID != "" && entry.SessionID != id {
			return savedTabReconcileDecision{outcome: preserveError, reason: "bot_identity_conflict", identityKind: "bot"}, true
		}
		if _, err := a.embeddedBotSessionPath(entry.Scope, entry.WorkspaceRoot, entry.SessionPath); err != nil {
			return savedTabReconcileDecision{outcome: preserveError, reason: "bot_session_unavailable", identityKind: "bot"}, true
		}
		return savedTabReconcileDecision{outcome: restoreTab, reason: "bot_session_active", identityKind: "bot"}, true
	}
	if sessionID := strings.TrimSpace(entry.SessionID); sessionID != "" {
		return a.classifyCanonicalSavedTab(entry, sessionID, evidence, afterRecovery)
	}
	if strings.TrimSpace(entry.SessionPath) != "" {
		return savedTabReconcileDecision{outcome: dropStalePresentation, reason: "unsupported_path_identity"}, true
	}
	if strings.TrimSpace(entry.CreateOperationID) != "" && !afterRecovery {
		return savedTabReconcileDecision{}, false
	}
	if strings.TrimSpace(entry.CreateOperationID) != "" {
		return savedTabReconcileDecision{outcome: preserveError, reason: "pending_identity_unresolved", hadPending: true}, true
	}
	if afterRecovery && evidence.registryErr != nil {
		return savedTabReconcileDecision{outcome: preserveError, reason: "persistence_state_unavailable"}, true
	}
	return savedTabReconcileDecision{outcome: dropStalePresentation, reason: "identity_absent"}, true
}

func (a *App) classifyCanonicalSavedTab(entry desktopTabEntry, sessionID string, evidence savedTabReconcileEvidence, afterRecovery bool) (savedTabReconcileDecision, bool) {
	if evidence.registryErr != nil {
		if afterRecovery {
			return savedTabReconcileDecision{outcome: preserveError, reason: "persistence_state_unavailable"}, true
		}
		return savedTabReconcileDecision{}, false
	}
	if savedTabHasMatchingPendingCreate(entry, evidence) {
		return savedTabReconcileDecision{outcome: restoreTab, reason: "pending_create", hadPending: true}, true
	}

	status, registered := evidence.registry.SessionStates[sessionID]
	if registered && (status.Lifecycle == workspacestate.Archived || status.Lifecycle == workspacestate.Deleted) {
		return savedTabReconcileDecision{outcome: dropStalePresentation, reason: "inactive_lifecycle"}, true
	}
	info, statErr := a.desktopSessionService("").Query().Stat(a.bootContext(), session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID})
	if !afterRecovery {
		if registered && status.Lifecycle == workspacestate.Active && statErr == nil {
			workspace, consistent := savedTabCanonicalWorkspace(evidence.registry, info, sessionID)
			if consistent && savedTabMatchesWorkspace(entry, workspace) {
				return savedTabReconcileDecision{outcome: restoreTab, reason: "canonical_active"}, true
			}
		}
		return savedTabReconcileDecision{}, false
	}

	recoveryOwner := savedTabHasRecoveryOwner(entry, evidence)
	if registered && status.Lifecycle == workspacestate.Active {
		if errors.Is(statErr, session.ErrSessionNotFound) || errors.Is(statErr, os.ErrNotExist) {
			return savedTabReconcileDecision{outcome: preserveRecovery, reason: "registered_session_missing", hadRecoveryOwner: true}, true
		}
		if statErr != nil || info.MetadataStatus == session.MetadataFailed {
			return savedTabReconcileDecision{outcome: preserveError, reason: "canonical_session_unreadable", hadRecoveryOwner: recoveryOwner}, true
		}
		workspace, consistent := savedTabCanonicalWorkspace(evidence.registry, info, sessionID)
		if !consistent {
			if recoveryOwner {
				return savedTabReconcileDecision{outcome: preserveRecovery, reason: "recovery_owner_present", hadRecoveryOwner: true}, true
			}
			return savedTabReconcileDecision{outcome: preserveError, reason: "canonical_workspace_conflict", hadRecoveryOwner: recoveryOwner}, true
		}
		if !savedTabMatchesWorkspace(entry, workspace) {
			return savedTabReconcileDecision{outcome: restoreTab, reason: "repair_workspace"}, true
		}
		return savedTabReconcileDecision{outcome: restoreTab, reason: "canonical_active"}, true
	}
	if statErr == nil {
		return savedTabReconcileDecision{outcome: preserveRecovery, reason: "canonical_session_unregistered", hadRecoveryOwner: true}, true
	}
	if !errors.Is(statErr, session.ErrSessionNotFound) && !errors.Is(statErr, os.ErrNotExist) {
		return savedTabReconcileDecision{outcome: preserveError, reason: "canonical_session_unreadable", hadRecoveryOwner: recoveryOwner}, true
	}
	if recoveryOwner {
		return savedTabReconcileDecision{outcome: preserveRecovery, reason: "recovery_owner_present", hadRecoveryOwner: true}, true
	}
	if present, err := a.savedTabCanonicalArtifactsPresent(sessionID); err != nil {
		return savedTabReconcileDecision{outcome: preserveError, reason: "canonical_artifacts_unreadable"}, true
	} else if present {
		return savedTabReconcileDecision{outcome: preserveRecovery, reason: "canonical_artifacts_present", hadRecoveryOwner: true}, true
	}
	return savedTabReconcileDecision{outcome: dropStalePresentation, reason: "canonical_identity_absent"}, true
}

func (a *App) savedTabCanonicalArtifactsPresent(sessionID string) (bool, error) {
	if err := session.ValidateSessionID(sessionID); err != nil {
		return false, err
	}
	_, err := os.Lstat(filepath.Join(a.desktopSessions.root, sessionID))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func savedTabHasMatchingPendingCreate(entry desktopTabEntry, evidence savedTabReconcileEvidence) bool {
	operationID := strings.TrimSpace(entry.CreateOperationID)
	sessionID := strings.TrimSpace(entry.SessionID)
	if operationID == "" || sessionID == "" {
		return false
	}
	if pending, ok := evidence.registry.PendingCreates[sessionID]; ok && pending.OperationID == operationID {
		return true
	}
	return false
}

func savedTabPendingSessionIdentity(entry desktopTabEntry, evidence savedTabReconcileEvidence) (string, bool, bool) {
	if strings.TrimSpace(entry.SessionID) != "" {
		return "", false, false
	}
	operationID := strings.TrimSpace(entry.CreateOperationID)
	if operationID == "" || evidence.registryErr != nil {
		return "", false, false
	}
	resolved := ""
	accept := func(sessionID string) bool {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return true
		}
		if resolved != "" && resolved != sessionID {
			return false
		}
		resolved = sessionID
		return true
	}
	for sessionID, pending := range evidence.registry.PendingCreates {
		if pending.OperationID == operationID && !accept(sessionID) {
			return "", false, true
		}
	}
	for _, operation := range evidence.registry.PendingOperations {
		if strings.TrimSpace(operation.ID) != operationID {
			continue
		}
		for _, sessionID := range operation.SessionIDs {
			if !accept(sessionID) {
				return "", false, true
			}
		}

	}
	return resolved, resolved != "", false
}

func savedTabHasRecoveryOwner(entry desktopTabEntry, evidence savedTabReconcileEvidence) bool {
	sessionID := strings.TrimSpace(entry.SessionID)
	if pending, ok := evidence.registry.PendingCreates[sessionID]; sessionID != "" && ok && pending.SessionID == sessionID && !strings.HasPrefix(pending.OperationID, "draft-op-") {
		return true
	}
	for _, operation := range evidence.registry.PendingOperations {
		if sessionID != "" && slices.Contains(operation.SessionIDs, sessionID) {
			return true
		}

	}

	return false
}

func savedTabCanonicalWorkspace(state workspacestate.State, info session.SessionInfo, sessionID string) (workspacestate.Workspace, bool) {
	var owner workspacestate.Workspace
	for _, workspace := range state.Workspaces {
		if !slices.Contains(workspace.SessionIDs, sessionID) {
			continue
		}
		if owner.ID != "" {
			return workspacestate.Workspace{}, false
		}
		owner = workspace
	}
	if owner.ID == "" || info.Origin == "" || strings.TrimSpace(info.CWD) == "" || !sameDesktopPath(info.CWD, owner.Root) {
		return workspacestate.Workspace{}, false
	}
	return owner, true
}

func savedTabMatchesWorkspace(entry desktopTabEntry, workspace workspacestate.Workspace) bool {
	return restoredWorkspaceID(entry) == workspace.ID && sameDesktopPath(desktopWorkspaceRoot(entry.Scope, entry.WorkspaceRoot), workspace.Root)
}

func repairReconciledTabSelection(file *desktopTabsFile, removed map[string]bool) {
	local := make(map[string]bool, len(file.Tabs))
	remote := make(map[string]bool, len(file.RemoteTabs))
	for _, entry := range file.Tabs {
		local[entry.ID] = true
	}
	for _, entry := range file.RemoteTabs {
		remote[entry.ID] = true
	}
	order := make([]string, 0, len(file.TabOrder))
	for _, id := range file.TabOrder {
		if !removed[id] && (local[id] || remote[id]) {
			order = append(order, id)
		}
	}
	file.TabOrder = order
	if local[file.ActiveTab] || remote[file.ActiveTab] {
		return
	}
	file.ActiveTab = ""
	if len(order) > 0 {
		file.ActiveTab = order[0]
	} else if len(file.Tabs) > 0 {
		file.ActiveTab = file.Tabs[0].ID
	} else if len(file.RemoteTabs) > 0 {
		file.ActiveTab = file.RemoteTabs[0].ID
	}
}

func savedTabIdentityKind(entry desktopTabEntry) string {
	if strings.HasPrefix(entry.SessionPath, embeddedBotSessionPrefix) {
		return "bot"
	}
	if strings.TrimSpace(entry.SessionID) != "" {
		return "canonical"
	}
	if strings.TrimSpace(entry.SessionPath) != "" {
		return "unsupported"
	}
	return "none"
}
