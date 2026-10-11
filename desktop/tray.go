package main

import "sync"

// desktopTray tracks Electron tray readiness.
type desktopTray struct {
	ready     chan struct{}
	readyOnce sync.Once
}

func newDesktopTray() *desktopTray { return &desktopTray{ready: make(chan struct{})} }

func (t *desktopTray) markReady() { t.readyOnce.Do(func() { close(t.ready) }) }

func (a *App) startTray() bool {
	if a == nil || a.shuttingDown.Load() || a.forceQuit.Load() {
		return false
	}
	if a.hostShell != nil {
		return a.hostShell.startTray()
	}
	a.setTrayHealth(nil, "unavailable", "host_unavailable")
	return false
}

func (a *App) stopTray() {
	if a != nil && a.hostShell != nil {
		a.hostShell.stopTray()
	}
}

func (a *App) updateTrayLocale(locale string) {
	if a != nil && a.hostShell != nil {
		a.hostShell.updateTrayLocale(locale)
	}
}
