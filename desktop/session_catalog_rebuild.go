package main

import (
	"errors"

	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/session"
	"sort"
)

var errSessionCatalogPanic = errors.New("session catalog rebuild failed unexpectedly")

type sessionCatalogRebuildFlight struct {
	done chan struct{}
	err  error
}

// RebuildSessionCatalog repairs current metadata through one cancellable flight.
// Duplicate callers join the same result without replacing the query owner.
func (a *App) RebuildSessionCatalog() (err error) {
	if a == nil || a.shuttingDown.Load() {
		return errors.New("application is shutting down")
	}
	a.catalogRebuildMu.Lock()
	if flight := a.catalogRebuild; flight != nil {
		a.catalogRebuildMu.Unlock()
		if a.catalogRebuildJoinHook != nil {
			a.catalogRebuildJoinHook()
		}
		<-flight.done
		return flight.err
	}
	flight := &sessionCatalogRebuildFlight{done: make(chan struct{})}
	a.catalogRebuild = flight
	a.catalogRebuilding.Store(true)
	a.catalogRebuildMu.Unlock()

	defer func() {
		a.catalogRebuilding.Store(false)
		panicValue := recover()
		if panicValue != nil {
			flight.err = errSessionCatalogPanic
		} else {
			flight.err = err
		}
		a.catalogRebuildMu.Lock()
		a.catalogRebuild = nil
		close(flight.done)
		a.catalogRebuildMu.Unlock()
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	err = a.runSessionCatalogRebuild()
	return err
}

func (a *App) runSessionCatalogRebuild() error {
	a.startSessionCatalog()
	a.catalogLifecycleMu.Lock()
	ctx := a.catalogContext
	a.catalogLifecycleMu.Unlock()
	if ctx == nil {
		return errors.New("current catalog owner is unavailable")
	}
	status := a.currentSessionCatalogStatus()
	a.emitProjectTreeChangedV2(status.Revision, nil, "catalog_rebuild_started")
	defer func() {
		a.catalogRebuilding.Store(false)
		if ctx.Err() == nil && !a.shuttingDown.Load() {
			a.emitProjectTreeChangedV2(a.currentSessionCatalogStatus().Revision, nil, "catalog_rebuild_finished")
		}
	}()
	state, err := a.workspaceRegistry().LoadProjection(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(state.SessionStates))
	for id, row := range state.SessionStates {
		if row.Lifecycle != workspacestate.Deleted {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var failures error
	query := a.desktopSessionService("").Query()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		a.lifecycleCheckpoint("current-catalog-before-refresh")
		_, err := query.RefreshMetadata(ctx, session.SessionRef{HostID: localDesktopHostID, SessionID: id})
		failures = errors.Join(failures, err)
	}
	return failures
}
