package main

import (
	"context"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
	"errors"
	"log/slog"
	"strings"
)

// Settle current create reservations and interrupted lifecycle transactions before tab restore.
func (a *App) startDesktopSessionRecovery(ctx context.Context) {
	state, err := a.workspaceRegistry().Load(ctx)
	if err != nil {
		a.desktopRecoveryFailed.Store(true)
		close(a.desktopRecoveryDone)
		return
	}
	go func() {
		defer close(a.desktopRecoveryDone)
		if err := errors.Join(a.recoverDesktopPendingCreateSnapshot(ctx, state.PendingCreates), a.recoverDesktopSessionOperations(ctx)); err != nil {
			a.desktopRecoveryFailed.Store(true)
			slog.Warn("desktop session reservation recovery incomplete")
		}
	}()
}

// recoverDesktopPendingCreates completes the registry half of a create that
// reached durable session publication before the process stopped. A missing
// target is safe to forget: no canonical content exists for the pending ID and
// the UI can retry creation without inventing a replacement identity.
func (a *App) recoverDesktopPendingCreates(ctx context.Context) error {
	state, err := a.workspaceRegistry().Load(ctx)
	if err != nil {
		return err
	}
	return a.recoverDesktopPendingCreateSnapshot(ctx, state.PendingCreates)
}

func (a *App) recoverDesktopPendingCreateSnapshot(ctx context.Context, pendingCreates map[string]workspacestate.PendingCreate) error {
	service := a.desktopSessionService("")
	var joined error
	for sessionID, pending := range pendingCreates {
		ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID}
		if _, err := service.Query().Snapshot(ctx, ref); err == nil {
			var attachErr error
			if strings.HasPrefix(pending.OperationID, "rotate-") {
				attachErr = a.workspaceRegistry().CommitRotation(ctx, pending.OperationID, pending.WorkspaceID, sessionID, "", pending.ArchiveSource)
			} else {
				attachErr = a.workspaceRegistry().AttachSession(ctx, pending.OperationID, pending.WorkspaceID, sessionID, "")
			}
			if attachErr != nil {
				joined = errors.Join(joined, attachErr)
			} else {
				a.desktopSessions.pendingCreateRecovered.Add(1)
			}
		} else if errors.Is(err, session.ErrSessionNotFound) {
			if abortErr := a.workspaceRegistry().AbortCreate(ctx, sessionID); abortErr != nil {
				joined = errors.Join(joined, abortErr)
			}
		} else {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}
