package main

import (
	"sync"
	"sync/atomic"

	"deepzora/desktop/internal/draftstate"
	"deepzora/desktop/internal/sessionui"
	"deepzora/internal/config"
)

// desktopPersistenceState groups process-lifetime stores and their recovery
// coordinator so App does not expose each lifecycle field independently.
type desktopPersistenceState struct {
	desktopSessions            desktopSessionState
	desktopDrafts              *draftstate.Store
	sessionUI                  *sessionui.Store
	sessionPresets             *sessionPresetStore
	manualCreationMu           sync.Mutex
	manualCreationTasks        sync.WaitGroup
	manualCreations            *manualCreationManager
	desktopRecoveryDone        chan struct{}
	desktopRecoveryFailed      atomic.Bool
	beforeSavedTabRecoveryWait func()
}

func newDesktopPersistenceState() desktopPersistenceState {
	return desktopPersistenceState{
		desktopSessions:     newDesktopSessionState(),
		desktopDrafts:       draftstate.New(config.DesktopDraftStatePath()),
		sessionUI:           sessionui.New(config.DesktopSessionUIStatePath()),
		sessionPresets:      newSessionPresetStore(),
		desktopRecoveryDone: make(chan struct{}),
	}
}

func (a *App) startDesktopPersistenceReconciliation() {
	a.goSafe("reconcileManualSessionCreations", a.reconcileManualSessionCreations)
	// Workspace drafts are retired, including unsent content and interrupted
	// draft submissions. Do not open or reconcile their historical database.
}
