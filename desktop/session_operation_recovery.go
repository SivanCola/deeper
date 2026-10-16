package main

import (
	"context"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// Resume only operations written by the current registry. This is crash
// recovery for current archive, restore and purge transactions.
func (a *App) recoverDesktopSessionOperations(ctx context.Context) error {
	state, err := a.workspaceRegistry().Load(ctx)
	if err != nil {
		return err
	}
	replay := func(op workspacestate.Operation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		release, ok := a.tryLockRuntimeMutation("replay session lifecycle")
		if !ok {
			return errTopicArchiveBusy
		}
		defer release()
		return a.replayDesktopSessionOperation(ctx, op)
	}
	joined := a.reconcileTopicRemovals(state)
	for _, op := range state.PendingOperations {
		if op.Kind == "purge" && op.Phase != "committed" {
			joined = errors.Join(joined, replay(op))
		}
	}
	for _, op := range state.PendingOperations {
		if op.Phase != "committed" && (op.Kind == "archive" || op.Kind == "restore") {
			joined = errors.Join(joined, replay(op))
		}
	}
	for _, op := range state.PendingOperations {
		if op.Kind != "command" || op.Phase == "committed" {
			continue
		}
		var req SessionLifecycleRequest
		if err := json.Unmarshal(op.Request, &req); err != nil {
			joined = errors.Join(joined, err)
			continue
		}
		_, err := a.ApplySessionLifecycle(req)
		joined = errors.Join(joined, err)
	}
	return joined
}

func (a *App) replayDesktopSessionOperation(ctx context.Context, op workspacestate.Operation) error {
	if op.Kind == "purge" {
		if len(op.SessionIDs) != 1 {
			return workspacestate.ErrMutationConflict
		}
		if err := a.resumeCanonicalPurge(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: op.SessionIDs[0]}, op); err != nil {
			return fmt.Errorf("replay purge session=%s phase=%s expected_generation=%d: %w", op.SessionIDs[0], op.Phase, op.ExpectedGeneration, err)
		}
		return nil
	}
	if len(op.SessionIDs) == 0 {
		return workspacestate.ErrMutationConflict
	}
	guards := []func(){}
	defer func() {
		for _, release := range slices.Backward(guards) {
			release()
		}
	}()
	removed := []removedSessionRuntime{}
	if op.Lifecycle == workspacestate.Archived || op.Lifecycle == workspacestate.Deleted {
		var err error
		removed, err = a.idleArchiveRuntimes(op.SessionIDs)
		if err != nil {
			return err
		}
	}
	service := a.desktopSessionService("")
	for _, id := range op.SessionIDs {
		ref := session.SessionRef{HostID: localDesktopHostID, SessionID: id}
		if runtime, live := service.Runtime(ref); live {
			phase := runtime.StateSnapshot().Phase
			if phase != session.RuntimeIdle && phase != session.RuntimeRecoveryRequired {
				return errTopicHasActiveWork
			}
		} else {
			guard, err := session.NewFilesystemPersistence(a.desktopSessions.root).AcquireMaintenance(id)
			if err != nil {
				return err
			}
			guards = append(guards, guard)
		}
		if _, err := service.Query().Snapshot(ctx, ref); err != nil {
			return err
		}
		if op.WorkspaceID != "" {
			if err := a.validateDesktopWorkspaceMembership(ctx, op.WorkspaceID, ref); err != nil {
				return err
			}
		}
	}
	if op.Phase == "prepared" {
		if err := a.workspaceRegistry().PrepareOperationContent(ctx, op.ID, op.SessionIDs, op.Presentation); err != nil {
			return err
		}
	}
	if err := a.workspaceRegistry().CommitOperation(ctx, op.ID); err != nil {
		return err
	}
	if len(removed) > 0 {
		a.finishArchivedRuntimeBindings(removed)
	}
	return nil
}
