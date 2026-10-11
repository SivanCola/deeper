package main

import (
	"slices"

	"deepzora/desktop/internal/legacycleanup"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
)

func classifyLegacyCleanupPersistentOwner(state workspacestate.State, ref session.SessionRef, frozen legacycleanup.Candidate) (legacyCleanupDecision, bool) {
	protected := func(reason string) (legacyCleanupDecision, bool) {
		return legacyCleanupDecision{"protected", reason, session.SessionInfo{}, session.Snapshot{}}, true
	}
	for _, recovery := range state.RecoveryEntries {
		if recovery.SessionID == ref.SessionID {
			return protected("recovery_owner")
		}
		for _, source := range frozen.Sources {
			if recovery.Path != "" && sameDesktopPath(recovery.Path, source.Path) {
				return protected("recovery_owner")
			}
		}
	}
	for _, operation := range state.PendingOperations {
		// Committed operations are durable receipts, not active owners.
		if operation.ID == frozen.OperationID || operation.Phase == "committed" {
			continue
		}
		mapped := operation.Mapping != nil && operation.Mapping.SessionID == ref.SessionID
		if slices.Contains(operation.SessionIDs, ref.SessionID) || mapped {
			return protected("pending_operation")
		}
	}
	return legacyCleanupDecision{}, false
}
