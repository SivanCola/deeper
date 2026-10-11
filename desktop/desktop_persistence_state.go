package main

import (
	"sync"
	"sync/atomic"

	"deepzora/desktop/internal/sessionui"
	"deepzora/internal/config"
)

// desktopPersistenceState groups process-lifetime stores and their recovery
// coordinator so App does not expose each lifecycle field independently.
type desktopPersistenceState struct {
	desktopSessions            desktopSessionState
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
		sessionUI:           sessionui.New(config.DesktopSessionUIStatePath()),
		sessionPresets:      newSessionPresetStore(),
		desktopRecoveryDone: make(chan struct{}),
	}
}

func (a *App) startDesktopPersistenceReconciliation() {
	a.goSafe("reconcileManualSessionCreations", a.reconcileManualSessionCreations)
}
