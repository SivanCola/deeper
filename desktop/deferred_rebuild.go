package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/secrets"
	"deepzora/internal/session"
)

// deferredRebuildRetryInterval is how often pending runtime changes retry.
// Package-level so tests can shorten it.
var deferredRebuildRetryInterval = 2 * time.Second

const deferredStartupBuildLabel = "__startup__"

// deferredRuntimeReloadLabel marks a queued ReloadRuntime in the pending map.
// Like the startup label it is not a user setting name; the retry loop routes
// it to the boot.Rebuild reload path instead of a settings rebuild.
const deferredRuntimeReloadLabel = "__reload__"

// deferredRebuildState tracks pending settings, runtime reloads and startup
// attempts. Retries use the current session service's writer admission rather
// than acquiring a separate file lease. Tests can leave the loop disabled and
// drive individual retries deterministically.
type deferredRebuildState struct {
	mu      sync.Mutex
	pending map[string]deferredRebuildRequest
	next    uint64
	enabled bool
	running bool
	stopped bool
	stop    chan struct{}
}

type deferredRebuildReason uint8

const (
	deferredSettingsReason deferredRebuildReason = iota
	deferredStartupReason
	deferredReloadReason
)

type deferredRebuildRequest struct {
	reason    deferredRebuildReason
	label     string
	target    *WorkspaceTab
	runtimeID string
	revision  string
	sequence  uint64
}

// enableDeferredRebuildRetry arms the retry loop; called from startup.
func (a *App) enableDeferredRebuildRetry() {
	d := &a.deferredRebuild
	d.mu.Lock()
	defer d.mu.Unlock()
	d.enabled = true
	a.startDeferredRebuildLoopLocked()
}

// startDeferredRebuildLoopLocked starts the loop when it is armed, idle, and
// has work. Callers must hold d.mu.
func (a *App) startDeferredRebuildLoopLocked() {
	d := &a.deferredRebuild
	if !d.enabled || d.running || d.stopped || len(d.pending) == 0 {
		return
	}
	d.running = true
	if d.stop == nil {
		d.stop = make(chan struct{})
	}
	go a.deferredRebuildLoop(d.stop)
}

// scheduleDeferredRebuild records that tabID needs a runtime refresh for
// setting and starts the retry loop if it is not running yet. Repeated calls
// for the same tab collapse into one retry carrying the latest label.
func (a *App) scheduleDeferredRebuild(tabID, setting string) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return
	}
	a.mu.RLock()
	tab := a.tabs[tabID]
	request := deferredRebuildRequest{label: setting, target: tab}
	var snapshot modelSettingsSnapshot
	if tab != nil {
		request.runtimeID = tab.runtimeID
		snapshot, _ = tab.Ctrl.(modelSettingsSnapshot)
	}
	a.mu.RUnlock()
	if snapshot != nil {
		_, request.revision, _ = snapshot.ModelSettingsState()
	}
	switch setting {
	case deferredStartupBuildLabel:
		request.reason = deferredStartupReason
	case deferredRuntimeReloadLabel:
		request.reason = deferredReloadReason
	}
	d := &a.deferredRebuild
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if d.pending == nil {
		d.pending = map[string]deferredRebuildRequest{}
	}
	d.next++
	request.sequence = d.next
	d.pending[tabID] = request
	a.startDeferredRebuildLoopLocked()
}

func (a *App) scheduleDeferredStartupBuild(tabID string) {
	a.scheduleDeferredRebuild(tabID, deferredStartupBuildLabel)
}

func (a *App) deferredRebuildSequence(tabID string) uint64 {
	d := &a.deferredRebuild
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending[tabID].sequence
}

func (a *App) clearDeferredRebuildVersion(tabID string, sequence uint64) {
	d := &a.deferredRebuild
	d.mu.Lock()
	defer d.mu.Unlock()
	if sequence != 0 && d.pending[tabID].sequence == sequence {
		delete(d.pending, tabID)
	}
}

func (a *App) deferredRebuildPending(tabID string) bool {
	d := &a.deferredRebuild
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.pending[tabID]
	return ok
}

// stopDeferredRebuildRetry permanently stops the retry loop; used on shutdown
// and by tests.
func (a *App) stopDeferredRebuildRetry() {
	d := &a.deferredRebuild
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	if d.stop != nil {
		close(d.stop)
	}
}

func (a *App) deferredRebuildLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(deferredRebuildRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			d := &a.deferredRebuild
			d.mu.Lock()
			d.running = false
			d.mu.Unlock()
			return
		case <-ticker.C:
		}
		if a.deferredRebuildTickDone() {
			return
		}
	}
}

// deferredRebuildTickDone runs one retry pass and reports true when the loop
// should exit because nothing is pending anymore.
func (a *App) deferredRebuildTickDone() bool {
	return a.deferredRebuildTick(true)
}

func (a *App) deferredRebuildTick(markIdle bool) bool {
	d := &a.deferredRebuild
	d.mu.Lock()
	if d.stopped || len(d.pending) == 0 {
		if markIdle {
			d.running = false
		}
		d.mu.Unlock()
		return true
	}
	pending := make(map[string]deferredRebuildRequest, len(d.pending))
	maps.Copy(pending, d.pending)
	d.mu.Unlock()

	for tabID, request := range pending {
		a.retryDeferredRebuild(tabID, request)
	}
	return false
}

func (a *App) kickDeferredRebuildRetry() {
	if a.ctx == nil {
		return
	}
	a.goSafe("deferredRebuildKick", func() {
		_ = a.deferredRebuildTick(false)
	})
}

func (a *App) retryDeferredRebuild(tabID string, request deferredRebuildRequest) {
	if a.ctx == nil {
		return
	}
	tab := a.tabByID(tabID)
	a.mu.RLock()
	valid := tab != nil && tab == request.target && tab.runtimeID == request.runtimeID
	a.mu.RUnlock()
	if !valid {
		// The tab is gone; nothing left to refresh.
		a.clearDeferredRebuildVersion(tabID, request.sequence)
		return
	}
	if request.reason == deferredStartupReason {
		a.retryDeferredStartupBuild(tabID, tab, request.sequence)
		return
	}
	if request.reason == deferredReloadReason {
		a.retryDeferredRuntimeReload(tabID, tab, request.sequence)
		return
	}
	// Serialize retry publication with manual runtime rebuilds.
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	ctrl := a.controllerForTab(tab)
	if ctrl == nil {
		// Mid-(re)build on another path (provider retarget, workspace repair);
		// racing a second build+swap against it is what this loop must avoid.
		return
	}
	if request.label == "saved model settings" {
		pending, err := modelSettingsNeedApply(ctrl)
		if err == nil && !pending {
			a.clearDeferredRebuildVersion(tabID, request.sequence)
			return
		}
	}
	if (request.label == "saved model settings" && control.ModelReplacementBlocked(ctrl)) || (request.label != "saved model settings" && controllerHasActiveRuntimeWork(ctrl)) {
		return
	}
	setting := request.label
	err := a.rebuildSettingTurnLocked(setting, tab, false, setting == "saved model settings")
	if err == nil {
		// rebuildSettingLocked already cleared the pending entry for the tab it
		// refreshed; just announce it.
		a.noticeForTab(tabID, fmt.Sprintf("%s applied: session refreshed after the session became available", setting))
		return
	}
	if errors.Is(err, session.ErrWriterOwned) {
		return // grabbed back before we could rebuild; keep waiting
	}
	var busy *rebuildBusyError
	if errors.As(err, &busy) {
		return // a turn started meanwhile; retry once it finishes
	}
	// Anything else will not resolve by waiting; give up loudly instead of
	// retrying forever.
	a.clearDeferredRebuildVersion(tabID, request.sequence)
	if setting == "saved model settings" {
		a.mu.Lock()
		if a.ownsRuntimeTabLocked(tab) && tab.Ctrl == ctrl {
			tab.modelApplication.failure = &modelSettingsApplyFailure{ctrl, request.revision, modelSettingsIssue("apply_failed", err).Message}
		}
		a.mu.Unlock()
	}
	slog.Warn("desktop: deferred settings rebuild failed", "setting", setting, "tab", tabID, "err", secrets.RedactError(err))
	a.warnForTab(tabID, fmt.Sprintf("%s was saved but the session could not refresh: %s", setting, secrets.RedactError(err)))
}

// retryDeferredRuntimeReload retries an idle runtime. Busy turns and writer
// ownership conflicts remain pending; other failures are reported once.
func (a *App) retryDeferredRuntimeReload(tabID string, tab *WorkspaceTab, queuedSequence ...uint64) {
	sequence := a.deferredRebuildSequence(tabID)
	if len(queuedSequence) > 0 {
		sequence = queuedSequence[0]
	}
	// Serialize retry publication with manual runtime rebuilds.
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	ctrl := a.controllerForTab(tab)
	if ctrl == nil {
		// Mid-(re)build on another path; racing a second build+swap against it
		// is what this loop must avoid.
		return
	}
	if controllerHasActiveRuntimeWork(ctrl) {
		return
	}
	err := a.reloadRuntimeTurnLocked(tab)
	if err == nil {
		// rebuildSettingTurnLocked already cleared the pending entry for the
		// tab it refreshed; just announce it.
		a.noticeForTab(tabID, "runtime reloaded after the session went idle")
		return
	}
	if errors.Is(err, session.ErrWriterOwned) {
		return // grabbed back before we could reload; keep waiting
	}
	var busy *rebuildBusyError
	if errors.As(err, &busy) {
		return // a turn started meanwhile; retry once it finishes
	}
	// Anything else will not resolve by waiting; give up loudly instead of
	// retrying forever. The error may come from provider/config plumbing and
	// carry credential-shaped values (passwords, resolved API keys) — the
	// tested helper redacts before the text reaches logs or the frontend.
	a.clearDeferredRebuildVersion(tabID, sequence)
	failure := deferredReloadFailedText(err)
	slog.Warn("desktop: "+failure, "tab", tabID)
	a.warnForTab(tabID, failure)
}

// deferredReloadFailedText is the failure line for an unrecoverable deferred
// reload — the single formatter both the log and the tab warning use, so a
// credential-shaped error can never reach either sink unredacted.
func deferredReloadFailedText(err error) string {
	return "runtime reload failed: " + secrets.RedactCredentials(err.Error())
}

func (a *App) retryDeferredStartupBuild(tabID string, tab *WorkspaceTab, queuedSequence ...uint64) {
	sequence := a.deferredRebuildSequence(tabID)
	if len(queuedSequence) > 0 {
		sequence = queuedSequence[0]
	}
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	if !a.tabHasRetryableStartupLeaseError(tab) {
		a.clearDeferredRebuildVersion(tabID, sequence)
		return
	}
	a.mu.RLock()
	path := tab.currentSessionIdentity()
	a.mu.RUnlock()
	if path != "" && a.attachExistingSessionRuntime(tab, path, a.ctx) {
		a.clearDeferredRebuildVersion(tabID, sequence)
		return
	}
	err := a.rebuildStartupTabLocked(tab)
	if err == nil {
		a.clearDeferredRebuildVersion(tabID, sequence)
		return
	}
	if errors.Is(err, session.ErrWriterOwned) {
		return
	}
	a.clearDeferredRebuildVersion(tabID, sequence)
	slog.Warn("desktop: deferred session startup failed", "tab", tabID, "err", secrets.RedactError(err))
}

func (a *App) tabHasRetryableStartupLeaseError(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tabs[tab.ID] == tab && !tab.removed && tab.Ctrl == nil && (tab.StartupErrLeaseHeld || tab.modelApplication.startupRetry)
}

func (a *App) rebuildStartupTabLocked(tab *WorkspaceTab) error {
	buildCtx, cancel := context.WithCancel(a.bootContext())
	a.mu.Lock()
	if tab == nil || a.tabs[tab.ID] != tab || tab.removed {
		a.mu.Unlock()
		cancel()
		return nil
	}
	if tab.Ctrl != nil {
		a.mu.Unlock()
		cancel()
		return nil
	}
	if !tab.StartupErrLeaseHeld && !tab.modelApplication.startupRetry {
		a.mu.Unlock()
		cancel()
		return nil
	}
	tab.buildGeneration++
	generation := tab.buildGeneration
	if tab.buildCancel != nil {
		tab.buildCancel()
	}
	tab.buildCancel = cancel
	tab.Ready = false
	clearTabStartupError(tab)
	a.setSessionRuntimePhaseLocked(tab, sessionRuntimeStarting, nil)
	tab.ActivityStatus = ""
	if tab.sink == nil {
		tab.sink = &tabEventSink{tabID: tab.ID, app: a, ctx: a.ctx}
	}
	a.saveTabsLocked()
	a.mu.Unlock()

	a.buildTabControllerWithContext(tab, loadedTabSession{}, buildCtx, generation, cancel)

	a.mu.RLock()
	stillCurrent := false
	var ctrl control.SessionAPI
	startupErr := ""
	leaseHeld := false
	if tab != nil {
		stillCurrent = a.tabs[tab.ID] == tab && !tab.removed
		ctrl = tab.Ctrl
		startupErr = tab.StartupErr
		leaseHeld = tab.StartupErrLeaseHeld
	}
	a.mu.RUnlock()
	if !stillCurrent || ctrl != nil {
		return nil
	}
	if leaseHeld {
		return session.ErrWriterOwned
	}
	if strings.TrimSpace(startupErr) != "" {
		return fmt.Errorf("session startup: %s", startupErr)
	}
	return fmt.Errorf("session startup: controller was not built")
}

func (a *App) tryRecoverStartupLeaseHeldTab(tab *WorkspaceTab) bool {
	if a.ctx == nil || !a.tabHasRetryableStartupLeaseError(tab) {
		return false
	}
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	sequence := a.deferredRebuildSequence(tab.ID)
	if !a.tabHasRetryableStartupLeaseError(tab) {
		return a.controllerForTab(tab) != nil
	}
	err := a.rebuildStartupTabLocked(tab)
	if err == nil {
		a.clearDeferredRebuildVersion(tab.ID, sequence)
		return a.controllerForTab(tab) != nil
	}
	if errors.Is(err, session.ErrWriterOwned) {
		a.scheduleDeferredStartupBuild(tab.ID)
	} else {
		a.clearDeferredRebuildVersion(tab.ID, sequence)
	}
	return false
}
