package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"deepzora/desktop/internal/browserops"
	"deepzora/desktop/internal/instanceidentity"
	"deepzora/internal/agent"
	"deepzora/internal/billing"
	"deepzora/internal/boot"
	"deepzora/internal/checkpoint"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/evidence"
	"deepzora/internal/extension/providerext"
	"deepzora/internal/fileref"
	fileenc "deepzora/internal/fileutil/encoding"
	"deepzora/internal/historywork"
	"deepzora/internal/i18n"
	"deepzora/internal/mcpdiag"
	"deepzora/internal/mcpregistry"
	"deepzora/internal/memory"
	"deepzora/internal/notify"
	"deepzora/internal/plugin"
	"deepzora/internal/proc"
	"deepzora/internal/provider"
	"deepzora/internal/repair"
	"deepzora/internal/session"
	"deepzora/internal/sessiontemp"
	"deepzora/internal/skill"
	"deepzora/internal/skill/skillwatch"
	"deepzora/internal/taskcatalog"
	"deepzora/internal/taskmonitor"
	"deepzora/internal/tool"
	"deepzora/internal/tool/builtin"
	"deepzora/internal/transcript"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// sessionTempFromController returns the logical-session private temporary
// directory manager for a same-session controller rebuild. Nil when the
// controller is missing or is not a *control.Controller.
func sessionTempFromController(ctrl control.SessionAPI) *sessiontemp.Manager {
	c, ok := ctrl.(*control.Controller)
	if !ok || c == nil {
		return nil
	}
	return c.SessionTemp()
}

// eventChannel is the Wails runtime event name the frontend subscribes to for the
// agent's typed event stream. One channel carries every event kind; the payload's
// `kind` field discriminates — the desktop analogue of the serve transport's SSE
// `data:` frames.
const eventChannel = "agent:event"

const singleInstanceIDPrefix = instanceidentity.Prefix

func singleInstanceID() string { return instanceidentity.ForHome(config.DeepzoraHomeDir()) }

// PromptHistoryEntry is one user prompt extracted from a session JSONL file.
// The frontend uses these for ↑/↓ prompt-history navigation.
type PromptHistoryEntry struct {
	Text        string `json:"text"`
	At          int64  `json:"at"` // unix ms
	SessionPath string `json:"sessionPath"`
	Turn        int    `json:"turn"`
}

// PromptHistoryResult is returned as one Wails value. It carries one loaded tape
// segment plus the cursor needed to keep walking toward older prompts.
type PromptHistoryResult struct {
	Entries     []PromptHistoryEntry `json:"entries"`
	Nonce       string               `json:"nonce"`
	OlderCursor string               `json:"olderCursor,omitempty"`
	HasOlder    bool                 `json:"hasOlder"`
}

// App is the Wails-bound application object: the desktop frontend's command
// surface. Its exported methods (Submit/Cancel/ResolvePromptForSession/…) are generated into JS
// bindings. The app manages multiple WorkspaceTabs — each with its own controller
// scoped to a project workspace — and routes commands to the active tab. Events
// flow the other way: each tab's controller emits to a tabEventSink that
// forwards events tagged with tabId to the webview via runtime.EventsEmit.
type App struct {
	sessionExportMu sync.Mutex
	sessionExports  map[string]*sessionExportJob
	ctx             context.Context
	host            nativeHost
	workspaceHub    *workspaceChangeHub
	// topicTitleMutationMu keeps the authoritative title commit and its Tab /
	// current presentation publication in the same order for manual and automatic
	// renames. It is never held by generic topic-state reads or other metadata.
	topicTitleMutationMu sync.Mutex
	// aiSessionTitleMu deduplicates explicit AI rename requests by durable
	// session identity. It never serializes different sessions.
	aiSessionTitleMu       sync.Mutex
	aiSessionTitleInFlight map[string]aiSessionTitleOperation
	// auxiliaryProviderGeneration invalidates bounded provider-only work when
	// model credentials/configuration or extension packages change. Cancellation
	// is an optimization; title CAS remains the final acceptance authority.
	auxiliaryProviderGeneration atomic.Uint64

	historyMaintenance                       historywork.Coordinator
	historyIdlePool                          session.IdlePool
	historyReaders                           desktopHistoryReaders
	catalogLifecycleMu                       sync.Mutex
	catalogCancel                            context.CancelFunc
	catalogContext                           context.Context
	catalogDone, catalogInitialReconcileDone chan struct{}
	catalogMetadataRequests                  chan struct{}        // guarded by catalogLifecycleMu
	catalogClosing                           *sessionCatalogClose // guarded by catalogLifecycleMu
	catalogRebuildMu                         sync.Mutex
	catalogRebuild                           *sessionCatalogRebuildFlight
	catalogRebuilding                        atomic.Bool
	shuttingDown                             atomic.Bool
	shutdownMu                               sync.Mutex
	shutdownCoordinator                      *desktopShutdownCoordinator
	// catalogRebuildJoinHook is test-only: it proves concurrent Wails callers
	// joined the published rebuild flight before its completion was released.
	catalogRebuildJoinHook func()
	// projectTreeCatalogRefreshHook is test-only: it proves runtime-only
	// navigation never falls back to the broad catalog refresh path.
	projectTreeCatalogRefreshHook func()

	// taskCtrl is the process-wide task-monitor control service (lazy; see
	// taskControl). One instance serializes control operations in-process.
	taskCtrl     *taskmonitor.ControlService
	taskCtrlOnce sync.Once

	// mu protects the tab map, tabOrder, activeTabID, and per-tab fields that are read
	// from bound methods. All bound methods that touch a controller use activeCtrl().
	mu          sync.RWMutex
	tabs        map[string]*WorkspaceTab
	tabOrder    []string
	activeTabID string
	readyHook   func()
	attachmentTargetState
	// tabSelectionMu serializes cross-registry activation. A remote selection
	// must not overtake the local-session snapshot that makes switching safe.
	tabSelectionMu sync.Mutex

	// Ticketed topic activation bookkeeping (StartTopicActivation). Guarded by
	// mu. activationGen bumps on every activation-or-supersede so a background
	// completion can tell whether it still owns publication; the pending
	// request/tab pair identifies the in-flight ticketed activation whose
	// completion may still prune and emit "ready".
	topicActivationState
	// activationEventHook is test-only: when set it replaces the
	// "topic:activation" runtime event emission so tests capture events
	// synchronously. Set before starting concurrent work, never mutate after.
	activationEventHook func(TopicActivationEvent)
	// tabBuildStartHook is test-only: called at the top of every tab
	// controller build (even already-superseded ones) so ordering tests can
	// gate builds. Same set-before-concurrency rule.
	tabBuildStartHook func(tabID string)
	// configLoadForRootHook is test-only: called from the background meta
	// extras refresh so tests can prove MetaForTab itself never loads config.
	configLoadForRootHook func(root string)

	// runtimeByID/runtimeBySessionKey form the process-local ownership registry.
	// App.mu guards both maps and every desktopSessionRuntime field.
	runtimeByID         map[string]*desktopSessionRuntime
	runtimeBySessionKey map[string]*desktopSessionRuntime
	// sessionServices contains one SessionID-only registry for the whole local
	// Desktop host. desktopSessions owns its persistence and navigation state.
	sessionServicesMu      sync.Mutex
	sessionServices        map[string]*session.Service
	projectSessionServices map[string]*session.Service
	// skillWatch is the one skill-watch service this host shares across every
	// controller build, so the host owns a single watcher helper process
	// instead of one per rebuild. Created on first use; closed, never cleared, at shutdown.
	skillWatchMu sync.Mutex
	skillWatch   *skillwatch.Service
	desktopPersistenceState

	// tabsRestored is closed when restoreOrBuildTabs has finished populating
	// a.tabs from desktop-tabs.json (or built the first-launch tab). Startup
	// work that inspects "which sessions are open" or persists the tab list
	// (recovery GC's DeleteSession does both) must wait on it: running against
	// the pre-restore empty tab map would treat every saved tab's session as
	// closed and could overwrite desktop-tabs.json with an empty snapshot.
	tabsRestored chan struct{}

	// projectTreeChangedHook is test-only: set once before any concurrency
	// starts, then read lock-free from emitProjectTreeChanged (whose callers
	// may or may not hold a.mu, so it cannot re-lock). Never write it after
	// startup.
	projectTreeChangedHook func()
	projectTreeRuntime     projectTreeRuntimeState
	runtimeStateProjection desktopRuntimeProjection
	remoteRuntimeSync      remoteRuntimeSync

	// singleSurfaceMu serializes open/reuse plus visible-tab pruning for the
	// one-conversation layout so overlapping navigation cannot remove the tab
	// another navigation is still activating.
	singleSurfaceMu sync.Mutex
	// worktreeMergeMu serializes the inspect-confirm-merge/finalize mutation
	// boundary. Git identities are still revalidated after workspace leases are
	// acquired; this mutex only prevents duplicate in-process Wails calls.
	worktreeMergeMu sync.Mutex
	// Worktree runtime reservations are ordered before App.mu. Runtime owners
	// hold this gate through final publication; callers must never acquire it
	// under App.mu. Merge reservations cover both the source and isolated roots,
	// while cleanup reservations cover the complete allocation through removal.
	worktreeReservations worktreeRuntimeReservations
	// navigationIntent linearizes frontend intent publication with the final
	// merged-worktree removal before the runtime mutation barrier and App.mu.
	navigationIntent navigationIntentFence

	// sessionRemovalMu serializes operations that remove visible or detached
	// session bindings. Those operations may snapshot controllers before
	// deletion; keep that snapshot outside a.mu, but do not let DeleteSession or
	// topic/workspace removal trash the same files while it is in flight.
	sessionRemovalMu sync.Mutex

	// runtimeRebuildMu serializes controller rebuilds (build + swap), teardown,
	// and MCP lifecycle mutations. Two concurrent rebuilds of the same tab both
	// pass the tab-identity check at swap time, while MCP launch authorization racing
	// a toggle/reconnect can restore stale tools or launch a second single-instance
	// server. MCP paths insert extensionBuildMu between runtimeRebuildMu and
	// runtimeAdmissionMu; both orders end at App.mu -> Host/Registry.
	runtimeRebuildMu sync.Mutex
	// runtimeAdmissionMu is the runtime lifecycle barrier. Foreground turn-start
	// tokens and the short publication phase of asynchronous controller builds
	// hold the read side; runtime teardown and MCP lifecycle mutations hold the
	// write side so their captured controller/Host cannot be replaced, closed, or
	// handed a late turn in flight. Writers already hold runtimeRebuildMu, making
	// them mutually exclusive. Read holders must never acquire runtimeRebuildMu,
	// or a queued writer would deadlock the pair.
	runtimeAdmissionMu sync.RWMutex
	appLifecycleTestHooks
	// modelSwitchTimingHook is test-only. Production diagnostics use the same
	// sanitized timing record through debug logging.
	modelSwitchTimingHook func(modelSwitchTiming)
	// rebindCandidateHook is test-only. It exposes deterministic transaction
	// boundaries without weakening the production lock order. Set it before
	// starting a rebind and never mutate it until that rebind returns.
	rebindCandidateHook func(string) error
	// providerCatalogBeforeCredentialLockHook is test-only. It pauses catalog
	// compare-and-apply after its optimistic credential snapshot but before the
	// shared credential lock and authoritative re-read.
	providerCatalogBeforeCredentialLockHook func(string)

	// tryRunMu guards tryRunCancel — the cancel handle for the single
	// in-flight settings-page subagent try run (TrySubagentProfile /
	// CancelTrySubagentProfile).
	tryRunMu     sync.Mutex
	tryRunCancel context.CancelFunc

	// updaterOperationMu guards the single native download/install operation.
	// Checks are read-only and may overlap; cache mutation and installation fail
	// fast when another updater operation is already active.
	updaterOperationMu sync.Mutex
	updaterOperationID string

	// deferredRebuild tracks tabs whose settings were saved but whose runtime
	// could not refresh because the session lease was held by another process.
	deferredRebuild deferredRebuildState

	// detachedSessions keeps live session runtimes whose visible tab was closed.
	// It is process-local by design: shutdown closes every detached controller.
	detachedSessions map[string]*WorkspaceTab

	// takeoverMirrors tracks sessions this desktop took over from a local
	// serve: the tab writes locally while its events mirror to the remote tab.
	takeoverMirrors        map[string]*takeoverMirror
	takeoverAdoptRevisions map[string]uint64
	takeoverMu             sync.Mutex
	// serveProbeUntil suppresses serve probing after a failed handshake
	// (rotated token file); guarded by serveProbeMu.
	serveProbeUntil map[string]time.Time
	serveProbeMu    sync.Mutex

	// sharedHosts holds one *plugin.Host per workspace root, shared by all
	// controllers/tabs in that root so MCP subprocesses (CodeGraph, etc.) are
	// spawned once instead of N times. Lifecycle: first Acquire creates the
	// host, last Release closes it.
	sharedHosts   map[string]*sharedPluginHost
	sharedHostsMu sync.Mutex
	// extensionGeneration fences off-lock shared-host boot against MCP mutations;
	// stale generations abandon publication instead of restoring old tools.
	extensionGeneration atomic.Uint64
	extensionBuildMu    sync.RWMutex

	// tabsSaveMu serializes writes to desktop-tabs.json and its fixed .tmp path.
	tabsSaveMu             sync.Mutex
	tabsSaveVersion        uint64                     // protected by mu; assigned when collecting a snapshot
	tabsLastWrittenVersion uint64                     // protected by tabsSaveMu
	tabsFileExtra          map[string]json.RawMessage // protected by tabsSaveMu; unknown top-level persistence fields

	forceQuit           atomic.Bool
	backgroundMaximised atomic.Bool
	desktopLocale       atomic.Int32
	trayReady           bool
	tray                *desktopTray
	desktopShell        desktopShellRuntimeState

	mediaTokens    *mediaTokenStore
	presentPreview *workspacePreviewOrigin

	metrics atomic.Pointer[metricsAggregator] // non-nil only when desktop.metrics is opted in; swapped live by SetDesktopMetrics

	notificationSenderOnce sync.Once
	notificationSender     notify.Sender

	runtimeEvents  asyncRuntimeEmitter
	mcpAppsSandbox mcpAppsSandbox

	// terminals owns local PTY/ConPTY sessions. It is intentionally separate
	// from chat runtimes: terminal lifecycle must never acquire App.mu or the
	// controller rebuild locks while process I/O is blocked.
	terminals *terminalManager

	// Remote SSH module: the manager is created lazily on the first remote
	// binding call and closed on shutdown.
	remoteMu      sync.Mutex
	remoteRuntime remoteKernel

	// Remote web windows (SSH Serve pages). The Electron shell owns one
	// BrowserWindow per host; the bridge tracks which host keys are open.
	// Host-scoped lifecycle operations are generation-fenced and serialized so
	// an overlapping disconnect/stop cannot miss a window that is still being
	// opened. Closing a window never stops the remote Serve or the SSH
	// connection.
	remoteWindows          *remoteWindowRegistry
	remoteWindowLifecycles remoteWindowLifecycleRegistry
	remoteWindowOpener     func(remoteWindowLaunch) error // test-only injection
	// Remote project tabs are in-app surfaces bound to a remote workspace.
	// Project pins persist in user config; open tab shells persist separately
	// and restore disconnected until the user activates them.
	remoteTabMu     sync.Mutex
	remoteTabs      map[string]*remoteTab
	remoteTabLayout remoteTabLayoutState
	remoteTabTasks  sync.WaitGroup
	// remoteTabModelMu makes the caller's current-model snapshot, the remote
	// Serve rebuild, and the tab metadata commit one transaction. Without it,
	// overlapping switches could roll remote config back to a stale model.
	remoteTabModelMu          sync.Mutex
	modelSettingsSubmitMu     sync.Mutex
	modelSettingsReceipts     map[string]modelSettingsReceipt
	modelSettingsReceiptOrder []string
	// remoteEventHook observes remote events in tests; production leaves it nil.
	remoteEventHook func(name string, payload any)
	// credProxy is the lazy app-wide key holder for local-proxy mode.
	credProxyMu sync.Mutex
	credProxy   *credentialProxy
	// browserBroker is the lazy app-wide loopback broker remote serves reach
	// through SSH reverse tunnels; per-generation tokens isolate hosts.
	browserBrokerMu sync.Mutex
	browserBroker   *browserBroker

	// promptHistoryTape is a lazy, cursor-addressed view of prompt history. It
	// stores session order and per-session parsed entries only after that session is
	// reached by ↑ navigation. See ScanPromptHistory.
	promptHistoryMu   sync.Mutex
	promptHistoryTape *promptHistoryTape

	skillRootsMu    sync.Mutex
	skillRootsCache skillRootsCache

	heartbeat *HeartbeatEngine // scheduled heartbeat tasks; nil until startup
	lifecycle desktopLifecycleRuntime
	// diagnosticsOwner is acquired before Wails starts so Linux's OnStartup
	// ordering cannot let a second-instance handoff create lifecycle evidence.
	diagnosticsOwner        bool
	diagnosticsOwnerRelease func()
	diagnosticsConfigLoaded bool
	diagnosticsTelemetry    bool
	// Healthy-update identity is captured before Wails starts. A process may
	// commit only the complete probationary transaction it actually booted from,
	// never a rewritten or later same-version retry.
	healthyUpdateCreatedAt     string
	healthyUpdateTransactionID string
	// startupReady records that React rendered and the host bridge heartbeat
	// succeeded. DOM navigation alone is not application health.
	startupReady atomic.Bool
	// hostShell is non-nil only under the Electron shell (--host-rpc).
	hostShell *hostShellBridge
	// browserExecutors are per-tab browser grants over the shell; browserOps is
	// the durable write ledger shared by all of them. Both lazily created.
	browserExecMu    sync.Mutex
	browserExecutors map[string]*hostBrowserExecutor
	browserOps       *browserops.Ledger
	// Browser operations and lifecycle invalidation have separate lock domains.
	filePreviews fileBrowserPreviewRegistry
	// browserControl is the shell-pushed switch that decides whether new
	// sessions may drive the built-in browser at all.
	browserControl browserControl
}

type desktopShellRuntimeState struct {
	coordinator *desktopShellCoordinator
	trayState   string
	trayReason  string
}

type skillRootsCache struct {
	key   string
	at    time.Time
	roots []SkillRootView
}

// jsProfilingMiddleware opts every asset response into the JS Self-Profiling
// document policy so the frontend performance monitor can attach sampled stacks
// to long-task reports. Chromium honors both the header and the API; other
// engines degrade to unattributed reports.
func (a *App) jsProfilingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Document-Policy", "js-profiling")
			next.ServeHTTP(w, r)
		})
	}
}

// NewApp constructs the bound object. Tabs are restored in startup from the
// last session's desktop-tabs.json.
func NewApp() *App {
	a := &App{
		tabs:                    map[string]*WorkspaceTab{},
		runtimeByID:             map[string]*desktopSessionRuntime{},
		runtimeBySessionKey:     map[string]*desktopSessionRuntime{},
		sessionServices:         map[string]*session.Service{},
		aiSessionTitleInFlight:  map[string]aiSessionTitleOperation{},
		desktopPersistenceState: newDesktopPersistenceState(),
		detachedSessions:        map[string]*WorkspaceTab{},
		mediaTokens:             newMediaTokenStore(),
		presentPreview:          newWorkspacePreviewOrigin(),
		remoteWindows:           newRemoteWindowRegistry(),
		worktreeReservations: worktreeRuntimeReservations{
			cleanup: map[string]struct{}{},
			merge:   map[string]struct{}{},
		},
	}
	a.desktopShell.trayState = "probing"
	a.desktopShell.coordinator = newDesktopShellCoordinator(a)
	a.workspaceHub = newWorkspaceChangeHub(a)
	a.terminals = newTerminalManager(a)
	return a
}

func (a *App) bootContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// Platform exposes the native OS to the frontend so chrome/layout affordances can
// stay platform-scoped instead of relying on browser user-agent guesses.
func (a *App) Platform() string {
	return goruntime.GOOS
}

// startup runs once the shell's renderer is up, before the frontend can issue
// any bound call. It stores the service-lifetime context, then kicks off the
// initialization in a background goroutine so the page loads immediately.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.shuttingDown.Store(false)
	a.initializeDesktopSessionRoot()
	// Only the process that claimed the pre-shell diagnostics lock consumes
	// lifecycle evidence.
	initializeLifecycleDiagnostics(a)
	a.desktopShell.coordinator.start(ctx)
	a.lifecycle.tracker.markAsync("ready")
	a.startNativeShellSupport()
	a.enableDeferredRebuildRetry()
	// Historical bodies are prepared only when a reader requests them.
	a.mu.Lock()
	a.tabsRestored = make(chan struct{})
	a.mu.Unlock()
	a.startDesktopSessionRecovery(ctx)

	if cfg, err := config.Load(); err == nil && cfg.DesktopMetrics() && version != "dev" {
		a.metrics.Store(newMetricsAggregator(config.MemoryUserDir()))
		a.recordSettingsMetricsSnapshot(cfg)
	}
	a.recordPreviousRunDiagnostics()

	a.heartbeat = newHeartbeatEngine(a)
	a.heartbeat.Start()

	go a.restoreOrBuildTabs()
	a.startDesktopPersistenceReconciliation()
	a.registerHistoryIndexEvents()
	a.startSessionCatalog()
	a.goSafe("sendStartupPing", a.sendStartupPing)
	a.goSafe("flushMetrics", a.flushMetrics)
	a.goSafe("flushPendingCrash", a.flushPendingCrash)
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.forceQuit.Swap(false) || consumeSystemQuitRequested() {
		return false
	}
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		cfg = config.LoadForEdit(config.UserConfigPath())
	}
	if cfg.DesktopCloseBehavior() == "background" {
		if !a.backgroundCloseHasRestorePath() {
			return false
		}
		// Never query native maximise state here: during close the Win32 DPI
		// path can report 0 and panic inside Wails ScaleToDefaultDPI. Use the
		// last frontend-reported geometry instead.
		a.backgroundMaximised.Store(a.lastKnownMaximised())
		a.saveWindowStateSync()
		a.snapshotAllTabs()
		if a.desktopShell.coordinator != nil {
			return a.desktopShell.coordinator.hideToBackground(ctx, func() bool {
				return backgroundCloseUsesApplicationHide(goruntime.GOOS) || a.isTrayReady()
			})
		}
		hideForBackground(ctx, a.nativeHost())
		return true
	}
	return false
}

const backgroundCloseTrayReadyTimeout = 500 * time.Millisecond

func (a *App) backgroundCloseHasRestorePath() bool {
	if backgroundCloseUsesApplicationHide(goruntime.GOOS) {
		return backgroundCloseHasRestorePathFor(goruntime.GOOS, false, false)
	}
	if !a.startTray() {
		return false
	}
	return backgroundCloseHasRestorePathFor(goruntime.GOOS, true, a.waitForTrayReady(backgroundCloseTrayReadyTimeout))
}

func (a *App) waitForTrayReady(timeout time.Duration) bool {
	if a.isTrayReady() {
		return true
	}
	ready := a.trayReadySignal()
	if ready == nil {
		return false
	}
	if timeout <= 0 {
		select {
		case <-ready:
			return a.isTrayReady()
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ready:
		return a.isTrayReady()
	case <-timer.C:
		return a.isTrayReady()
	}
}

func (a *App) isTrayReady() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.trayReady
}

func (a *App) trayReadySignal() <-chan struct{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.tray == nil {
		return nil
	}
	return a.tray.ready
}

// markTabsRestored closes the tabsRestored gate exactly once. Safe when the
// channel was never created (tests that drive App without startup).
func (a *App) markTabsRestored() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tabsRestored == nil {
		return
	}
	select {
	case <-a.tabsRestored:
	default:
		close(a.tabsRestored)
	}
}

// tabsRestoredSignal returns a channel closed once tab restore has completed.
// When startup never armed the gate (tests), it reports already-restored.
func (a *App) tabsRestoredSignal() <-chan struct{} {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.tabsRestored == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return a.tabsRestored
}

func (a *App) showMainWindow() {
	a.showMainWindowFrom("menu")
}

func (a *App) secondInstanceLaunch() {
	a.showMainWindowFrom("second_instance")
}

func (a *App) quitApp() {
	if a.ctx == nil {
		return
	}
	a.forceQuit.Store(true)
	a.nativeHost().Quit(a.ctx)
}

func hideForBackground(ctx context.Context, host nativeHost) {
	if backgroundCloseUsesApplicationHide(goruntime.GOOS) {
		host.HideApplication(ctx)
		return
	}
	host.HideWindow(ctx)
}

func backgroundCloseUsesApplicationHide(goos string) bool {
	return goos == "darwin"
}

func backgroundCloseHasRestorePathFor(goos string, trayStarted, trayReady bool) bool {
	return backgroundCloseUsesApplicationHide(goos) || (trayStarted && trayReady)
}

type backgroundRestorePlan struct {
	maximiseBeforeShow  bool
	unminimiseAfterShow bool
}

func backgroundRestorePlanFor(goos string, wasMaximised bool) backgroundRestorePlan {
	if backgroundRestoreShouldMaximise(goos, wasMaximised) {
		return backgroundRestorePlan{maximiseBeforeShow: true}
	}
	return backgroundRestorePlan{unminimiseAfterShow: true}
}

func backgroundRestoreShouldMaximise(goos string, wasMaximised bool) bool {
	return wasMaximised && !backgroundCloseUsesApplicationHide(goos)
}

// restoreOrBuildTabs restores the tabs from the last session, or creates a
// default Global tab on first launch.
func (a *App) restoreOrBuildTabs() {
	defer a.recoverToPending("restoreOrBuildTabs")
	// Unblock startup work gated on the restore (recovery GC) no matter how
	// this returns — including the recover path above.
	defer a.markTabsRestored()
	// Reap any orphaned codegraph processes from a previous crash or older
	// version that leaked them, so they don't accumulate across restarts.
	a.reapOrphanCodeGraph()
	ctx := a.ctx
	ensureWorkspace()

	f, tabsVersion := a.loadTabsForRestore()

	// Load i18n from the first available config.
	// Prefer DesktopLanguage (desktop UI setting) over Language (CLI setting),
	// so the user's language choice in desktop settings takes effect.
	a.loadStartupLocale()
	f, _, restoreCurrent := a.reconcileTabsBeforeRestore(ctx, f, tabsVersion)
	if !restoreCurrent {
		return
	}
	// Every surviving layout style is single-surface, and a config that failed
	// to load already took this path when the predicate could still be false.
	f = singleSurfaceTabsFile(f)
	// Restore remote tabs as disconnected shells; activation performs the
	// first network work so desktop startup remains offline-safe.
	a.restoreRemoteTabShells(f)
	if len(f.Tabs) > 0 {
		toBuild := make([]*WorkspaceTab, 0, len(f.Tabs))
		for _, entry := range f.Tabs {
			releaseAdmission, admissionErr := a.beginProjectRuntimeAdmission(entry.Scope, entry.WorkspaceRoot)
			if admissionErr != nil {
				continue
			}
			a.mu.Lock()
			id := a.restoredTabIDLocked(entry.ID)
			a.mu.Unlock()

			var tab *WorkspaceTab
			if entry.Scope == "project" {
				tab = a.createTabEntryWithID(entry.Scope, entry.WorkspaceRoot, entry.TopicID, id)
			} else {
				tab = a.createTabEntryWithID("global", globalTabWorkspaceRoot(), entry.TopicID, id)
			}
			tab.model = entry.Model
			tab.SessionWorkspace.ID = restoredWorkspaceID(entry)
			tab.effort = cloneStringPtr(entry.Effort)
			tab.mode = persistedTabMode(entry.Mode)
			restoreRuntime := prepareRestoredTabIdentity(tab, entry)
			if id := strings.TrimSpace(entry.SessionID); id != "" {
				tab.toolApprovalMode = a.sessionPresets.restore(id, tab.toolApprovalMode)
			} else {
				tab.toolApprovalMode = normalizeToolApprovalMode(entry.ToolApprovalMode)

			}
			tab.SessionID = strings.TrimSpace(entry.SessionID)
			tab.PendingCreateOperationID = strings.TrimSpace(entry.CreateOperationID)
			tab.persistenceExtra = cloneDesktopJSONFields(entry.extra)
			tab.ReadOnly = entry.ReadOnly
			tab.Takeover.Spectator = entry.TakeoverSpectator
			tab.sink = &tabEventSink{tabID: tab.ID, app: a, ctx: ctx}
			a.publishRestoredTab(tab, releaseAdmission)
			if restoreRuntime {
				toBuild = append(toBuild, tab)
			}
		}
		a.finishRestoredLocalTabs(f, toBuild)
		return
	}
	if len(f.RemoteTabs) > 0 {
		// Remote-only layout: the remote shell is the visible surface, but local
		// commands still need a workspace tab to target.
		a.restoreDormantWorkspaceTab(ctx)
		return
	}

	// First launch intentionally has no runtime. The renderer opens a persisted
	// Global draft after this restore gate closes; the first execution creates
	// the canonical Session and Controller.
}

func (a *App) loadStartupLocale() {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	lang := cfg.DesktopLanguage()
	if lang == "" {
		lang = cfg.Language
	}
	a.setDesktopLocale(i18n.DetectLanguage(lang))
}

func (a *App) createTabEntry(scope, workspaceRoot, topicID string) *WorkspaceTab {
	return a.createTabEntryWithID(scope, workspaceRoot, topicID, newTabID())
}

func desktopNewSessionDefaults(scope, workspaceRoot string) (string, string) {
	userCfg := config.LoadForEdit(config.UserConfigPath())
	modelCfg := userCfg
	if strings.TrimSpace(scope) == "project" && strings.TrimSpace(workspaceRoot) != "" {
		if cfg, err := config.LoadForRootReadOnly(workspaceRoot); err == nil {
			modelCfg = cfg
		}
	}
	return resolveNewSessionModel(modelCfg), newSessionPreset(userCfg)
}

func newSessionPreset(userCfg *config.Config) string {
	return normalizeToolApprovalMode(userCfg.DesktopDefaultToolApprovalMode())
}

// resolveNewSessionModel picks the model a fresh session starts on. A
// default_model that resolves but has no API key in the current environment
// would boot every new tab straight into the missing-key notice, so fall
// through to the first provider that is actually configured, mirroring the
// Configured() gate in Config.ResolveModelWithFallback's fallback chain. An
// allowed chat default is preserved when every eligible provider is keyless so
// the existing missing-key notice still tells the user what to fix. When no
// desktop-accessible chat model exists, the empty result lets tab startup show
// an actionable setup error instead of re-admitting an ineligible default.
func resolveNewSessionModel(cfg *config.Config) string {
	if resolved, _, ok := cfg.ResolveDesktopNewSessionModel(); ok {
		// Keep provider identity explicit at the new-session boundary. A bare
		// model id is ambiguous when two configured gateways expose the same
		// model, and a provider-only ref otherwise compares unequal to the
		// canonical ref stored on a running tab.
		if entry, found := cfg.ResolveModel(resolved); found {
			return entry.Name + "/" + entry.Model
		}
		return resolved
	}
	return ""
}

func (a *App) createTabEntryWithID(scope, workspaceRoot, topicID, id string) *WorkspaceTab {
	model, toolApprovalMode := desktopNewSessionDefaults(scope, workspaceRoot)
	return &WorkspaceTab{
		ID:               id,
		Scope:            scope,
		WorkspaceRoot:    workspaceRoot,
		SessionWorkspace: desktopTabWorkspace{ID: desktopWorkspaceID(scope, workspaceRoot)},
		TopicID:          topicID,
		TopicTitle:       a.localizedDefaultTopicTitle(),
		topicTitleSource: topicTitleSourceAuto,
		model:            model,
		mode:             tabModeForPlan(false),
		toolApprovalMode: toolApprovalMode,
		disabledMCP:      map[string]ServerView{},
	}
}

func (a *App) snapshotAllTabs() {
	a.mu.RLock()
	tabs := a.runtimeTabsLocked()
	a.mu.RUnlock()
	for _, t := range tabs {
		if err := a.snapshotTab(t); err != nil {
			slog.Warn("desktop: snapshot all tabs failed", "tab", t.ID, "err", err)
		}
	}
}

// shutdown snapshots all tabs, saves the final window geometry, and closes tabs.
func (a *App) shutdown(ctx context.Context) {
	_, _ = a.requestShutdown(ctx, shutdownRequest{
		RequestID: newDesktopLifecycleRunID(),
		Reason:    shutdownReasonUserQuit,
	})
}

// domReady is called (via the shell's DOMReady hook) after the renderer
// finishes loading its DOM but before the hidden window is presented. It
// restores saved geometry, then delegates presentation to the shell
// coordinator.
func (a *App) domReady(_ context.Context) {
	if a.desktopShell.coordinator != nil {
		a.desktopShell.coordinator.markDOMReady()
	}

	a.restoreWindowGeometry()
	a.showMainWindowFrom("startup_dom_ready")
}

func (a *App) completeFrontendStartup() {
	a.markDesktopHealthy()
	ctx := a.ctx
	a.goSafe("recordHealthyConfig", func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		if err := a.commitPendingUpdateHealth(); err != nil {
			slog.Warn("desktop: commit healthy update", "err", err)
		}
		if err := repair.RecordHealthyConfig(version); err != nil {
			slog.Debug("desktop: record last-known-good config", "err", err)
		}
	})
}

// ReportDesktopWebViewReady is the content-process heartbeat. DOMReady proves
// navigation completed; this bound call additionally proves that React and the
// host bridge are responsive after a renderer reload.
func (a *App) ReportDesktopWebViewReady() {
	if a == nil || a.shuttingDown.Load() || a.forceQuit.Load() {
		return
	}
	if a.desktopShell.coordinator != nil {
		first, healthy := a.desktopShell.coordinator.markFrontendHeartbeat(time.Now())
		if first {
			a.goSafe("startDesktopTrayAfterFrontendReady", func() { a.startTray() })
		}
		if healthy {
			a.completeFrontendStartup()
		}
	}
}

func (a *App) commitPendingUpdateHealth() error {
	if a == nil || strings.TrimSpace(a.healthyUpdateCreatedAt) == "" ||
		strings.TrimSpace(a.healthyUpdateTransactionID) == "" {
		return nil
	}
	return markPendingUpdateHealthyAfterReady(
		version,
		a.healthyUpdateCreatedAt,
		a.healthyUpdateTransactionID,
	)
}

// bound command surface (frontend → controller)
// Each method guards on a nil controller so a pre-startup or failed-build call is
// a no-op, never a panic.

var errEmptyTurnInput = errors.New("message cannot be empty")

func validateTurnInput(input string) error {
	if strings.TrimSpace(input) == "" {
		return errEmptyTurnInput
	}
	return nil
}

// submitToTab is the shared submit body.
func (a *App) submitToTab(tabID, input string, submissionID ...string) error {
	_, err := a.submitToTabResult(tabID, input, false, submissionID...)
	return err
}

func (a *App) submitUserTurnToTab(tabID, input string) bool {
	admission, ctrl, err := a.beginTabTurn(tabID, true)
	if err != nil {
		return false
	}
	defer admission.abort()
	tab := admission.tab
	if err := a.ensureTabTopicIndexedForUserTurn(tab); err != nil {
		return false
	}
	ctrl.SubmitUserTurn(input, input)
	return admission.finish(ctrl)
}

func (a *App) RunShellForTab(tabID, command string) error {
	admission, ctrl, err := a.beginTabTurn(tabID, true)
	if err != nil {
		return err
	}
	defer admission.abort()
	tab := admission.tab
	if err := a.ensureTabTopicIndexedForUserTurn(tab); err != nil {
		return err
	}
	ctrl.RunShell(command)
	admission.finish(ctrl)
	return nil
}

// InvocationRequest is the Wails-bound form of a composer invocation entity.
type InvocationRequest struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Offset int    `json:"offset"`
}

func controlInvocationRequests(invocations []InvocationRequest) []control.InvocationRequest {
	out := make([]control.InvocationRequest, 0, len(invocations))
	for _, invocation := range invocations {
		out = append(out, control.InvocationRequest{
			Name: invocation.Name, Kind: invocation.Kind, Offset: invocation.Offset,
		})
	}
	return out
}

func validateInvocationTurnInput(input string, invocations []InvocationRequest) error {
	// A skill-only turn legitimately has no explicit task: the resolved
	// invocation content becomes the provider input. Without an invocation,
	// keep the same empty-input protection as every other submit path.
	if len(invocations) > 0 {
		return nil
	}
	return validateTurnInput(input)
}

func (a *App) submitInitialGoalToLocalTab(
	tabID, toolApprovalMode, goal, display, input string,
	invocations []InvocationRequest,
	submissionID ...string,
) ([]string, error) {
	req := control.SubmissionRequest{ID: firstSubmissionID(submissionID), Input: input, Display: display,
		Goal: strings.TrimSpace(goal), ToolApprovalMode: normalizeToolApprovalMode(toolApprovalMode), Invocations: controlInvocationRequests(invocations)}
	if found, err := a.knownSubmission(tabID, req); found || err != nil {
		return []string{}, err
	}
	admission, ctrl, err := a.beginTabTurn(tabID, true, submissionID...)
	if err != nil {
		return []string{}, a.submissionAdmissionError(tabID, req, err)
	}
	defer admission.abort()

	tab := admission.tab
	toolApprovalMode = normalizeToolApprovalMode(toolApprovalMode)
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return []string{}, fmt.Errorf("goal is required")
	}
	var drained []string
	setup := func() error {
		if err := syncTabGoalToController(ctrl, goal); err != nil {
			return fmt.Errorf("activate goal: %w", err)
		}
		a.mu.Lock()
		if a.tabs[tab.ID] != tab {
			a.mu.Unlock()
			return a.workspaceNotReadyErr(nil)
		}
		tab.toolApprovalMode = toolApprovalMode
		tab.goal = goal
		tab.mode = tabModeForPlan(false)
		a.saveTabsLocked()
		a.mu.Unlock()

		ctrl.SetPlanMode(false)
		drained = applyTabToolApprovalModeToController(ctrl, toolApprovalMode)
		return a.ensureTabTopicIndexedForUserTurn(tab)
	}
	if err := submitIdentifiedWithSetup(ctrl, req, setup, func() {
		if len(invocations) > 0 {
			ctrl.SubmitInvocationDisplay(display, input, controlInvocationRequests(invocations))
		} else {
			ctrl.SubmitDisplay(display, input)
		}
	}); err != nil {
		return []string{}, err
	}
	admission.finish(ctrl)
	return drained, nil
}

// Cancel aborts the in-flight turn.
func (a *App) Cancel() {
	a.CancelTab("")
}

func (a *App) CancelTab(tabID string) {
	if ctrl := a.ctrlByTabID(tabID); ctrl != nil {
		ctrl.Cancel()
	}
}

// Steer sends mid-turn guidance to the agent without interrupting the in-flight request.
func (a *App) Steer(text string) error {
	return a.SteerForTab("", text)
}

// SteerForTab sends mid-turn guidance to a specific tab's active agent turn.
// A rejected steer is returned to the frontend so its guidance shelf retains
// the text and submits it as a regular follow-up after the turn completes.
func (a *App) SteerForTab(tabID, text string) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	if err := a.validateTabControllerWorkspace(tab); err != nil {
		return err
	}
	ctrl = a.controllerForTab(tab)
	if ctrl == nil {
		return a.workspaceNotReadyErr(tab)
	}
	steerer, ok := ctrl.(interface{ TrySteer(string) bool })
	if !ok {
		return fmt.Errorf("this runtime cannot accept mid-turn guidance")
	}
	if !steerer.TrySteer(text) {
		return fmt.Errorf("the turn ended before guidance could be applied; it will remain queued for the next turn")
	}
	return nil
}

func (a *App) tabAndCtrlByID(tabID string) (*WorkspaceTab, control.SessionAPI) {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return nil, nil
	}
	ctrl := tab.Ctrl
	retryStartup := ctrl == nil && (tab.StartupErrLeaseHeld || tab.modelApplication.startupRetry)
	a.mu.RUnlock()
	if retryStartup && a.tryRecoverStartupLeaseHeldTab(tab) {
		a.mu.RLock()
		defer a.mu.RUnlock()
		if a.tabs[tab.ID] != tab {
			return nil, nil
		}
		return tab, tab.Ctrl
	}
	return tab, ctrl
}

// activeTabAndCtrl snapshots the active tab and its controller in one locked
// read, so callers never do a check-then-use on tab.Ctrl after the lock is
// released (a rebuild can swap the controller in between).
func (a *App) activeTabAndCtrl() (*WorkspaceTab, control.SessionAPI) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.activeTabLocked()
	if tab == nil {
		return nil, nil
	}
	return tab, tab.Ctrl
}

// activeMCPRuntime snapshots the complete target of a Wails MCP action in one
// critical section. MCP operations may outlive a frontend tab switch; carrying
// the invoking workspace root prevents config/authorization reads from drifting to the
// newly active tab while controller calls still target the original runtime.
// mcpAppsSandboxAvailable reports whether Desktop may declare the Apps
// capability profile for newly acquired shared hosts.
func (a *App) mcpAppsSandboxAvailable() bool { return a.mcpAppsSandbox.available() }

func (a *App) activeMCPRuntime() (*WorkspaceTab, control.SessionAPI, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.activeTabLocked()
	if tab == nil {
		return nil, nil, ""
	}
	return tab, tab.Ctrl, tab.WorkspaceRoot
}

func (a *App) controllerForTab(tab *WorkspaceTab) control.SessionAPI {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if tab.ID != "" && !a.ownsRuntimeTabLocked(tab) {
		return nil
	}
	return tab.Ctrl
}

// currentSessionPathFor is the locked form of tab.currentSessionPath: it
// snapshots Ctrl/SessionPath under a.mu, then queries the controller off-lock.
// Use it on paths that do not otherwise hold a.mu.
func (a *App) currentSessionPathFor(tab *WorkspaceTab) string {
	if tab == nil {
		return ""
	}
	a.mu.RLock()
	ctrl := tab.Ctrl
	sessionID := strings.TrimSpace(tab.SessionID)
	a.mu.RUnlock()
	if identity, ok := ctrl.(control.IdentityLifecycle); ok && identity.UsesExclusiveSession() {
		if ref, bound := identity.SessionRef(); bound {
			return sessionRoute(ref.SessionID)
		}
	}
	if sessionID != "" {
		return sessionRoute(sessionID)
	}
	return ""
}

// sessionDirForSnapshot mirrors tabSessionDir for callers that hold a
// tabRuntimeSnapshot instead of reading the live tab.
func sessionDirForSnapshot(s tabRuntimeSnapshot) string {
	if s.workspaceRoot != "" {
		return desktopSessionDir(s.workspaceRoot)
	}
	if s.ctrl != nil {
		if dir := s.ctrl.SessionDir(); dir != "" {
			return dir
		}
	}
	return desktopSessionDir("")
}

func readOnlyChannelErr() error {
	return fmt.Errorf("channel session is read-only")
}

func (a *App) snapshotTab(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	readOnly := tab.ReadOnly
	ctrl := tab.Ctrl
	identityErr := validateTabSessionIdentity(tab.SessionID)
	a.mu.RUnlock()
	if identityErr != nil {
		return identityErr
	}
	if readOnly || ctrl == nil {
		return nil
	}
	return ctrl.Snapshot()
}

func (a *App) snapshotTabForAction(tab *WorkspaceTab, action string) error {
	if err := a.snapshotTab(tab); err != nil {
		a.reportTabSnapshotError(tab, action, err)
		if strings.TrimSpace(action) == "" {
			return fmt.Errorf("save current session: %w", err)
		}
		return fmt.Errorf("save current session before %s: %w", action, err)
	}
	return nil
}

func (a *App) reportTabSnapshotError(tab *WorkspaceTab, action string, err error) {
	if err == nil {
		return
	}
	tabID := ""
	if tab != nil {
		tabID = tab.ID
	}
	slog.Warn("desktop: session snapshot failed", "tab", tabID, "action", action, "err", err)
	if tab == nil || tab.sink == nil {
		return
	}
	// Autosave fires once per turn; on a persistently failing disk that would
	// stream a chat warning after every turn. Rate-limit the user-facing
	// notice per tab (the slog line above always records every failure). Saves
	// triggered by an explicit action are one-shot and always surface.
	if action == "autosave" {
		tab.saveMu.Lock()
		now := time.Now()
		if !tab.lastAutosaveWarnAt.IsZero() && now.Sub(tab.lastAutosaveWarnAt) < autosaveWarnInterval {
			tab.saveMu.Unlock()
			return
		}
		tab.lastAutosaveWarnAt = now
		tab.saveMu.Unlock()
	}
	prefix := "Session autosave failed"
	if strings.TrimSpace(action) != "" && action != "autosave" {
		prefix = "Session save failed before " + action
	}
	tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: prefix + ": " + err.Error()})
}

// validateTabControllerWorkspace rejects a stale surface binding without
// rewriting the session, replacing its controller or opening another workspace.
func (a *App) validateTabControllerWorkspace(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	current, ctrl, readOnly, desiredRoot := a.tabs[tab.ID], tab.Ctrl, tab.ReadOnly, strings.TrimSpace(tab.WorkspaceRoot)
	a.mu.RUnlock()
	if current != tab || ctrl == nil || readOnly {
		return nil
	}
	root, ok := safeControllerWorkspaceRoot(ctrl)
	if ok && desiredRoot != "" && strings.TrimSpace(root) != "" && !sameDesktopPath(root, desiredRoot) {
		return errSessionWorkspaceConflict
	}
	return nil
}

func safeControllerWorkspaceRoot(ctrl control.SessionAPI) (root string, ok bool) {
	if ctrl == nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			root = ""
			ok = false
		}
	}()
	return ctrl.WorkspaceRoot(), true
}

func safeControllerSessionDir(ctrl control.SessionAPI) (dir string, ok bool) {
	if ctrl == nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			dir = ""
			ok = false
		}
	}()
	return ctrl.SessionDir(), true
}

// ReplayPendingPromptsForTab re-emits only the prompt owned by tabID. Tab
// switches use this scoped form so a background session's ask/approval cannot
// depend on whichever tab happens to be backend-active when the replay RPC
// arrives.
func (a *App) ReplayPendingPromptsForTab(tabID string) {
	ctrl := a.ctrlByTabID(tabID)
	if ctrl != nil {
		ctrl.ReplayPendingPrompts()
	}
}

// SetModeForTab returns the pending approval prompt ids the switch
// auto-allowed, so the frontend dismisses exactly those cards and keeps the
// ones the backend still holds (plan/memory/sandbox-escape never drain, and
// auto keeps approvals an allow policy would not cover — #6432).
func (a *App) SetModeForTab(tabID, mode string) []string {
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	normalized := normalizeTabMode(mode)
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return nil
	}
	tab.mode = normalized
	tab.toolApprovalMode = normalizeToolApprovalMode(tab.toolApprovalMode)

	ctrl := tab.Ctrl
	approvalMode := tab.toolApprovalMode
	tabIDForSave := tab.ID
	a.mu.Unlock()
	drained := applyTabModeToController(ctrl, normalized)
	drained = append(drained, applyTabToolApprovalModeToController(ctrl, approvalMode)...)
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return drained
}

// modeApplier / toolApprovalApplier are the drained-id-reporting variants of
// SessionAPI's SetMode / SetToolApprovalMode. Asserted optionally so test
// fakes implementing the plain SessionAPI keep compiling (they report nil).

type toolApprovalApplier interface {
	ApplyToolApprovalMode(mode string) []string
}

func applyTabToolApprovalModeToController(ctrl control.SessionAPI, mode string) []string {
	if ctrl == nil {
		return nil
	}
	mode = normalizeToolApprovalMode(mode)
	if applier, ok := ctrl.(toolApprovalApplier); ok {
		return applier.ApplyToolApprovalMode(mode)
	}
	ctrl.SetToolApprovalMode(mode)
	return nil
}

func normalizeCollaborationMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return "plan"
	case "goal":
		return "goal"
	default:
		return "normal"
	}
}

// SetComposerProfileForTab applies the controller-facing profile axes under one
// turn gate. Frontends use this before submit and after controller rebuilds so a
// turn cannot observe collaboration, approval, and goal from different UI
// generations.
func (a *App) SetComposerProfileForTab(tabID, collaborationMode, toolApprovalMode, goal string) ([]string, error) {
	if a.isRemoteTab(tabID) {
		return []string{}, nil
	}
	collaborationMode = normalizeCollaborationMode(collaborationMode)
	toolApprovalMode = normalizeToolApprovalMode(toolApprovalMode)
	goal = strings.TrimSpace(goal)

	tab := a.tabByID(tabID)
	if tab == nil {
		return []string{}, fmt.Errorf("tab is no longer available")
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()

	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return []string{}, fmt.Errorf("tab is no longer available")
	}
	ctrl := tab.Ctrl
	tabIDForSave := tab.ID
	a.mu.Unlock()

	plan := collaborationMode == "plan" && goal == ""
	var drained []string
	if concrete, ok := ctrl.(*control.Controller); ok && concrete != nil {
		var err error
		drained, err = concrete.ApplyComposerProfileAt(plan, toolApprovalMode, goal, concrete.PermissionSnapshot().Revision)
		if err != nil {
			return []string{}, err
		}
	} else {
		if ctrl != nil {
			ctrl.SetPlanMode(plan)
		}
		drained = applyTabToolApprovalModeToController(ctrl, toolApprovalMode)
		if err := syncTabGoalToController(ctrl, goal); err != nil {
			return []string{}, err
		}
	}

	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		tab.toolApprovalMode = toolApprovalMode
		tab.goal = goal
		tab.mode = tabModeForPlan(plan)
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if drained == nil {
		return []string{}, nil
	}
	return drained, nil
}

func (a *App) SetCollaborationModeForTab(tabID, mode string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		return a.workspaceNotReadyErr(nil)
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	mode = normalizeCollaborationMode(mode)
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return a.workspaceNotReadyErr(nil)
	}
	nextGoal := tab.goal
	nextMode := tabModeForPlan(false)
	if mode == "plan" {
		nextMode = tabModeForPlan(true)
		nextGoal = ""
	} else if mode != "goal" {
		nextGoal = ""
	}
	ctrl := tab.Ctrl
	plan := tabModeHasPlan(nextMode)
	tabIDForSave := tab.ID
	a.mu.Unlock()
	if ctrl != nil {
		if err := syncTabGoalToController(ctrl, nextGoal); err != nil {
			return err
		}
		ctrl.SetPlanMode(plan)
	}
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		tab.mode = nextMode
		tab.goal = nextGoal
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return nil
}

// QuestionAnswer is the frontend's reply to one question in an ask_request.
type QuestionAnswer struct {
	QuestionID string   `json:"questionId"`
	Selected   []string `json:"selected"`
}

// CompactForTab compacts the requested tab without depending on which tab is
// focused when the asynchronous frontend call reaches the backend.
func (a *App) CompactForTab(tabID string) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	if err := a.validateTabControllerWorkspace(tab); err != nil {
		return err
	}
	ctrl = a.controllerForTab(tab)
	if ctrl == nil {
		return nil
	}
	return ctrl.Compact(a.ctx, "")
}

// workspaceNotReadyErr names why a session action arrived before the tab's
// controller existed: still starting, or failed to start. Silently returning
// nil here swallowed the click with no feedback (#3938).
//
// This is the bound-method form: StartupErr is written under a.mu by the
// build goroutine while Submit-family calls race it, so read it under the
// lock. Callers must not hold a.mu.
func (a *App) workspaceNotReadyErr(tab *WorkspaceTab) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workspaceNotReadyErrLocked(tab)
}

func (a *App) workspaceNotReadyErrLocked(tab *WorkspaceTab) error {
	startupErr := ""
	var issue *SessionRuntimeIssue
	if tab != nil {
		startupErr = tab.StartupErr
		issue = a.sessionRuntimeViewLocked(tab).Issue
	}
	if strings.TrimSpace(startupErr) != "" {
		return fmt.Errorf("workspace failed to start: %s", startupErr)
	}
	if issue != nil && strings.TrimSpace(issue.Message) != "" {
		return fmt.Errorf("workspace failed to start: %s", issue.Message)
	}
	return fmt.Errorf("workspace is still starting")
}

// tabIsReadOnly reads tab.ReadOnly under a.mu; setTabReadOnly can flip it
// concurrently with Submit-family bound calls. Callers must not hold a.mu.
func (a *App) tabIsReadOnly(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return tab.ReadOnly
}

// applyNewSessionDefaultModel makes a freshly rotated or reused blank session
// obey the same default as EnsureBlankTab. Existing conversations keep their
// saved model until the user starts a new one.
func (a *App) applyNewSessionDefaultModel(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	scope := tab.Scope
	root := tab.WorkspaceRoot
	a.mu.RUnlock()
	if strings.TrimSpace(scope) != "project" {
		scope = "global"
		root = ""
	}
	defaultModel, _ := desktopNewSessionDefaults(scope, root)
	return a.alignReusableBlankTabModel(tab, defaultModel)
}

func (a *App) assignFreshSessionTopic(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	topicID := newTopicID()
	a.mu.Lock()
	sessionID := tab.SessionID
	tab.TopicID = topicID
	tab.TopicTitle = defaultTopicTitle
	tab.topicTitleSource = topicTitleSourceAuto
	if current := a.tabs[tab.ID]; current == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if err := a.workspaceRegistry().EnsureSessionTopic(a.bootContext(), sessionID, topicID, ""); err != nil {
		return err
	}
	return nil
}

func (a *App) ensureTabTopicIndexedForUserTurn(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	topicID := newTopicID()
	a.mu.Lock()
	if strings.TrimSpace(tab.TopicID) != "" {
		a.mu.Unlock()
		return a.persistCanonicalTopicForTab(tab)
	}
	tab.TopicID = topicID
	tab.TopicTitle = defaultTopicTitle
	tab.topicTitleSource = topicTitleSourceAuto
	if current := a.tabs[tab.ID]; current == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if err := a.persistCanonicalTopicForTab(tab); err != nil {
		return err
	}
	path := a.currentSessionPathFor(tab)
	a.rememberTabSessionPath(tab, path)
	a.emitProjectTreeChangedEvent()
	return nil
}

func (a *App) persistCanonicalTopicForTab(tab *WorkspaceTab) error {
	a.mu.RLock()
	sessionID, workspaceID, topicID := tab.SessionID, tab.SessionWorkspace.ID, tab.TopicID
	title := canonicalSeedTitle(tab.TopicTitle, tab.topicTitleSource)
	a.mu.RUnlock()
	if workspaceID == "" {
		return nil
	}
	return a.workspaceRegistry().EnsureSessionTopic(a.bootContext(), sessionID, topicID, title)
}

func messagesHaveConversationContent(messages []provider.Message) bool {
	for _, msg := range messages {
		if msg.Role != provider.RoleSystem {
			return true
		}
	}
	return false
}

func (a *App) clearActiveSessionRuntime(tab *WorkspaceTab, oldCtrl control.SessionAPI) (SessionClearResult, error) {
	if tab == nil || oldCtrl == nil {
		return SessionClearResult{}, fmt.Errorf("workspace is still starting")
	}
	unlockRuntime := a.lockRuntimeMutation("clear active session")
	defer unlockRuntime()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()

	a.mu.RLock()
	current := a.tabs[tab.ID] == tab && tab.Ctrl == oldCtrl
	a.mu.RUnlock()
	if !current {
		return SessionClearResult{}, fmt.Errorf("workspace changed while clearing the session")
	}
	_, runtime, exclusive := exclusiveSessionBinding(oldCtrl)
	if !exclusive {
		return SessionClearResult{}, fmt.Errorf("clearing requires a current session controller")
	}
	if oldCtrl.RuntimeStateSnapshot().Cancellable {
		oldCtrl.Cancel()
		if err := waitControllerStopped(oldCtrl); err != nil {
			return SessionClearResult{}, err
		}
	}
	// Cancel only the captured identity's background work. Do not rotate while
	// a process can still mutate that session's state; a timeout leaves its
	// identity bound, and the destroy gate stays held until the job exits.
	destroy := oldCtrl.BeginDestroySession(runtime.Ref().SessionID)
	if result := destroy.WaitFor(-1); result.HasTimedOut() {
		go func() {
			destroy.WaitAll()
			destroy.Finish()
		}()
		return SessionClearResult{}, fmt.Errorf("timed out waiting for session jobs to stop")
	}
	defer destroy.Finish()
	if err := oldCtrl.ClearSession(); err != nil {
		a.syncTabSessionIdentity(tab, oldCtrl)
		return SessionClearResult{}, err
	}
	a.syncTabSessionIdentity(tab, oldCtrl)
	tab.setPinnedFiles(nil)
	a.clearTabGoal(tab)
	tab.resetTelemetry(tab.currentSessionIdentity())
	a.mu.Lock()
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	a.invalidatePromptHistoryCache()
	a.notifyTabRuntimeRebuilt(tab)
	return a.bumpAndSnapshotSessionClear(tab), nil
}

// CheckpointMeta summarises one rewind point (a user turn) for the desktop.
// Optional v2 fields use omitempty so older frontends keep reading the rest.
type CheckpointMeta struct {
	Turn               int      `json:"turn"`
	Prompt             string   `json:"prompt"`
	Files              []string `json:"files"`     // stable preview of cumulative files RestoreCode would affect from this turn
	FileCount          int      `json:"fileCount"` // full cumulative file count, including entries omitted from Files
	FilesTruncated     bool     `json:"filesTruncated,omitempty"`
	TurnFileCount      int      `json:"turnFileCount"` // files changed during this turn only
	Time               int64    `json:"time"`          // unix milliseconds
	CanCode            bool     `json:"canCode"`
	CanConversation    bool     `json:"canConversation"`
	Coverage           string   `json:"coverage,omitempty"`
	CoverageGaps       []string `json:"coverageGaps,omitempty"`
	ExpiredFilePayload bool     `json:"expiredFilePayload,omitempty"`
	ActiveWriters      int      `json:"activeWriters,omitempty"`
	CanUndoFiles       bool     `json:"canUndoFiles,omitempty"`
	DisabledReason     string   `json:"disabledReason,omitempty"`
}

// RewindPlanView is the desktop-facing prepare result.
type RewindPlanView struct {
	PlanID             string   `json:"planId"`
	Turn               int      `json:"turn"`
	Scope              string   `json:"scope"`
	Coverage           string   `json:"coverage,omitempty"`
	CoverageGaps       []string `json:"coverageGaps,omitempty"`
	ExpiredFilePayload bool     `json:"expiredFilePayload,omitempty"`
	CanFiles           bool     `json:"canFiles"`
	CanConversation    bool     `json:"canConversation"`
	DisabledReason     string   `json:"disabledReason,omitempty"`
	Conflicts          []string `json:"conflicts,omitempty"`
	Files              []string `json:"files,omitempty"`
	FileCount          int      `json:"fileCount"`
	ActiveWriters      int      `json:"activeWriters,omitempty"`
	Path               string   `json:"path,omitempty"`
	ConversationAction string   `json:"conversationAction,omitempty"`
	OK                 bool     `json:"ok"`
	Error              string   `json:"error,omitempty"`
}

// RewindResultView is the desktop-facing commit/undo result.
type RewindResultView struct {
	OK                 bool     `json:"ok"`
	TransactionID      string   `json:"transactionId,omitempty"`
	UndoAvailable      bool     `json:"undoAvailable"`
	Written            []string `json:"written,omitempty"`
	Deleted            []string `json:"deleted,omitempty"`
	ConversationOK     bool     `json:"conversationOk,omitempty"`
	ConversationForked bool     `json:"conversationForked,omitempty"`
	OperationID        string   `json:"operationId,omitempty"`
	Branch             string   `json:"branch,omitempty"`
	Partial            bool     `json:"partial,omitempty"`
	TabID              string   `json:"tabId,omitempty"`
	Tab                *TabMeta `json:"tab,omitempty"`
	Error              string   `json:"error,omitempty"`
	Conflicts          []string `json:"conflicts,omitempty"`
	Coverage           string   `json:"coverage,omitempty"`
}

const checkpointFilePreviewLimit = 60

func (a *App) CheckpointsForTab(tabID string) []CheckpointMeta {
	a.mu.RLock()
	var ctrl control.SessionAPI
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return []CheckpointMeta{}
	}
	metas := ctrl.Checkpoints()
	out := make([]CheckpointMeta, 0, len(metas))
	for _, m := range metas {
		gaps := make([]string, 0, len(m.CoverageGaps))
		for _, g := range m.CoverageGaps {
			if g.Detail != "" {
				gaps = append(gaps, g.Reason+": "+g.Detail)
			} else {
				gaps = append(gaps, g.Reason)
			}
		}
		cov := string(m.Coverage)
		meta := CheckpointMeta{
			Turn:               m.Turn,
			Prompt:             m.Prompt,
			Files:              m.Paths,
			TurnFileCount:      len(m.Paths),
			Time:               m.Time.UnixMilli(),
			CanCode:            len(m.Paths) > 0 && m.CanUndoFiles,
			CanConversation:    ctrl.CheckpointHasBoundary(m.Turn),
			Coverage:           cov,
			CoverageGaps:       gaps,
			ExpiredFilePayload: m.ExpiredFilePayload,
			ActiveWriters:      len(m.ActiveWriters),
			CanUndoFiles:       m.CanUndoFiles,
			DisabledReason:     m.DisabledReason,
		}
		out = append(out, meta)
	}
	// RestoreCode(turn) reverts every file touched in this turn or any later one, so
	// a turn can rewind code even when it changed no files itself — as long as a
	// later turn did. Propagate CanCode backwards over the oldest-first list.
	// Also propagate the cumulative unique file count so the UI shows how many
	// files RestoreCode would actually affect from this turn.
	hasCodeAfter := false
	canCodeAfter := true
	codeFileSet := make(map[string]bool, len(metas)*2)
	codeFilePreview := []string{}
	//nolint:modernize // slices.Backward yields element copies; this body writes through the index.
	for i := len(out) - 1; i >= 0; i-- {
		if len(out[i].Files) > 0 {
			hasCodeAfter = true
			if !out[i].CanUndoFiles {
				canCodeAfter = false
			}
		}
		for _, f := range out[i].Files {
			if codeFileSet[f] {
				continue
			}
			codeFileSet[f] = true
			codeFilePreview = insertCheckpointFilePreview(codeFilePreview, f, checkpointFilePreviewLimit)
		}
		out[i].CanCode = hasCodeAfter && canCodeAfter
		out[i].FileCount = len(codeFileSet)
		out[i].Files = append([]string{}, codeFilePreview...)
		out[i].FilesTruncated = out[i].FileCount > len(out[i].Files)
	}
	return out
}

func insertCheckpointFilePreview(preview []string, path string, limit int) []string {
	if limit <= 0 || path == "" {
		return preview
	}
	idx := sort.SearchStrings(preview, path)
	if idx < len(preview) && preview[idx] == path {
		return preview
	}
	if len(preview) < limit {
		preview = append(preview, "")
		copy(preview[idx+1:], preview[idx:])
		preview[idx] = path
		return preview
	}
	if idx >= limit {
		return preview
	}
	copy(preview[idx+1:], preview[idx:limit-1])
	preview[idx] = path
	return preview
}

// ToolResultForTab returns the full arguments and output for one tool call that
// were elided from the frontend's in-memory items[] for memory efficiency. The
// caller (frontend ToolCard) loads this on demand when the user expands a
// collapsed tool card. Returns nil when the tool ID is not found.
func (a *App) ToolResultForTab(tabID, toolID string) *control.ToolResultData {
	a.mu.RLock()
	var ctrl control.SessionAPI
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ctrl = tab.Ctrl
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return nil
	}
	return ctrl.ToolResult(toolID)
}

// PreviewRewindForTab returns a structured precheck without mutating state.
func (a *App) PreviewRewindForTab(tabID string, turn int, scope string) RewindPlanView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return RewindPlanView{OK: false, Error: readOnlyChannelErr().Error()}
	}
	if ctrl == nil {
		return RewindPlanView{OK: false, Error: "no controller"}
	}
	s := control.RewindBoth
	switch scope {
	case "code":
		s = control.RewindCode
	case "conversation":
		s = control.RewindConversation
	}
	plan, err := ctrl.PrepareRewind(turn, s)
	view := rewindPlanToView(plan, scope)
	if err != nil {
		view.OK = false
		view.Error = err.Error()
		return view
	}
	view.OK = true
	return view
}

// PreviewWorkspaceFileRevertForTab prepares a single-file session-owned revert.
func (a *App) PreviewWorkspaceFileRevertForTab(tabID, path string) RewindPlanView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return RewindPlanView{OK: false, Error: readOnlyChannelErr().Error(), Path: path}
	}
	if ctrl == nil {
		return RewindPlanView{OK: false, Error: "no controller", Path: path}
	}
	plan, err := ctrl.PrepareFileRevert(path)
	view := rewindPlanToView(plan, "code")
	view.Path = path
	if err != nil {
		view.OK = false
		view.Error = err.Error()
		return view
	}
	view.OK = plan.CanFiles || len(plan.Conflicts) > 0
	return view
}

// CommitWorkspaceFileRevertForTab commits a single-file revert.
// resolution is "keep_current" or "overwrite_checkpoint".
func (a *App) CommitWorkspaceFileRevertForTab(tabID, planID, resolution string) RewindResultView {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return RewindResultView{OK: false, Error: readOnlyChannelErr().Error()}
	}
	if ctrl == nil {
		return RewindResultView{OK: false, Error: "no controller"}
	}
	res := checkpoint.ConflictResolution("")
	switch resolution {
	case "keep_current":
		res = checkpoint.ResolveKeepCurrent
	case "overwrite_checkpoint":
		res = checkpoint.ResolveOverwriteCheckpoint
	}
	result, err := ctrl.CommitFileRevert(planID, res)
	view := rewindResultToView(result)
	if err != nil {
		view.OK = false
		if view.Error == "" {
			view.Error = err.Error()
		}
	}
	return view
}

func rewindPlanToView(plan checkpoint.RewindPlan, scope string) RewindPlanView {
	gaps := make([]string, 0, len(plan.CoverageGaps))
	for _, g := range plan.CoverageGaps {
		if g.Detail != "" {
			gaps = append(gaps, g.Reason+": "+g.Detail)
		} else {
			gaps = append(gaps, g.Reason)
		}
	}
	return RewindPlanView{
		PlanID:             plan.PlanID,
		Turn:               plan.Turn,
		Scope:              scope,
		Coverage:           string(plan.Coverage),
		CoverageGaps:       gaps,
		ExpiredFilePayload: plan.ExpiredFilePayload,
		CanFiles:           plan.CanFiles,
		CanConversation:    plan.CanConversation,
		DisabledReason:     plan.DisabledReason,
		Conflicts:          conflictStrings(plan),
		Files:              plan.Files,
		FileCount:          plan.FileCount,
		ActiveWriters:      len(plan.ActiveWriters),
		Path:               plan.Path,
		ConversationAction: plan.ConversationAction,
	}
}

func conflictStrings(plan checkpoint.RewindPlan) []string {
	out := make([]string, 0, len(plan.Conflicts))
	for _, c := range plan.Conflicts {
		if c.Path != "" {
			out = append(out, c.Path+": "+c.Reason)
		} else {
			out = append(out, c.Reason)
		}
	}
	return out
}

func rewindResultToView(result checkpoint.RewindResult) RewindResultView {
	conflicts := make([]string, 0, len(result.Conflicts))
	for _, c := range result.Conflicts {
		if c.Path != "" {
			conflicts = append(conflicts, c.Path+": "+c.Reason)
		} else {
			conflicts = append(conflicts, c.Reason)
		}
	}
	txID := result.TransactionID
	if result.OperationID != "" {
		txID = result.OperationID
	}
	return RewindResultView{
		OK:                 result.OK,
		TransactionID:      txID,
		OperationID:        nonEmptyStr(result.OperationID, result.TransactionID),
		UndoAvailable:      result.UndoAvailable,
		Written:            result.Written,
		Deleted:            result.Deleted,
		ConversationOK:     result.ConversationOK || result.ConversationForked,
		ConversationForked: result.ConversationForked,
		Branch:             result.Branch,
		Partial:            result.Partial,
		Error:              result.Error,
		Conflicts:          conflicts,
		Coverage:           string(result.Coverage),
	}
}

func nonEmptyStr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// ForkForTab forks the requested source tab even if focus changes before the
// backend begins processing the request. The fork becomes active only while the
// source tab still owns focus, so a later tab selection remains authoritative.
func (a *App) ForkForTab(tabID string, turn int) (TabMeta, error) {
	result, err := a.forkForTabWithOptions(tabID, turn, false)
	return result.Tab, err
}

// ForkWorktreeForTab forks the requested source tab into an isolated Git worktree.
func (a *App) ForkWorktreeForTab(tabID string, turn int) (ForkWorktreeResultView, error) {
	return a.forkForTabWithOptions(tabID, turn, true)
}

func (a *App) SummarizeFromForTab(tabID string, turn int) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	return ctrl.SummarizeFrom(a.ctx, turn)
}

func (a *App) SummarizeUpToForTab(tabID string, turn int) error {
	tab, ctrl := a.tabAndCtrlByID(tabID)
	if a.tabIsReadOnly(tab) {
		return readOnlyChannelErr()
	}
	if ctrl == nil {
		return nil
	}
	return ctrl.SummarizeUpTo(a.ctx, turn)
}

type WorkspaceMeta struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

func tabSessionDir(tab *WorkspaceTab) string {
	if tab != nil {
		if tab.WorkspaceRoot != "" {
			return desktopSessionDir(tab.WorkspaceRoot)
		}
		if tab.Ctrl != nil {
			if dir := tab.Ctrl.SessionDir(); dir != "" {
				return dir
			}
		}
	}
	return desktopSessionDir("")
}

func tabRuntimeSessionDir(tab *WorkspaceTab) string {
	if tab != nil && tab.Ctrl != nil {
		if dir, ok := safeControllerSessionDir(tab.Ctrl); ok && strings.TrimSpace(dir) != "" {
			return dir
		}
	}
	return tabSessionDir(tab)
}

func (a *App) activeSessionDir() string {
	tab := a.activeTab()
	if tab != nil && tab.Ctrl != nil {
		return tabRuntimeSessionDir(tab)
	}
	return tabSessionDir(tab)
}

type fallbackRuntimeTarget struct {
	needs         bool
	scope         string
	workspaceRoot string
}

func removedRuntimeFromTab(tab *WorkspaceTab, dir, sessionPath string) removedSessionRuntime {
	return removedSessionRuntime{
		tab:           tab,
		ctrl:          tab.Ctrl,
		sink:          tab.sink,
		sessionDir:    dir,
		sessionPath:   sessionPath,
		scope:         tab.Scope,
		workspaceRoot: tab.WorkspaceRoot,
		topicID:       tab.TopicID,
		readOnly:      tab.ReadOnly,
	}
}

func waitControllerStopped(ctrl control.SessionAPI) error {
	deadline := time.Now().Add(5 * time.Second)
	for ctrl.Running() {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for cancelled session work to stop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (a *App) closeRemovedSessionRuntimes(removed []removedSessionRuntime) {
	defer a.lockRuntimeMutation("close-removed-session-runtimes")()
	a.closeRemainingRemovedSessionRuntimesAdmissionHeld(removed, map[control.SessionAPI]bool{})
}

func (a *App) closeRemainingRemovedSessionRuntimesAdmissionHeld(removed []removedSessionRuntime, closed map[control.SessionAPI]bool) {
	releasedTabs := map[*WorkspaceTab]bool{}
	for _, item := range removed {
		a.closeRemovedSessionRuntime(item, closed, releasedTabs, false)
	}
}

func (a *App) closeRemovedSessionRuntime(item removedSessionRuntime, closed map[control.SessionAPI]bool, releasedTabs map[*WorkspaceTab]bool, afterDestroy bool) {
	if item.tab != nil {
		if releasedTabs == nil || !releasedTabs[item.tab] {
			if releasedTabs != nil {
				releasedTabs[item.tab] = true
			}
			a.mu.Lock()
			if owner := a.tabByEventSinkIDLocked(item.tab.ID); owner == nil || owner == item.tab {
				a.forgetBrowserExecutorLocked(item.tab.ID)
			}
			a.mu.Unlock()
			a.releaseTabSharedHost(item.tab)
		}
	}
	if item.ctrl == nil {
		return
	}
	if closed == nil {
		closed = map[control.SessionAPI]bool{}
	}
	if closed[item.ctrl] {
		return
	}
	closed[item.ctrl] = true
	if afterDestroy {
		item.ctrl.CloseAfterDestroy()
		return
	}
	item.ctrl.Close()
}

// RenameSession renames a durable session identified by the current route.
func (a *App) RenameSession(path, title string) error {
	id, ok := parseSessionRoute(path)
	if !ok {
		return errors.New("session version is unavailable")
	}
	return a.RenameCanonicalSession(session.SessionRef{HostID: localDesktopHostID, SessionID: id}, title)
}

// onSessionTitleChanged projects the committed title from the current event log.
// Reading the log keeps a delayed callback from publishing an older title.
func (a *App) onSessionTitleChanged(_, sessionID, _ string) error {
	a.topicTitleMutationMu.Lock()
	defer a.topicTitleMutationMu.Unlock()
	ref := session.SessionRef{HostID: localDesktopHostID, SessionID: sessionID}
	if err := validateLocalSessionRef(ref); err != nil {
		return err
	}
	info, err := a.desktopSessionService("").Query().Stat(a.bootContext(), ref)
	if err != nil {
		return err
	}
	a.publishCanonicalSessionTitle(ref, info.Title)
	return nil
}

func (a *App) openChannelSessionForTranscript(tabID, path string, limit int, includeHistory bool) (HistoryPage, error) {
	page, err := a.resumeSessionForTranscript(tabID, path, limit, includeHistory)
	if err == nil {
		a.setTabReadOnly(tabID, true)
	}
	return page, err
}

func (a *App) runRebindCandidateHook(stage string) error {
	if a == nil || a.rebindCandidateHook == nil {
		return nil
	}
	return a.rebindCandidateHook(stage)
}

// PreviewSession reads a saved session for display only. It does not snapshot or
// swap the active controller, so the history drawer can call it while a turn runs.
func (a *App) PreviewSession(path string) ([]HistoryMessage, error) {
	target, err := a.resolveSessionTargetWithArchived(SessionSelector{SessionPath: path}, true)
	if err != nil {
		return nil, err
	}
	messages, err := a.desktopSessionService("").Query().History(a.bootContext(), target.SessionRef)
	if err != nil {
		return nil, err
	}
	return historyMessagesFromProvider(messages, historyReplayUserContent, nil), nil
}

// invalidatePromptHistoryCache resets the lazy prompt-history tape so the next
// ScanPromptHistory call rebuilds session order and reloads sessions on demand.
// Called from every session-mutating path: NewSession, ClearSession,
// DeleteSession, RestoreSession, PurgeTrashedSession, RenameSession.
func (a *App) invalidatePromptHistoryCache() {
	a.promptHistoryMu.Lock()
	a.promptHistoryTape = nil
	a.promptHistoryMu.Unlock()
}

const (
	promptHistoryPageLimit    = 50
	promptHistoryMaxPageLimit = 200
)

type promptHistoryRequest struct {
	Nonce  string `json:"nonce,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type promptHistoryCursor struct {
	Nonce   string `json:"n"`
	Session int    `json:"s"`
	Offset  int    `json:"o"`
}

type promptHistoryTape struct {
	nonce       string
	dir         string
	currentPath string
	read        func(session.SessionInfo) ([]provider.Message, error)
	sessions    []session.SessionInfo
	loaded      map[string][]PromptHistoryEntry
}

// ScanPromptHistory returns the next prompt-history tape segment. The request is
// a JSON string so the Wails binding stays one-argument while the protocol can
// carry a cursor and page limit.
func (a *App) ScanPromptHistory(rawRequest string) (PromptHistoryResult, error) {
	var req promptHistoryRequest
	if strings.TrimSpace(rawRequest) != "" {
		if err := json.Unmarshal([]byte(rawRequest), &req); err != nil {
			return PromptHistoryResult{}, fmt.Errorf("invalid prompt history request: %w", err)
		}
	}
	a.mu.RLock()
	tab := a.activeTabLocked()
	scope, root, current := "global", "", ""
	if tab != nil {
		scope, root, current = tab.Scope, tab.WorkspaceRoot, tab.SessionID
	}
	a.mu.RUnlock()
	a.promptHistoryMu.Lock()
	tape, err := a.promptHistoryTapeForLocked(scope, root, current)
	if err != nil {
		a.promptHistoryMu.Unlock()
		return PromptHistoryResult{}, err
	}

	result := tape.readOlder(req.Cursor, promptHistoryLimit(req.Limit))
	a.promptHistoryMu.Unlock()
	return result, nil
}

func promptHistoryLimit(limit int) int {
	if limit <= 0 {
		return promptHistoryPageLimit
	}
	if limit > promptHistoryMaxPageLimit {
		return promptHistoryMaxPageLimit
	}
	return limit
}

func (a *App) promptHistoryTapeForLocked(scope, root, currentID string) (*promptHistoryTape, error) {
	key := scope + "\x00" + root
	if a.promptHistoryTape != nil && a.promptHistoryTape.dir == key && a.promptHistoryTape.currentPath == currentID {
		return a.promptHistoryTape, nil
	}
	tape, err := a.newPromptHistoryTape(scope, root, currentID)
	if err != nil {
		return nil, err
	}
	a.promptHistoryTape = tape
	return tape, nil
}

func (a *App) newPromptHistoryTape(scope, root, currentID string) (*promptHistoryTape, error) {
	sessions, err := a.workspaceHistorySessions(scope, root)
	if err != nil {
		return nil, err
	}
	for i, info := range sessions {
		if info.SessionID == currentID {
			copy(sessions[1:i+1], sessions[:i])
			sessions[0] = info
			break
		}
	}
	return &promptHistoryTape{
		nonce: rand.Text(), dir: scope + "\x00" + root, currentPath: currentID,
		sessions: sessions, loaded: map[string][]PromptHistoryEntry{},
		read: func(info session.SessionInfo) ([]provider.Message, error) {
			return a.desktopSessionService("").Query().History(a.bootContext(), info.Ref)
		},
	}, nil
}

func (t *promptHistoryTape) readOlder(cursor string, limit int) PromptHistoryResult {
	c := promptHistoryCursor{Nonce: t.nonce}
	if decoded, ok := decodePromptHistoryCursor(cursor); ok && decoded.Nonce == t.nonce {
		c = decoded
	}
	if c.Session < 0 {
		c.Session = 0
	}
	if c.Offset < 0 {
		c.Offset = 0
	}

	out := make([]PromptHistoryEntry, 0, limit)
	sessionIndex := c.Session
	offset := c.Offset
	for sessionIndex < len(t.sessions) && len(out) < limit {
		entries, err := t.entriesForSession(sessionIndex)
		if err != nil || offset >= len(entries) {
			sessionIndex++
			offset = 0
			continue
		}

		end := min(len(entries), offset+limit-len(out))
		out = append(out, entries[offset:end]...)
		offset = end
		if offset >= len(entries) && len(out) < limit {
			sessionIndex++
			offset = 0
		}
	}

	if sessionIndex < len(t.sessions) {
		if entries, ok := t.loaded[t.sessions[sessionIndex].SessionID]; ok && offset >= len(entries) {
			sessionIndex++
			offset = 0
		}
	}
	hasOlder := sessionIndex < len(t.sessions)
	olderCursor := ""
	if hasOlder {
		olderCursor = encodePromptHistoryCursor(promptHistoryCursor{Nonce: t.nonce, Session: sessionIndex, Offset: offset})
	}
	return PromptHistoryResult{Entries: out, Nonce: t.nonce, OlderCursor: olderCursor, HasOlder: hasOlder}
}

func (t *promptHistoryTape) readAll() []PromptHistoryEntry {
	out := []PromptHistoryEntry{}
	cursor := ""
	for {
		page := t.readOlder(cursor, promptHistoryMaxPageLimit)
		out = append(out, page.Entries...)
		if !page.HasOlder || page.OlderCursor == "" {
			return out
		}
		cursor = page.OlderCursor
	}
}

func (t *promptHistoryTape) entriesForSession(index int) ([]PromptHistoryEntry, error) {
	if index < 0 || index >= len(t.sessions) {
		return nil, nil
	}
	info := t.sessions[index]
	if entries, ok := t.loaded[info.SessionID]; ok {
		return entries, nil
	}
	messages, err := t.read(info)
	if err != nil {
		return nil, err
	}
	entries := promptHistoryEntries(info, messages)
	sortPromptHistoryNewestFirst(entries)
	t.loaded = map[string][]PromptHistoryEntry{info.SessionID: entries}
	return entries, nil
}

func promptHistoryEntries(info session.SessionInfo, messages []provider.Message) []PromptHistoryEntry {
	var entries []PromptHistoryEntry
	for _, message := range messages {
		if !agent.IsUserAuthoredTurnMessage(message) {
			continue
		}
		text := sessionUserPromptText(message, historyReplayUserContent)
		if text == "" {
			continue
		}
		at := message.CreatedAt
		if at == 0 {
			at = info.UpdatedAt.UnixMilli()
		}
		entries = append(entries, PromptHistoryEntry{Text: text, At: at, SessionPath: sessionRoute(info.SessionID), Turn: len(entries)})
	}
	return entries
}

func encodePromptHistoryCursor(cursor promptHistoryCursor) string {
	b, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodePromptHistoryCursor(value string) (promptHistoryCursor, bool) {
	if strings.TrimSpace(value) == "" {
		return promptHistoryCursor{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return promptHistoryCursor{}, false
	}
	var cursor promptHistoryCursor
	if err := json.Unmarshal(b, &cursor); err != nil {
		return promptHistoryCursor{}, false
	}
	return cursor, true
}

func promptHistoryEntryNewer(a, b PromptHistoryEntry) bool {
	if a.At != b.At {
		return a.At > b.At
	}
	if a.SessionPath != b.SessionPath {
		return a.SessionPath > b.SessionPath
	}
	return a.Turn > b.Turn
}

func sortPromptHistoryNewestFirst(entries []PromptHistoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return promptHistoryEntryNewer(entries[i], entries[j])
	})
}

func sessionUserPromptText(message provider.Message, resolveUserContent func(string) string) string {
	if strings.TrimSpace(message.RawContent) != "" {
		return strings.TrimSpace(agent.UserMessageText(message))
	}
	return strings.TrimSpace(resolveUserContent(strings.TrimSpace(message.Content)))
}

// PickWorkspace opens a folder chooser and, on a pick, opens a new project tab
// scoped to that folder. Returns the chosen path ("" if cancelled).
func (a *App) PickWorkspace() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur, _ := os.Getwd()
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil && tab.WorkspaceRoot != "" {
		cur = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	dir, err := a.nativeHost().OpenDirectoryDialog(a.ctx, nativeDialogOptions{
		Title:            "Choose working folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return a.SwitchWorkspace(dir)
}

func dialogDefaultDirectory(preferred string) string {
	if dir := nearestExistingDirectory(preferred); dir != "" {
		return dir
	}
	if cwd, err := os.Getwd(); err == nil {
		if dir := nearestExistingDirectory(cwd); dir != "" {
			return dir
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if dir := nearestExistingDirectory(home); dir != "" {
			return dir
		}
	}
	return ""
}

func nearestExistingDirectory(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for {
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return path
			}
			path = filepath.Dir(path)
			continue
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func (a *App) ListWorkspaces() []WorkspaceMeta {
	activeRoot := ""
	cur, _ := os.Getwd()
	a.mu.RLock()
	if tab := a.activeTabLocked(); tab != nil && tab.WorkspaceRoot != "" {
		activeRoot = normalizeProjectRoot(tab.WorkspaceRoot)
	}
	a.mu.RUnlock()
	if activeRoot == "" {
		activeRoot = normalizeProjectRoot(cur)
	}
	projects := loadProjectsFile().Projects
	out := make([]WorkspaceMeta, 0, len(projects))
	for _, project := range projects {
		out = append(out, WorkspaceMeta{
			Path:    project.Root,
			Name:    projectDisplayName(project),
			Current: activeRoot != "" && sameProjectRoot(project.Root, activeRoot),
		})
	}
	return out
}

func (a *App) RemoveWorkspace(dir string) error {
	if dir == "" {
		return fmt.Errorf("workspace path is required")
	}
	dir = normalizeProjectRoot(dir)
	a.singleSurfaceMu.Lock()
	defer a.singleSurfaceMu.Unlock()
	releaseRemoval, err := a.reserveWorkspaceRemoval(dir)
	if err != nil {
		return err
	}
	defer releaseRemoval()

	var fallback *WorkspaceTab
	// sessionRemovalMu covers every step that can still touch this workspace's
	// session files: snapshotting, unlinking the tab/runtime bindings, and
	// closing the unlinked runtimes (quiescing autosave). Once a runtime is
	// unlinked from a.tabs/detachedSessions it is invisible to
	// DeleteSession/TrashTopic/RestoreSession, so it must stop writing before
	// the lock is released. Durable project bookkeeping precedes unlinking;
	// the fallback controller build and notifications run after release.
	if err := func() error {
		defer a.lockRuntimeMutation("remove-workspace")()
		a.sessionRemovalMu.Lock()
		defer a.sessionRemovalMu.Unlock()

		candidates, err := a.snapshotWorkspaceTabsForRemoval(dir)
		if err != nil {
			return err
		}
		if err := a.hideWorkspaceForRemoval(dir); err != nil {
			return err
		}
		var closeTabs, closeDetached []*WorkspaceTab
		fallback, closeTabs, closeDetached = a.unlinkWorkspaceTabsForRemoval(dir, candidates)

		for _, tab := range closeTabs {
			a.closeTabRuntimeAdmissionHeld(tab)
		}
		for _, tab := range closeDetached {
			a.closeTabRuntimeAdmissionHeld(tab)
		}
		return nil
	}(); err != nil {
		return err
	}

	// The fallback tab is already linked into a.tabs; its controller build is
	// asynchronous and does not touch removed session files, so it does not
	// need the removal lock.
	if fallback != nil {
		a.startTabControllerBuild(fallback)
	}

	// If the removed workspace was the active one, clear the pointer
	// so we don't leave a stale reference to a deleted project.
	if loadWorkspace() == dir {
		if remaining := loadProjectsFile(); len(remaining.Projects) > 0 {
			// Fall back to the first remaining project
			saveWorkspace(remaining.Projects[0].Root)
		} else {
			// No projects left; clear the active pointer entirely
			clearWorkspace()
		}
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

func workspaceName(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return path
	}
	return name
}

// tabWorkspaceNameForScope resolves the display name for a tab's workspace.
// Callers pass tab.Scope copied under a.mu instead of re-reading the tab.
func tabWorkspaceNameForScope(scope, cwd string) string {
	if scope == "global" {
		return globalProjectTitle()
	}
	return workspaceName(cwd)
}

func (a *App) SwitchWorkspace(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = home
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	// Selecting a workspace is navigation, not a new-session command. Serialize
	// selection and initial-topic creation so duplicate/retried opens reuse it.
	navigation := a.desktopSessions.navigationSeq.Add(1)
	a.singleSurfaceMu.Lock()
	defer a.singleSurfaceMu.Unlock()
	if a.desktopSessions.navigationSeq.Load() != navigation {
		return "", errSessionNavigationSuperseded
	}
	releaseAdmission, err := a.beginProjectRuntimeAdmission("project", dir)
	if err != nil {
		return "", err
	}
	releaseAdmission()

	// Adding a folder is an explicit request to show that workspace again.
	// EnsureWorkspaceResolved deliberately preserves an existing workspace's
	// presentation, including Visible=false after RemoveWorkspace, so restore
	// visibility through the dedicated presentation mutation before opening it.
	workspaceID, err := a.ensureDesktopWorkspace(a.bootContext(), "project", dir)
	if err != nil {
		return "", err
	}
	registry := a.workspaceRegistry()
	state, err := registry.Load(a.bootContext())
	if err != nil {
		return "", err
	}
	wasVisible := state.Workspaces[workspaceID].Visible
	if !wasVisible {
		if err := registry.SetWorkspaceVisible(a.bootContext(), workspaceID, true); err != nil {
			return "", err
		}
	}
	rollbackVisibility := func(cause error) error {
		if wasVisible {
			return cause
		}
		if restoreErr := registry.SetWorkspaceVisible(a.bootContext(), workspaceID, false); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore workspace visibility: %w", restoreErr))
		}
		return cause
	}

	// Ensure project metadata is present before querying its existing topics,
	// including placeholders left by an interrupted initial open.
	if err := addProject(dir, ""); err != nil {
		return "", rollbackVisibility(err)
	}
	topicID, sessionPath, err := a.workspaceEntryConversation(dir)
	if err != nil {
		return "", rollbackVisibility(err)
	}
	if topicID == "" && sessionPath == "" {
		topic, err := a.CreateTopic("project", dir, "")
		if err != nil {
			return "", rollbackVisibility(err)
		}
		topicID = topic.ID
	}
	meta, err := a.activateTopicLocked("project", dir, topicID, sessionPath, navigation)
	if err != nil {
		return "", rollbackVisibility(err)
	}
	if !wasVisible {
		// A restored workspace can reuse an existing topic, so no topic-created
		// event follows the visibility write. Publish the completed membership
		// change for already-mounted project trees as well as fresh readers.
		a.emitProjectTreeMetadataChanged()
	}
	return meta.WorkspaceRoot, nil
}

// HistoryMessage is one prior turn, for the frontend to repopulate its transcript
// after a reload.
type HistoryMessage = transcript.Message

func interruptedTurnHistoryNotice(recovery *provider.InterruptedTurnRecovery) HistoryMessage {
	if recovery != nil && recovery.TerminalStatus == "failed" {
		diagnostic := recovery.FailureDiagnostic
		message := "The provider request failed. Check the connection settings and try again."
		detail := provider.FailureDiagnosticDetail(diagnostic)
		if diagnostic != nil {
			if statusMessage := i18n.M.ProviderStatusMessage(diagnostic.Status); statusMessage != "" {
				message = statusMessage
			} else if diagnostic.Status > 0 {
				message = fmt.Sprintf("Provider request failed (HTTP %d).", diagnostic.Status)
			}
			label := provider.ProviderDisplayLabel(diagnostic.ProviderID, diagnostic.ProviderDisplayName, diagnostic.Protocol)
			if label != "" {
				message = label + ": " + message
			}
		}
		return HistoryMessage{Role: "notice", Level: "warn", Code: event.NoticeCodeProviderRequestFailed, Content: message, Detail: detail, Diagnostic: diagnostic}
	}
	return HistoryMessage{
		Role: "notice", Level: "info", Code: event.NoticeCodeCancelledTurn,
		Content: "This turn was interrupted. Partial output is kept for reference; only completed tool pairs and a bounded recovery summary enter the next model turn. Inspect the workspace before continuing or reverting changes.",
	}
}

type HistoryToolCall = transcript.ToolCall

const (
	defaultHistoryPageTurns = 60
	maxHistoryPageTurns     = 200
)

type HistoryPage struct {
	Messages   []HistoryMessage     `json:"messages"`
	StartTurn  int                  `json:"startTurn"`
	EndTurn    int                  `json:"endTurn"`
	TotalTurns int                  `json:"totalTurns"`
	HasOlder   bool                 `json:"hasOlder"`
	Revision   int64                `json:"revision,omitempty"`
	Digest     string               `json:"digest,omitempty"`
	Switch     *HistorySwitchPhases `json:"switch,omitempty"`
}

// HistorySwitchPhases records a session adoption and optional initial page.
// It carries durations, counts and sizes, never paths or message content.
// Snapshot adoption leaves HistoryMs and HistoryCount zero; the frontend
// measures its authoritative snapshot separately. A changed controller may
// require another durable read instead of reusing an obsolete preload.
type HistorySwitchPhases struct {
	ResolveMs    int64 `json:"resolveMs"`
	LoadMs       int64 `json:"loadMs"`
	RebindMs     int64 `json:"rebindMs"`
	HistoryMs    int64 `json:"historyMs"`
	TotalMs      int64 `json:"totalMs"`
	LoadedCount  int   `json:"loadedMessages"`
	LoadedBytes  int64 `json:"loadedBytes"`
	HistoryCount int   `json:"historyEntries"`
	// DurableReads counts target log reads, including refreshes after preload invalidation.
	DurableReads int    `json:"durableReads"`
	Outcome      string `json:"outcome"`
}

func historyPageFromMessagesForTab(tab *WorkspaceTab, ctrl control.SessionAPI, msgs []provider.Message, beforeTurn, limit int) HistoryPage {
	if tab == nil || ctrl == nil {
		return HistoryPage{Messages: []HistoryMessage{}}
	}
	page := historyPageFromProviderMessages(
		msgs,
		historyReplayUserContent,
		ctrl.CheckpointTurnsByMessageIndex(),
		beforeTurn,
		limit,
	)
	// Fingerprint the exact transcript used for this page, without sidecar reads.
	page.Digest, _ = agent.ContentDigestForMessages(msgs)
	return page
}

func normalizeHistoryPageLimit(limit int) int {
	if limit <= 0 {
		return defaultHistoryPageTurns
	}
	if limit > maxHistoryPageTurns {
		return maxHistoryPageTurns
	}
	return limit
}

// historyReplayUserContent keeps only user-authored replay data. Provider-facing
// capability, goal, hook, and resolved-reference context must not be resubmitted.
func historyReplayUserContent(content string) string {
	return control.StripReferencedContextPrefix(control.StripComposePrefixes(content))
}

func historyMessagesFromProvider(msgs []provider.Message, resolveUserContent func(string) string, checkpointTurns map[int]int) []HistoryMessage {
	toolResults := historyToolResultsByID(msgs)
	return historyMessagesFromProviderWithLookups(msgs, resolveUserContent, checkpointTurns, toolResults)
}

func historyMessagesFromProviderWithLookups(
	msgs []provider.Message,
	resolveUserContent func(string) string,
	checkpointTurns map[int]int,
	toolResults map[string]provider.Message,
) []HistoryMessage {
	out := make([]HistoryMessage, 0, len(msgs))
	for index, m := range msgs {
		out = append(out, convertHistoryMessage(index, m, resolveUserContent, checkpointTurns, toolResults)...)
	}
	return out
}

// convertHistoryMessage converts one provider message into its 0..n history
// rows. index is the message's position in the coordinate system of
// checkpointTurns (window-relative for the legacy full-pass callers, absolute
// for the windowed slice API).
func convertHistoryMessage(
	index int,
	m provider.Message,
	resolveUserContent func(string) string,
	checkpointTurns map[int]int,
	toolResults map[string]provider.Message,
) []HistoryMessage {
	var out []HistoryMessage
	if m.Role == provider.Role("compaction") {
		return maintenanceHistoryMessage(m)
	}
	if m.DecisionReceipt != nil {
		return append(out, HistoryMessage{
			Role:            "notice",
			Code:            event.NoticeCodeDecisionReceipt,
			Level:           "info",
			DecisionReceipt: cloneDecisionReceipt(m.DecisionReceipt),
		})
	}
	if rows, handled := historyLocalOnlyRows(m); handled {
		return append(out, rows...)
	}
	content := m.Content
	var checkpointTurn *int
	if m.Role == provider.RoleUser {
		// Mid-turn steer messages are persisted in the session so they
		// survive tab switches. They are surfaced as a notice (↪ text)
		// — matching the live Steer event look — rather than as a
		// regular user bubble or being filtered as synthetic (#4044).
		// Check against the raw m.Content: resolveUserContent applies
		// StripComposePrefixes which trims trailing whitespace.
		if rows, handled := historySteerRows(m.Content, false); handled {
			return append(out, rows...)
		}
		content = transcript.CollapseExpandedPaste(resolveUserContent(agent.UserMessageText(m)))
		if agent.IsHostGeneratedUserMessage(m) {
			return out
		}
		if turn, ok := checkpointTurns[index]; ok {
			turnCopy := turn
			checkpointTurn = &turnCopy
		}
	}
	reasoning := ""
	if m.Role == provider.RoleAssistant || m.LocalOnly {
		reasoning = m.ReasoningContent
	}
	displayRole := string(m.Role)
	if m.LocalOnly {
		displayRole = "assistant"
	}
	hm := HistoryMessage{MessageID: m.ID, Role: displayRole, Content: content, CheckpointTurn: checkpointTurn, CreatedAt: m.CreatedAt, Reasoning: reasoning, WorkDurationMs: m.WorkDurationMs}
	if m.Role == provider.RoleAssistant && len(m.MemoryCitations) > 0 {
		hm.MemoryCitations = append([]provider.MemoryCitation(nil), m.MemoryCitations...)
	}
	if m.Role == provider.RoleUser && content != m.Content {
		replay := historyReplayUserContent(m.Content)
		if replay != content {
			hm.SubmitText = replay
		}
	}
	hm.ServerSearch = historyServerSearch(m.ServerSearch)
	inputs := m.ImageInputs
	if len(inputs) == 0 && len(m.Images) > 0 {
		// Live providers still carry transient image encodings; display them
		// through the same current input representation used after persistence.
		if stored, err := m.StorageView(); err == nil {
			inputs = stored.ImageInputs
		}
	}
	hm.Attachments = historyDisplayAttachments(inputs)
	if (m.Role == provider.RoleAssistant || m.LocalOnly) && len(m.ToolCalls) > 0 {
		hm.ToolCalls = make([]HistoryToolCall, len(m.ToolCalls))
		for i, tc := range m.ToolCalls {
			// Historical tool calls are immutable facts. Never rewrite todo_write
			// arguments by interpreting later results as current todo state.
			hm.ToolCalls[i] = historyToolCall(tc, tc.Arguments, toolResults[tc.ID])
		}
	}
	if m.Role == provider.RoleTool && !m.LocalOnly {
		hm.ToolCallID = m.ToolCallID
		hm.ToolName = m.Name
		hm.Content, hm.ToolResultArchived, hm.ToolResultError = historyToolResultContent(m.Content, m.ToolCallID != "")
		hm.Execution = m.ToolExecution
	}
	hasVisibleLocalContent := strings.TrimSpace(hm.Content) != "" || strings.TrimSpace(hm.Reasoning) != "" || len(hm.ToolCalls) > 0 || (!m.LocalOnly && m.Role == provider.RoleTool)
	if !m.LocalOnly || hasVisibleLocalContent {
		out = append(out, hm)
	}
	for _, receipt := range m.DecisionReceipts {
		if receipt == nil {
			continue
		}
		out = append(out, HistoryMessage{
			Role:            "notice",
			Code:            event.NoticeCodeDecisionReceipt,
			Level:           "info",
			DecisionReceipt: cloneDecisionReceipt(receipt),
		})
	}
	if m.LocalOnly && m.InterruptedTurn != nil {
		out = append(out, interruptedTurnHistoryNotice(m.InterruptedTurn))
	}
	return out
}

func cloneDecisionReceipt(in *provider.DecisionReceipt) *provider.DecisionReceipt {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func historyPageFromProviderMessages(
	msgs []provider.Message,
	resolveUserContent func(string) string,
	checkpointTurns map[int]int,
	beforeTurn, limit int,
) HistoryPage {
	limit = normalizeHistoryPageLimit(limit)
	totalTurns := visibleHistoryUserTurns(msgs, resolveUserContent)
	if beforeTurn <= 0 || beforeTurn > totalTurns {
		beforeTurn = totalTurns
	}
	startTurn := max(beforeTurn-limit, 0)
	page := HistoryPage{
		StartTurn:  startTurn,
		EndTurn:    beforeTurn,
		TotalTurns: totalTurns,
		HasOlder:   startTurn > 0,
	}
	if len(msgs) == 0 || startTurn >= beforeTurn {
		page.Messages = []HistoryMessage{}
		return page
	}
	pageMessages, originalIndexes := providerMessagesForVisibleTurnRange(msgs, resolveUserContent, startTurn, beforeTurn)
	page.Messages = historyMessagesFromProviderWithLookups(
		pageMessages,
		resolveUserContent,
		checkpointTurnsForProviderWindow(checkpointTurns, originalIndexes),
		historyToolResultsByID(msgs),
	)
	return page
}

func visibleHistoryUserTurns(msgs []provider.Message, resolveUserContent func(string) string) int {
	total := 0
	for _, msg := range msgs {
		if isVisibleHistoryUser(msg, resolveUserContent) {
			total++
		}
	}
	return total
}

func isVisibleHistoryUser(msg provider.Message, resolveUserContent func(string) string) bool {
	return agent.IsUserAuthoredTurnMessage(msg)
}

func providerMessagesForVisibleTurnRange(msgs []provider.Message, resolveUserContent func(string) string, startTurn, endTurn int) ([]provider.Message, []int) {
	out := make([]provider.Message, 0, len(msgs))
	indexes := make([]int, 0, len(msgs))
	turn := -1
	for index, msg := range msgs {
		if isVisibleHistoryUser(msg, resolveUserContent) {
			turn++
		}
		if turn < 0 {
			if startTurn == 0 {
				out = append(out, msg)
				indexes = append(indexes, index)
			}
			continue
		}
		if turn >= startTurn && turn < endTurn {
			out = append(out, msg)
			indexes = append(indexes, index)
		}
	}
	return out, indexes
}

func checkpointTurnsForProviderWindow(checkpointTurns map[int]int, originalIndexes []int) map[int]int {
	if len(checkpointTurns) == 0 || len(originalIndexes) == 0 {
		return nil
	}
	out := map[int]int{}
	for pageIndex, originalIndex := range originalIndexes {
		if turn, ok := checkpointTurns[originalIndex]; ok {
			out[pageIndex] = turn
		}
	}
	return out
}

const historyToolPreviewLimit = 2_000

func historyToolCall(tc provider.ToolCall, args string, result provider.Message) HistoryToolCall {
	call := HistoryToolCall{
		ID:               tc.ID,
		Name:             tc.Name,
		ResolvedName:     tc.ResolvedName,
		CapabilityID:     tc.CapabilityID,
		ResolvedReadOnly: tc.ResolvedReadOnly,
		Subject:          historyToolSubject(tc.Name, args),
		Summary:          historyToolSummary(tc.Name, args, result.Content),
		Diff:             tc.Diff,
		Added:            tc.Added,
		Removed:          tc.Removed,
	}
	if tc.Name == "todo_write" {
		call.Arguments = args
		return call
	}
	if tc.ID == "" {
		call.Arguments = args
		return call
	}
	if args != "" {
		call.ArgumentsArchived = true
	}
	return call
}

func historyToolResultsByID(msgs []provider.Message) map[string]provider.Message {
	out := map[string]provider.Message{}
	for _, msg := range msgs {
		if msg.Role != provider.RoleTool || msg.ToolCallID == "" {
			continue
		}
		out[msg.ToolCallID] = msg
	}
	return out
}

func historyToolResultContent(content string, canArchive bool) (display string, archived bool, errPreview string) {
	if content == "" {
		return "", false, ""
	}
	if !canArchive {
		if historyToolResultFailed(content) {
			return content, false, content
		}
		return content, false, ""
	}
	if historyToolResultFailed(content) {
		display = clipHistoryToolPreview(strings.TrimSpace(content))
		return display, display != content, display
	}
	return "", true, ""
}

func clipHistoryToolPreview(s string) string {
	if len(s) <= historyToolPreviewLimit {
		return s
	}
	return strings.TrimSpace(clipStringBytes(s, historyToolPreviewLimit)) + "\n..."
}

func historyToolSubject(name, args string) string {
	a := parseHistoryToolArgs(args)
	var subject string
	if historyShellToolName(name) {
		return clipSingleLine(historyArgString(a, "command"), 240)
	}
	switch name {
	case "grep", "glob":
		subject = firstNonEmpty(historyArgString(a, "pattern"), historyArgString(a, "path"))
	case "web_fetch":
		subject = historyArgString(a, "url")
	case "task":
		subject = firstNonEmpty(historyArgString(a, "description"), historyArgString(a, "prompt"))
	case "run_skill":
		subject = historyArgString(a, "name")
	case "move_file":
		src := historyArgString(a, "source_path")
		dst := historyArgString(a, "destination_path")
		if src != "" && dst != "" {
			subject = src + " -> " + dst
		} else {
			subject = firstNonEmpty(src, dst)
		}
	case "remember":
		subject = firstNonEmpty(historyArgString(a, "name"), historyArgString(a, "description"))
	case "todo_write", "exit_plan_mode":
		subject = ""
	default:
		subject = firstNonEmpty(historyArgString(a, "path"), historyArgString(a, "file_path"))
	}
	return clipSingleLine(subject, 240)
}

func historyToolSummary(name, args, output string) string {
	if historyToolResultFailed(output) {
		return ""
	}
	if historyShellToolName(name) {
		if strings.TrimSpace(output) == "" {
			return "no output"
		}
		return fmt.Sprintf("%d lines", historyLineCount(output))
	}
	a := parseHistoryToolArgs(args)
	switch name {
	case "write_file":
		if content := historyArgString(a, "content"); content != "" {
			return fmt.Sprintf("%d lines", historyLineCount(content))
		}
	case "edit_file":
		oldText := historyArgString(a, "old_string")
		newText := historyArgString(a, "new_string")
		if oldText != "" || newText != "" {
			return fmt.Sprintf("%d -> %d lines", historyLineCount(oldText), historyLineCount(newText))
		}
	case "multi_edit":
		if edits, ok := a["edits"].([]any); ok && len(edits) > 0 {
			return fmt.Sprintf("%d edits", len(edits))
		}
	}
	if output == "" {
		return ""
	}
	switch name {
	case "read_file":
		if strings.HasPrefix(output, "(empty file)") {
			return "empty file"
		}
		if arrows := strings.Count(output, "→"); arrows > 0 {
			return fmt.Sprintf("%d lines", arrows)
		}
		return fmt.Sprintf("%d lines", historyLineCount(output))
	case "grep":
		return fmt.Sprintf("%d matches", historyNonEmptyLineCount(output))
	case "glob":
		return fmt.Sprintf("%d files", historyNonEmptyLineCount(output))
	case "ls":
		return fmt.Sprintf("%d entries", historyNonEmptyLineCount(output))
	case "web_fetch":
		return clipSingleLine(strings.SplitN(output, "\n", 2)[0], 80)
	default:
		return ""
	}
}

func historyShellToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "pwsh", "powershell", "shell":
		return true
	default:
		return false
	}
}

func parseHistoryToolArgs(args string) map[string]any {
	if args == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(args), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func historyArgString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func historyLineCount(s string) int {
	if s == "" {
		return 0
	}
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func historyNonEmptyLineCount(s string) int {
	count := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func clipSingleLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return clipStringBytes(s, max)
	}
	return clipStringBytes(s, max-3) + "..."
}

func clipStringBytes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func historyToolResultFailed(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "error:") ||
		strings.HasPrefix(content, "blocked:") ||
		strings.HasPrefix(content, "Error:") ||
		strings.HasPrefix(content, "[error")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (a *App) ContextUsageForTab(tabID string) ContextInfo {
	if a.isRemoteTab(tabID) {
		used, window, ok := a.remoteContextSnapshot(tabID)
		if ok {
			return ContextInfo{Used: used, Window: window}
		}
		return ContextInfo{}
	}
	read := a.captureContextRead(tabID)
	ctrl := read.ctrl
	var info ContextInfo
	if read.tab != nil {
		snap := read.telemetry
		info.SessionTokens = snap.Usage.TotalTokens
		info.SessionCost = snap.Usage.SessionCost
		info.SessionCurrency = snap.Usage.SessionCurrency
		info.CacheHitTokens = snap.Usage.CacheHitTokens
		info.CacheMissTokens = snap.Usage.CacheMissTokens
		info.Estimated = snap.Usage.Estimated
		info.SessionCostComplete = snap.Usage.SessionCostComplete
		info.SessionCostQuote = snap.Usage.SessionCostQuote
		info.Sources = snap.Usage.Sources
	}
	if ctrl == nil {
		return info
	}
	// The gauge measures the loaded view, so a rebound session reports its real
	// fill immediately and no longer needs the persisted last-turn fallback.
	used, window := ctrl.ContextSnapshot()
	info.Used = used
	info.Window = window
	info.CompactRatio = ctrl.CompactRatio()
	snapshot := ctrl.ContextMaintenanceSnapshot()
	info.Maintenance = contextMaintenanceInfo(snapshot)
	if snapshot.ContextBudget != nil {
		info.ContextBudget = contextBudgetInfo(snapshot.ContextBudget)
	}
	if !read.current(a) {
		return ContextInfo{}
	}
	return info
}

// BalanceInfo is the wallet-balance readout for the status bar. Available is true
// only when a balance was fetched; Display is the exact formatted amount (e.g.
// "¥110.00")
// and is "" when the active provider declares no balance_url — the frontend then
// omits the readout. Err carries a fetch failure for an optional tooltip.
// Wallet balances are displayed in their original currencies; no conversion
// or cross-currency sum is performed.
type BalanceInfo struct {
	Available           bool     `json:"available"`
	Display             string   `json:"display"`
	Detail              string   `json:"detail,omitempty"` // per-wallet original balances
	Complete            bool     `json:"complete"`
	RateDate            string   `json:"rateDate,omitempty"`
	Approx              bool     `json:"approx,omitempty"`
	Currencies          []string `json:"currencies,omitempty"`
	PrimaryCurrency     string   `json:"primaryCurrency,omitempty"`
	CostDisplayCurrency string   `json:"costDisplayCurrency,omitempty"`
	MultiCurrency       bool     `json:"multiCurrency,omitempty"`
	Err                 string   `json:"err,omitempty"`
}

// Balance queries the active provider's wallet balance (a network call). It
// returns an empty (unavailable) readout when no provider balance_url is set, the
// controller is down, or the fetch fails — so the status bar simply shows nothing
// rather than an error.
func (a *App) Balance() BalanceInfo {
	return a.BalanceForTab("")
}

func (a *App) BalanceForTab(tabID string) BalanceInfo {
	currency := a.balanceDisplayCurrency()
	tab, ctrl, generation := a.balanceRequestTarget(tabID)
	if ctrl == nil {
		return BalanceInfo{}
	}
	b, err := ctrl.Balance(a.ctx)
	if err != nil {
		return BalanceInfo{Err: err.Error()}
	}
	if b == nil {
		return BalanceInfo{} // provider declares no balance endpoint
	}
	display := b.DisplayForCurrency(currency)
	currencies := b.Currencies()
	primary := b.PrimaryCurrency()
	a.applyBalanceDisplayHint(tabID, tab, ctrl, currency, primary, generation)
	detail := balanceDetail(b)
	return BalanceInfo{
		Available:           true,
		Display:             display,
		Detail:              detail,
		Complete:            true,
		Currencies:          currencies,
		PrimaryCurrency:     primary,
		CostDisplayCurrency: firstNonEmptyString(currency, primary),
		MultiCurrency:       len(currencies) > 1,
	}
}

func balanceDetail(b *billing.Balance) string {
	if b == nil || len(b.Infos) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b.Infos))
	for _, info := range b.Infos {
		cur := strings.ToUpper(strings.TrimSpace(info.Currency))
		if cur == "" {
			cur = "UNKNOWN"
		}
		parts = append(parts, cur+" "+strings.TrimSpace(info.TotalBalance))
	}
	return strings.Join(parts, "\n")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// balanceDisplayCurrency resolves only an explicit global display currency.
// Automatic mode leaves the wallet in its original currency.
func (a *App) balanceDisplayCurrency() string {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		return ""
	}
	if pref := cfg.DisplayCurrencyPref(); pref != "" {
		return pref
	}
	return cfg.ExplicitDisplayCurrency()
}

// JobView is one running background job (bash/task started with
// run_in_background) for the status-bar indicator.
type JobView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"`
}

// Jobs returns the still-running background jobs for the status bar. It refreshes
// on demand (mount, turn end, and on each notice the frontend receives).
func (a *App) Jobs() []JobView {
	return a.JobsForTab("")
}

func (a *App) JobsForTab(tabID string) []JobView {
	out := []JobView{}
	ctrl := a.ctrlForRuntimeTabID(tabID)
	return a.jobsForCtrl(ctrl, out)
}

// CancelJob stops one running background job in the active tab.
func (a *App) CancelJob(jobID string) (bool, error) {
	return a.CancelJobForTab("", jobID)
}

// CancelJobForTab stops one running background job without relying on whatever
// tab happens to be active when the asynchronous frontend call completes.
func (a *App) CancelJobForTab(tabID, jobID string) (bool, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return false, fmt.Errorf("job id is required")
	}
	if tabID != "" {
		if a.isRemoteTab(tabID) {
			if err := a.CancelRemoteTabJobs(tabID, []string{jobID}); err != nil {
				return false, err
			}
			return true, nil
		}
		ctrl := a.ctrlForRuntimeTabID(tabID)
		if ctrl != nil {
			return cancelJobForController(ctrl, jobID)
		}
		return false, nil
	}
	return cancelJobForController(a.ctrlForRuntimeTabID(tabID), jobID)
}

func cancelJobForController(ctrl control.SessionAPI, jobID string) (bool, error) {
	if ctrl == nil {
		return false, nil
	}
	canceller, ok := ctrl.(interface{ CancelJob(string) bool })
	if !ok {
		return false, fmt.Errorf("background job cancellation is unavailable")
	}
	return canceller.CancelJob(jobID), nil
}

func (a *App) jobsForCtrl(ctrl control.SessionAPI, out []JobView) []JobView {
	if ctrl == nil {
		return out
	}
	for _, v := range ctrl.Jobs() {
		out = append(out, JobView{ID: v.ID, Kind: v.Kind, Label: v.Label, Status: v.Status, StartedAt: v.StartedAt})
	}
	return out
}

func goalRuntimeViewFromController(ctrl control.SessionAPI) *GoalRuntimeView {
	if ctrl == nil {
		return nil
	}
	rt := ctrl.GoalRuntime()
	return &GoalRuntimeView{
		TurnsUsed:        rt.TurnsUsed,
		TokensUsed:       rt.TokensUsed,
		RequestsUsed:     rt.RequestsUsed,
		WorkDurationMs:   rt.WorkDurationMs,
		TokensLimit:      rt.TokensLimit,
		NoProgressTurns:  rt.NoProgressTurns,
		LastReason:       rt.LastReason,
		StopCause:        rt.StopCause,
		BudgetExtensions: rt.BudgetExtensions,
	}
}

// Meta reports the model label, readiness, any startup error, the working
// directory (for the status line), and the runtime event channel the frontend
// subscribes to.
func (a *App) Meta() Meta {
	return a.MetaForTab("")
}

func (a *App) loadConfigForVision(root string) (*config.Config, error) {
	if hook := a.configLoadForRootHook; hook != nil {
		hook(root)
	}
	return config.LoadForRootWithoutCredentialsReadOnly(root)
}

func (a *App) MetaForTab(tabID string) Meta {
	return a.metaForTab(tabID)
}

// ctrlTodos returns the canonical task list from a session controller, or nil
// if the controller is not yet bound. Used by MetaForTab so the frontend
// task panel has access to the authoritative server-side todo state.
func ctrlTodos(ctrl control.SessionAPI) *[]evidence.TodoItem {
	if ctrl == nil {
		return nil
	}
	todos := ctrl.Todos()
	if todos == nil {
		todos = []evidence.TodoItem{}
	}
	return &todos
}

// SetToolApprovalModeForTab returns the pending approval prompt ids the
// switch auto-allowed (see SetModeForTab).
func (a *App) SetToolApprovalModeForTab(tabID, mode string) []string {
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	mode = normalizeToolApprovalMode(mode)
	plan := tabModeHasPlan(a.tabRuntimeSnapshot(tab).currentMode())
	a.mu.Lock()
	if a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return nil
	}
	tab.toolApprovalMode = mode
	tab.mode = tabModeForPlan(plan)
	ctrl := tab.Ctrl
	tabIDForSave := tab.ID
	a.mu.Unlock()
	drained := applyTabToolApprovalModeToController(ctrl, mode)
	a.mu.Lock()
	if a.tabs[tabIDForSave] == tab {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return drained
}

// PermissionSnapshotForTab returns the authoritative preset, capability and
// same-session grant state for one desktop session.
func (a *App) PermissionSnapshotForTab(tabID string) (control.PermissionSnapshot, error) {
	if a.isRemoteTab(tabID) {
		if err := a.requireRemotePermissionPresets(tabID); err != nil {
			return control.PermissionSnapshot{}, err
		}
		client, base, expectedPath, err := a.remoteTabCommandTarget(tabID)
		if err != nil {
			return control.PermissionSnapshot{}, err
		}
		ctx, cancel := commandContext(a)
		defer cancel()
		return remotePermissionSnapshot(ctx, client, base, expectedPath)
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return control.PermissionSnapshot{}, fmt.Errorf("tab not found")
	}
	ctrl, ok := a.controllerForTab(tab).(*control.Controller)
	if !ok || ctrl == nil {
		return control.PermissionSnapshot{}, fmt.Errorf("permission snapshot is unavailable")
	}
	return ctrl.PermissionSnapshot(), nil
}

// SetPermissionPresetForTab applies a permission update fenced by the session
// and revision of the snapshot the caller read. The revision alone is per
// controller, so a snapshot of another session can carry the same number.
func (a *App) SetPermissionPresetForTab(tabID, expectedSessionID, preset string, expectedRevision uint64) (control.PermissionSnapshot, error) {
	if a.isRemoteTab(tabID) {
		if err := a.requireRemoteExecutionProtocol(tabID); err != nil {
			return control.PermissionSnapshot{}, err
		}
		if err := a.requireRemotePermissionPresets(tabID); err != nil {
			return control.PermissionSnapshot{}, err
		}
		client, base, expectedPath, err := a.remoteTabCommandTarget(tabID)
		if err != nil {
			return control.PermissionSnapshot{}, err
		}
		ctx, cancel := commandContext(a)
		defer cancel()
		live, err := remotePermissionSnapshot(ctx, client, base, expectedPath)
		if err != nil {
			return control.PermissionSnapshot{}, err
		}
		if err := requirePermissionSession(live, expectedSessionID); err != nil {
			return live, err
		}
		return setRemotePermissionPresetAt(ctx, client, base, expectedPath, preset, expectedRevision)
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return control.PermissionSnapshot{}, fmt.Errorf("tab not found")
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	ctrl, ok := a.controllerForTab(tab).(*control.Controller)
	if !ok || ctrl == nil {
		return control.PermissionSnapshot{}, fmt.Errorf("permission presets are unavailable")
	}
	// turnStartMu also serializes session navigation, so the session checked
	// here is the one the preset lands on.
	live := ctrl.PermissionSnapshot()
	if err := requirePermissionSession(live, expectedSessionID); err != nil {
		return live, err
	}
	snapshot, _, err := ctrl.SetPermissionPreset(preset, expectedRevision)
	if err != nil {
		return snapshot, err
	}
	a.mu.Lock()
	chosenFor := ""
	if a.tabs[tab.ID] == tab {
		tab.toolApprovalMode = snapshot.Preset
		tab.mode = tabModeForPlan(tabModeHasPlan(tab.mode))
		if tab.SessionID == snapshot.SessionID {
			chosenFor = tab.SessionID
		}
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	a.sessionPresets.record(chosenFor, snapshot.Preset)
	return snapshot, nil
}

// RevokePermissionGrantForTab removes one exact same-session authorization.
func (a *App) RevokePermissionGrantForTab(tabID, scope, target string, expectedRevision uint64) (control.PermissionSnapshot, error) {
	if a.isRemoteTab(tabID) {
		if err := a.requireRemoteExecutionProtocol(tabID); err != nil {
			return control.PermissionSnapshot{}, err
		}
		if err := a.requireRemotePermissionPresets(tabID); err != nil {
			return control.PermissionSnapshot{}, err
		}
		client, base, expectedPath, err := a.remoteTabCommandTarget(tabID)
		if err != nil {
			return control.PermissionSnapshot{}, err
		}
		ctx, cancel := commandContext(a)
		defer cancel()
		return revokeRemotePermissionGrantAt(ctx, client, base, expectedPath, scope, target, expectedRevision)
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return control.PermissionSnapshot{}, fmt.Errorf("tab not found")
	}
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	ctrl, ok := a.controllerForTab(tab).(*control.Controller)
	if !ok || ctrl == nil {
		return control.PermissionSnapshot{}, fmt.Errorf("permission grants are unavailable")
	}
	return ctrl.RevokeSessionGrant(scope, target, expectedRevision)
}

// CommandInfo describes one available slash command for the composer's "/" menu.
type CommandInfo struct {
	Name          string `json:"name"` // without the leading slash
	Description   string `json:"description"`
	Hint          string `json:"hint,omitempty"`  // argument hint, if any
	Kind          string `json:"kind"`            // "builtin" | "custom" | "mcp" | "skill" | "subagent"
	Group         string `json:"group,omitempty"` // menu group; older frontends can ignore it
	Plugin        string `json:"plugin,omitempty"`
	Color         string `json:"color,omitempty"`
	DraftBehavior string `json:"draftBehavior,omitempty"` // submit | setting | direct | unavailable
}

// Commands lists the slash commands available this session — built-in actions,
// custom commands (.deepzora/commands), and MCP prompts — for the composer's "/"
// autocomplete menu.
func (a *App) Commands() []CommandInfo {
	out := builtinCommandInfos()
	a.mu.RLock()
	ctrl := a.activeCtrlLocked()
	a.mu.RUnlock()
	if ctrl == nil {
		return append(out, docsBuiltinCommand(control.DocsSlashName))
	}
	commands := ctrl.Commands()
	slashSkills := ctrl.SlashSkills()
	out = append(out, docsBuiltinCommand(control.ResolvedBuiltinSlashName(control.DocsSlashName, commands, slashSkills)))
	// Skills are invocable as slash commands (the model runs inline ones; subagent ones
	// run isolated). Listing them here is what surfaces /init, /explore, … in the
	// composer's slash menu; selecting one submits its displayed slash name, which the controller
	// resolves via RunSkill.
	for _, s := range slashSkills {
		kind := "skill"
		if s.RunAs == skill.RunSubagent {
			kind = "subagent"
		}
		group := "skills"
		if kind == "subagent" {
			group = "subagents"
		}
		out = append(out, CommandInfo{Name: s.SlashName(), Description: s.Description, Kind: kind, Group: group, Plugin: s.Plugin, Color: s.Color})
	}
	for _, c := range commands {
		out = append(out, CommandInfo{Name: c.Name, Description: c.Description, Hint: c.ArgHint, Kind: "custom", Group: "skills", Plugin: c.Plugin})
	}
	if h := ctrl.Host(); h != nil {
		for _, p := range h.Prompts() {
			out = append(out, CommandInfo{Name: p.Name, Description: p.Description, Kind: "mcp", Group: "integrations"})
		}
	}
	return resolveDocsCommand(out)
}

func builtinCommandInfos() []CommandInfo {
	return []CommandInfo{
		{Name: "new", Description: i18n.M.CmdNew, Kind: "builtin", Group: "actions", DraftBehavior: "unavailable"},
		{Name: "clear", Description: i18n.M.CmdClear, Kind: "builtin", Group: "actions", DraftBehavior: "unavailable"},
		{Name: "compact", Description: i18n.M.CmdCompact, Kind: "builtin", Group: "actions", DraftBehavior: "unavailable"},
		{Name: "model", Description: i18n.M.CmdModel, Kind: "builtin", Group: "actions", DraftBehavior: "setting"},
		{Name: "provider", Description: i18n.M.CmdProvider, Kind: "builtin", Group: "management", DraftBehavior: "unavailable"},
		{Name: "effort", Description: i18n.M.CmdEffort, Kind: "builtin", Group: "actions", DraftBehavior: "setting"},
		{Name: "memory", Description: i18n.M.CmdMemory, Kind: "builtin", Group: "management", DraftBehavior: "unavailable"},
		{Name: "goal", Description: i18n.M.CmdGoal, Kind: "builtin", Group: "actions"},
		{Name: "remember", Description: i18n.M.CmdRemember, Kind: "builtin", Group: "management", DraftBehavior: "unavailable"},
		{Name: "mcp", Description: i18n.M.CmdMcp, Kind: "builtin", Group: "integrations", DraftBehavior: "unavailable"},
		{Name: "hooks", Description: i18n.M.CmdHooks, Kind: "builtin", Group: "management", DraftBehavior: "unavailable"},
		{Name: "plugins", Description: i18n.M.CmdPlugins, Kind: "builtin", Group: "integrations", DraftBehavior: "unavailable"},
		{Name: "theme", Description: i18n.M.CmdTheme, Kind: "builtin", Group: "management", DraftBehavior: "direct"},
		{Name: "skill", Description: i18n.M.CmdSkill, Kind: "builtin", Group: "skills", DraftBehavior: "unavailable"},
		{Name: "reload-cmd", Description: i18n.M.CmdReloadCmd, Kind: "builtin", Group: "management", DraftBehavior: "unavailable"},
		{Name: "reload", Description: i18n.M.CmdReload, Kind: "builtin", Group: "management", DraftBehavior: "unavailable"},
	}
}

func docsBuiltinCommand(name string) CommandInfo {
	return CommandInfo{Name: name, Description: i18n.M.CmdDocs, Hint: "<question>", Kind: "builtin", Group: "integrations"}
}

func resolveDocsCommand(commands []CommandInfo) []CommandInfo {
	winner := -1
	winnerRank := -1
	for i, cmd := range commands {
		if cmd.Name != "docs" {
			continue
		}
		rank := 0
		switch cmd.Kind {
		case "custom":
			rank = 2
		case "skill", "subagent":
			rank = 1
		}
		if rank > winnerRank {
			winner = i
			winnerRank = rank
		}
	}
	if winner < 0 {
		return commands
	}
	out := make([]CommandInfo, 0, len(commands))
	for i, cmd := range commands {
		if cmd.Name != "docs" || i == winner {
			out = append(out, cmd)
		}
	}
	return out
}

// CapabilitiesView is the MCP & Skills drawer's data: connected/failed MCP
// servers and the discoverable skills, the GUI counterpart to `/mcp` + `/skill`.
type CapabilitiesView struct {
	Servers    []ServerView    `json:"servers"`
	Skills     []SkillView     `json:"skills"`
	SkillRoots []SkillRootView `json:"skillRoots"`
	Plugins    []PluginView    `json:"plugins"`
}

// SkillsSettingsView is the skills management page's data, split from MCP
// status so opening MCP settings does not scan skill roots.
type SkillsSettingsView struct {
	Skills                  []SkillView     `json:"skills"`
	SkillRoots              []SkillRootView `json:"skillRoots"`
	AllowImplicitInvocation bool            `json:"allowImplicitInvocation"`
}

// ServerView is one MCP server for the drawer. Status is "connected" (with
// tool/prompt/resource counts), "deferred" (enabled but idle), "failed" (with
// the connection error), "initializing" (background startup in progress), or
// "disabled".
//
// Product fields for the simplified MCP panel are Enabled/Installed/
// Availability/RuntimeState/ToolCount/ToolList/Action. Status describes the
// host registration; RuntimeState also reflects the live session lifecycle.
type ServerView struct {
	Name                   string         `json:"name"`
	Transport              string         `json:"transport"`
	Status                 string         `json:"status"`
	HostProfile            string         `json:"hostProfile,omitempty"`
	ElicitationNegotiated  bool           `json:"elicitationNegotiated,omitempty"`
	AppsNegotiated         bool           `json:"appsNegotiated,omitempty"`
	RuntimeState           string         `json:"runtimeState"`
	ProtocolVersion        string         `json:"protocolVersion,omitempty"`
	SessionState           string         `json:"sessionState,omitempty"`
	ReconnectAttempts      int            `json:"reconnectAttempts,omitempty"`
	ErrorKind              string         `json:"errorKind,omitempty"`
	Availability           string         `json:"availability"`
	Enabled                bool           `json:"enabled"`
	Installed              bool           `json:"installed"`
	Action                 string         `json:"action,omitempty"`
	Source                 string         `json:"source,omitempty"`
	ConfigSource           string         `json:"configSource,omitempty"`
	BuiltIn                bool           `json:"builtIn,omitempty"`
	Configured             bool           `json:"configured,omitempty"`
	Command                string         `json:"command,omitempty"`
	Args                   []string       `json:"args,omitempty"`
	URL                    string         `json:"url,omitempty"`
	EnvKeys                []string       `json:"envKeys,omitempty"`
	HeaderKeys             []string       `json:"headerKeys,omitempty"`
	Tools                  int            `json:"tools"`
	ToolCount              int            `json:"toolCount"`
	Prompts                int            `json:"prompts"`
	Resources              int            `json:"resources"`
	HasTools               bool           `json:"hasTools,omitempty"`
	Error                  string         `json:"error,omitempty"`
	ToolList               []ToolView     `json:"toolList"`
	CallTimeoutSeconds     int            `json:"callTimeoutSeconds,omitempty"`
	ToolTimeoutSeconds     map[string]int `json:"toolTimeoutSeconds,omitempty"`
	RequiresLaunchApproval bool           `json:"requiresLaunchApproval,omitempty"`
	AuthStatus             string         `json:"authStatus,omitempty"`
	AuthURL                string         `json:"authUrl,omitempty"`
	AuthConfigured         bool           `json:"authConfigured,omitempty"`
	ManagedByPlugin        string         `json:"managedByPlugin,omitempty"`
}

type ToolView struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	ReadOnlyHint    bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint bool   `json:"destructiveHint,omitempty"`
	SchemaError     string `json:"schemaError,omitempty"`
}

// SkillView is one discoverable skill for the drawer. Also backs the
// Subagents settings surface: the frontend filters this same list to
// RunAs=="subagent" rather than calling a second, redundant endpoint.
type SkillView struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	SourceDir    string   `json:"sourceDir,omitempty"`
	RunAs        string   `json:"runAs"`
	Enabled      bool     `json:"enabled"`
	Plugin       string   `json:"plugin,omitempty"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	// ReadOnly mirrors frontmatter read-only; omitted/false keeps the legacy
	// writable default for older profiles.
	ReadOnly bool   `json:"readOnly,omitempty"`
	Color    string `json:"color,omitempty"`
	// Invocation is the user-facing slash name; InvocationMode preserves the
	// frontmatter policy used by the subagent profile editor.
	Invocation     string `json:"invocation,omitempty"`
	InvocationMode string `json:"invocationMode,omitempty"`
	// Body is the skill's full markdown body (post-frontmatter) — the
	// subagent profile editor pre-fills its system-prompt field from this.
	Body string `json:"body,omitempty"`
	// ConfiguredModel/ConfiguredEffort are the per-name overrides from
	// cfg.Agent.SubagentModels/SubagentEfforts (internal/boot's
	// subagentModelRef/subagentEffortRef read the same map at dispatch time).
	// This is the only lever for a built-in subagent's model/effort, since
	// built-ins have no editable frontmatter file to carry Model/Effort.
	ConfiguredModel  string `json:"configuredModel,omitempty"`
	ConfiguredEffort string `json:"configuredEffort,omitempty"`
}

type SkillRootSkillView struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	RunAs        string   `json:"runAs"`
	Plugin       string   `json:"plugin,omitempty"`
	Model        string   `json:"model,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	AllowedTools []string `json:"allowedTools,omitempty"`
	Color        string   `json:"color,omitempty"`
	Invocation   string   `json:"invocation,omitempty"`
}

// SkillRootView is one skill discovery root for the drawer's Sources section.
type SkillRootView struct {
	Dir        string               `json:"dir"`
	Scope      string               `json:"scope"`
	Priority   int                  `json:"priority"`
	Status     string               `json:"status"`
	Enabled    bool                 `json:"enabled"`
	Configured bool                 `json:"configured"`
	Removable  bool                 `json:"removable"`
	Skills     int                  `json:"skills"`
	SkillItems []SkillRootSkillView `json:"skillItems,omitempty"`
	Warning    string               `json:"warning,omitempty"`
}

// Capabilities projects the session's MCP servers (connected + failed) and skills
// for the MCP & Skills drawer. Non-nil slices so the frontend can map over them.
func (a *App) Capabilities() CapabilitiesView {
	skills := a.SkillsSettings()
	return CapabilitiesView{
		Servers:    a.MCPServers(),
		Skills:     skills.Skills,
		SkillRoots: skills.SkillRoots,
		Plugins:    a.Plugins(),
	}
}

// MCPServers returns only MCP server status for settings pages that do not need
// skill discovery.
func (a *App) MCPServers() []ServerView {
	return a.mcpServersView()
}

type MCPMarketplaceEntryView struct {
	Name              string   `json:"name"`
	SuggestedName     string   `json:"suggestedName"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Version           string   `json:"version,omitempty"`
	RepositoryURL     string   `json:"repositoryUrl,omitempty"`
	Installable       bool     `json:"installable"`
	UnavailableReason string   `json:"unavailableReason,omitempty"`
	Transport         string   `json:"transport,omitempty"`
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args"`
	URL               string   `json:"url,omitempty"`
}

type MCPMarketplaceView struct {
	Servers []MCPMarketplaceEntryView `json:"servers"`
	Cached  bool                      `json:"cached"`
	Warning string                    `json:"warning,omitempty"`
}

// MCPMarketplace explicitly queries the official MCP Registry. It is only
// called from the settings marketplace; startup and tool discovery never touch
// the network. A query-specific cache keeps the page useful during a registry
// outage without treating cached entries as installed servers.
func (a *App) MCPMarketplace(query string) (MCPMarketplaceView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := mcpregistry.New(mcpRegistryCachePath()).Search(ctx, query, 50)
	if err != nil {
		return MCPMarketplaceView{Servers: []MCPMarketplaceEntryView{}}, err
	}
	view := MCPMarketplaceView{
		Servers: make([]MCPMarketplaceEntryView, 0, len(result.Entries)),
		Cached:  result.Cached,
		Warning: result.Warning,
	}
	for _, entry := range result.Entries {
		view.Servers = append(view.Servers, mcpMarketplaceEntryView(entry))
	}
	return view, nil
}

// MCPMarketplaceResolve re-fetches one Registry entry immediately before the
// settings UI installs it. Offline cache remains useful for browsing, but it is
// never accepted as installation metadata.
func (a *App) MCPMarketplaceResolve(registryName string) (MCPMarketplaceEntryView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entry, _, err := mcpregistry.New(mcpRegistryCachePath()).Resolve(ctx, registryName)
	if err != nil {
		return MCPMarketplaceEntryView{}, err
	}
	if _, err := entry.PluginEntry(""); err != nil {
		return MCPMarketplaceEntryView{}, err
	}
	return mcpMarketplaceEntryView(entry), nil
}

func mcpRegistryCachePath() string {
	if cacheDir := config.CacheDir(); cacheDir != "" {
		return filepath.Join(cacheDir, "mcp-registry-v0.1.json")
	}
	return ""
}

func mcpMarketplaceEntryView(entry mcpregistry.Entry) MCPMarketplaceEntryView {
	return MCPMarketplaceEntryView{
		Name:              entry.Name,
		SuggestedName:     entry.SuggestedName,
		Title:             entry.Title,
		Description:       entry.Description,
		Version:           entry.Version,
		RepositoryURL:     entry.RepositoryURL,
		Installable:       entry.Installable,
		UnavailableReason: entry.UnavailableReason,
		Transport:         entry.Transport,
		Command:           entry.Command,
		Args:              append([]string{}, entry.Args...),
		URL:               entry.URL,
	}
}

// lockRuntimeMutation serializes controller rebuild/teardown operations and
// freezes runtime admission so a captured controller or Host cannot be replaced
// or closed in flight. The caller must not hold App.mu; the lock order is
// runtimeRebuildMu -> runtimeAdmissionMu -> App/Host/Registry.
func (a *App) lockRuntimeMutation(operation string) func() {
	if hook := a.runtimeMutationBeforeLockHook; hook != nil {
		hook(operation)
	}
	a.runtimeRebuildMu.Lock()
	a.runtimeAdmissionMu.Lock()
	return func() {
		a.runtimeAdmissionMu.Unlock()
		a.runtimeRebuildMu.Unlock()
	}
}

type mcpControllerTarget struct {
	ctrl    control.SessionAPI
	enabled bool
}

// lockMCPHostTurnGates freezes every runtime sharing ctrl's Host. Callers hold
// lockMCPMutation, so runtimeAdmissionMu's write side already prevents new turn
// admissions, builds, and teardown while this helper snapshots and gates the
// existing runtimes.
func (a *App) lockMCPHostTurnGates(setting string, ctrl control.SessionAPI) (*plugin.Host, func(), error) {
	if ctrl == nil {
		return nil, nil, fmt.Errorf("no active session")
	}
	host := ctrl.Host()
	release, err := a.lockRuntimeTurnGates(setting, func(tab *WorkspaceTab) bool {
		if host == nil {
			return tab.Ctrl == ctrl
		}
		return tab.Ctrl != nil && tab.Ctrl.Host() == host
	})
	return host, release, err
}

func disconnectMCPServerControllers(name string, preferred control.SessionAPI, controllers []mcpControllerTarget) bool {
	for _, target := range controllers {
		target.ctrl.UnregisterMCPServerTools(name)
	}
	disconnected := false
	if preferred != nil {
		disconnected = preferred.DisconnectMCPServer(name)
	}
	// Every controller owns an independent capability runtime even when the Host
	// process is shared. Reconcile each one after the preferred controller drops
	// the client so remove/update/rollback cannot leave sibling tabs with stale
	// specs or live-tool snapshots.
	for _, target := range controllers {
		if target.ctrl == preferred {
			continue
		}
		disconnected = target.ctrl.DisconnectMCPServer(name) || disconnected
	}
	return disconnected
}

func (a *App) clearMCPServerTabState(name string, controllers []mcpControllerTarget) {
	selected := make(map[control.SessionAPI]bool, len(controllers))
	for _, target := range controllers {
		selected[target.ctrl] = true
	}
	a.mu.Lock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || !selected[tab.Ctrl] {
			continue
		}
		delete(tab.disabledMCP, name)
		tab.mcpOrder = removeServerOrder(tab.mcpOrder, name)
	}
	a.mu.Unlock()
}

// reconnectMCPServerControllers establishes one shared client, then refreshes
// every enabled controller's provider-visible Registry. Disabled tabs remain
// suspended and reconnect only when explicitly enabled.
func reconnectMCPServerControllers(entry config.PluginEntry, controllers []mcpControllerTarget) error {
	var startErrors []error
	connectedTarget := -1
	for i, target := range controllers {
		if !target.enabled {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			startErrors = append(startErrors, err)
			continue
		}
		connectedTarget = i
		break
	}
	if connectedTarget < 0 {
		// All tabs may have disabled this server. Keeping it disconnected
		// preserves their explicit state.
		return errors.Join(startErrors...)
	}

	var refreshErrors []error
	for i, target := range controllers {
		if !target.enabled || i == connectedTarget {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			refreshErrors = append(refreshErrors, err)
		}
	}
	return errors.Join(refreshErrors...)
}

// mcpControllersSharingHost snapshots visible and detached runtimes before
// calling controller methods. App.mu is never held across Host/controller
// locks or network work. preferred (normally the active tab) is returned first.
func (a *App) mcpControllersSharingHost(host *plugin.Host, name string, preferred control.SessionAPI) []mcpControllerTarget {
	if host == nil {
		enabled := true
		a.mu.RLock()
		for _, tab := range a.runtimeTabsLocked() {
			if tab != nil && tab.Ctrl == preferred {
				_, disabled := tab.disabledMCP[name]
				enabled = !disabled
				break
			}
		}
		a.mu.RUnlock()
		return []mcpControllerTarget{{ctrl: preferred, enabled: enabled}}
	}
	a.mu.RLock()
	candidates := make([]mcpControllerTarget, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil {
			continue
		}
		_, disabled := tab.disabledMCP[name]
		candidates = append(candidates, mcpControllerTarget{ctrl: tab.Ctrl, enabled: !disabled})
	}
	a.mu.RUnlock()

	byController := make(map[control.SessionAPI]int, len(candidates))
	targets := make([]mcpControllerTarget, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ctrl.Host() != host {
			continue
		}
		if idx, ok := byController[candidate.ctrl]; ok {
			targets[idx].enabled = targets[idx].enabled || candidate.enabled
			continue
		}
		byController[candidate.ctrl] = len(targets)
		targets = append(targets, candidate)
	}
	if len(targets) == 0 {
		return []mcpControllerTarget{{ctrl: preferred, enabled: true}}
	}
	if idx, ok := byController[preferred]; ok && idx > 0 {
		targets[0], targets[idx] = targets[idx], targets[0]
	}
	return targets
}

// lockRuntimeTurnGates locks the turn gate of every runtime tab selected by
// affected (nil selects all visible and detached runtime tabs) in stable tab-ID
// order, then verifies under the gates that no gated controller has active
// runtime work. Callers must hold runtimeRebuildMu and the write side of
// runtimeAdmissionMu (normally through lockMCPMutation), which freezes new turn
// admission, controller builds, and runtime teardown before this snapshot.
// On success the returned release func unlocks the per-tab gates in reverse
// order; on error every gate acquired here is already unlocked.
func (a *App) lockRuntimeTurnGates(setting string, affected func(*WorkspaceTab) bool) (func(), error) {
	a.mu.RLock()
	all := a.runtimeTabsLocked()
	tabs := make([]*WorkspaceTab, 0, len(all))
	for _, tab := range all {
		if tab == nil || (affected != nil && !affected(tab)) {
			continue
		}
		tabs = append(tabs, tab)
	}
	a.mu.RUnlock()
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].ID < tabs[j].ID })
	locked := 0
	release := func() {
		for i := locked - 1; i >= 0; i-- {
			tabs[i].turnStartMu.Unlock()
		}
	}
	for _, tab := range tabs {
		tab.turnStartMu.Lock()
		locked++
	}
	// Read tab.Ctrl under a.mu rather than through controllerForTab: detached
	// runtimes live in detachedSessions, not a.tabs, and their work counts too.
	a.mu.RLock()
	for _, tab := range tabs {
		if err := rebuildControllerActiveWorkErrorFor(tab.Ctrl, setting); err != nil {
			a.mu.RUnlock()
			release()
			return nil, err
		}
	}
	a.mu.RUnlock()
	return release, nil
}

// disconnectMCPServerAllRuntimes removes an uninstalled MCP server from every
// live runtime: all visible and detached runtime tabs, across every shared
// Host — a global plugin uninstall must not leave sibling tabs exposing stale
// provider-visible tools or other workspaces running the removed server.
// DisconnectMCPServer stops the shared client once per Host and drops the tool
// prefix from every other controller's registry.
func (a *App) disconnectMCPServerAllRuntimes(serverName string) bool {
	a.mu.RLock()
	ctrls := make([]control.SessionAPI, 0, len(a.tabs)+len(a.detachedSessions))
	seen := make(map[control.SessionAPI]bool, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.Ctrl == nil || seen[tab.Ctrl] {
			continue
		}
		seen[tab.Ctrl] = true
		ctrls = append(ctrls, tab.Ctrl)
	}
	a.mu.RUnlock()
	disconnected := false
	for _, ctrl := range ctrls {
		if ctrl.DisconnectMCPServer(serverName) {
			disconnected = true
		}
	}
	return disconnected
}

// SkillsSettings returns the skills management snapshot without MCP status.
func (a *App) SkillsSettings() SkillsSettingsView {
	out := SkillsSettingsView{Skills: []SkillView{}, SkillRoots: []SkillRootView{}, AllowImplicitInvocation: true}
	a.mu.RLock()
	tab := a.activeTabLocked()
	var ctrl control.SessionAPI
	workspaceRoot := "."
	if tab != nil {
		ctrl = tab.Ctrl
		if strings.TrimSpace(tab.WorkspaceRoot) != "" {
			workspaceRoot = tab.WorkspaceRoot
		}
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return out
	}

	disabled := map[string]bool{}
	var configuredModels, configuredEfforts map[string]string
	if cfg, err := config.LoadForRootReadOnly(workspaceRoot); err == nil {
		out.AllowImplicitInvocation = cfg.ImplicitSkillInvocationEnabled()
		for _, name := range cfg.Skills.DisabledSkills {
			if key := config.SkillNameKey(name); key != "" {
				disabled[key] = true
			}
		}
		configuredModels = cfg.Agent.SubagentModels
		configuredEfforts = cfg.Agent.SubagentEfforts
	}
	out.SkillRoots = a.cachedSkillRootsView(workspaceRoot)
	for _, s := range ctrl.AllSkills() {
		view := SkillView{
			Name: s.Name, Description: s.Description,
			Scope: string(s.Scope), SourceDir: skillSourceDir(s, out.SkillRoots), RunAs: string(s.RunAs),
			Enabled:          !disabled[config.SkillNameKey(s.Name)],
			Plugin:           s.Plugin,
			Model:            s.Model,
			Effort:           s.Effort,
			AllowedTools:     append([]string{}, s.AllowedTools...),
			ReadOnly:         s.ReadOnly,
			Color:            s.Color,
			Invocation:       "/" + s.SlashName(),
			InvocationMode:   s.Invocation,
			ConfiguredModel:  subagentOverrideFor(configuredModels, s.Name),
			ConfiguredEffort: subagentOverrideFor(configuredEfforts, s.Name),
		}
		// Body feeds only the Subagents editor's prompt prefill. Inline skills
		// fold references/ into Body at load time (hundreds of KB for a rich
		// skill library), and every Capabilities/Settings fetch would ship all
		// of it across the JSON bridge for nothing.
		if s.RunAs == skill.RunSubagent {
			if loaded, ok := ctrl.LoadSkill(s.Name); ok {
				view.Body = loaded.Body
			}
		}
		out.Skills = append(out.Skills, view)
	}
	return out
}

// SetSkillImplicitInvocation persists whether the model may discover and
// invoke skills automatically, then rebuilds the active runtime. Explicit
// /skill invocation and skill management remain available in either mode.
func (a *App) SetSkillImplicitInvocation(enabled bool) error {
	err := a.applySkillConfigChange("disable_implicit_invocation", "skills policy", func(c *config.Config) error {
		c.SetSkillImplicitInvocation(enabled)
		return nil
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// subagentOverrideFor resolves a per-name subagent override with the same
// underscore/hyphen alias fallback the runtime dispatch uses
// (boot.SubagentModelKeys) — an exact-key read would show a legacy
// `security_review` config entry as "inherit default" while it still won at
// dispatch time.
func subagentOverrideFor(overrides map[string]string, name string) string {
	for _, key := range boot.SubagentModelKeys(name) {
		if v := strings.TrimSpace(overrides[key]); v != "" {
			return v
		}
	}
	return ""
}

// AvailableSubagentTools lists the tool names a subagent profile's
// "available tools" picker may offer. Scoped to compile-time builtins for
// v1 — MCP/plugin tools are per-session/per-connection and would need a new
// live-registry accessor on control.Capabilities to enumerate safely; a
// profile's allowed-tools already degrades gracefully (FilterRegistry drops
// unknown names silently) if extended to MCP names by hand later. Tools that
// are always excluded from every subagent regardless of an explicit
// allowlist (agent.AlwaysHiddenSubagentTools) are left out entirely — they'd
// be a selectable no-op otherwise.
func (a *App) AvailableSubagentTools() []ToolView {
	hidden := map[string]bool{}
	for _, name := range agent.AlwaysHiddenSubagentTools() {
		hidden[name] = true
	}
	entries := tool.BuiltinContractEntries()
	out := make([]ToolView, 0, len(entries))
	for _, e := range entries {
		if hidden[e.Name] {
			continue
		}
		out = append(out, ToolView{Name: e.Name, Description: e.Description, ReadOnlyHint: e.ReadOnly})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *App) mcpServersView() []ServerView {
	out := []ServerView{}
	a.mu.RLock()
	tab := a.activeTabLocked()
	if tab == nil {
		a.mu.RUnlock()
		return out
	}
	ctrl := tab.Ctrl
	disabled := make(map[string]ServerView, len(tab.disabledMCP))
	maps.Copy(disabled, tab.disabledMCP)
	order := append([]string(nil), tab.mcpOrder...)
	workspaceRoot := tab.WorkspaceRoot
	tabID := tab.ID
	a.mu.RUnlock()
	if ctrl == nil {
		return out
	}
	seen := map[string]bool{}
	connected := map[string]bool{}
	retainedDisabled := map[string]ServerView{}
	configured := map[string]config.PluginEntry{}
	managedByPlugin := map[string]string{}
	var configuredEntries []config.PluginEntry
	if cfg, err := config.LoadForRoot(workspaceRoot); err == nil {
		configuredEntries = append(configuredEntries, cfg.Plugins...)
		for _, p := range configuredEntries {
			configured[p.Name] = p
			if owner, ok := cfg.PluginPackageOwner(p.Name); ok {
				managedByPlugin[p.Name] = owner
			}
		}
	}
	if h := ctrl.Host(); h != nil {
		for _, s := range h.Servers() {
			if disabledView, ok := disabled[s.Name]; ok {
				disabledView.Status = "disabled"
				disabledView.RuntimeState = "idle"
				disabledView.Error = ""
				if p, ok := configured[s.Name]; ok {
					disabledView = withPluginConfigInWorkspace(disabledView, p, workspaceRoot)
				}
				out = append(out, disabledView)
				retainedDisabled[s.Name] = disabledView
				seen[s.Name] = true
				delete(disabled, s.Name)
				continue
			}
			seen[s.Name] = true
			connected[s.Name] = true
			view := pluginServerToView(s)
			if p, ok := configured[s.Name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
		for _, f := range h.Failures() {
			seen[f.Name] = true
			view := ServerView{
				Name: f.Name, Transport: f.Transport, Status: "failed", RuntimeState: "issue", Error: f.Error,
				RequiresLaunchApproval: f.RequiresLaunchApproval,
			}
			if p, ok := configured[f.Name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
		for _, name := range h.ConnectingServers() {
			if seen[name] {
				continue
			}
			seen[name] = true
			view := ServerView{Name: name, Status: "initializing", RuntimeState: "connecting"}
			if p, ok := configured[name]; ok {
				view = withPluginConfigInWorkspace(view, p, workspaceRoot)
			}
			out = append(out, view)
		}
	}
	// Configured servers that are neither connected, connecting, nor failed are
	// idle: disabled/off or automatic background startup waiting for its next kick.
	if len(configuredEntries) > 0 {
		for _, p := range configuredEntries {
			if seen[p.Name] {
				continue
			}
			if s, ok := disabled[p.Name]; ok {
				s.Status = "disabled"
				s.RuntimeState = "idle"
				s = withPluginConfigInWorkspace(s, p, workspaceRoot)
				s.Error = ""
				out = append(out, s)
				retainedDisabled[p.Name] = s
				seen[p.Name] = true
				delete(disabled, p.Name)
				continue
			}
			status := "disabled"
			if mcpEntryEnabled(p, workspaceRoot) {
				status = "deferred"
			}
			out = append(out, withPluginConfigInWorkspace(ServerView{Name: p.Name, Status: status, RuntimeState: "idle"}, p, workspaceRoot))
			seen[p.Name] = true
		}
	}
	out = orderServerViews(out, order)
	for i := range out {
		out[i].ManagedByPlugin = managedByPlugin[out[i].Name]
		out[i] = finalizeServerView(out[i])
	}

	a.mu.Lock()
	if tab, ok := a.tabs[tabID]; ok {
		for name := range connected {
			delete(retainedDisabled, name)
		}
		tab.disabledMCP = retainedDisabled
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, out)
	}
	a.mu.Unlock()
	return out
}

func mcpEntryEnabled(p config.PluginEntry, workspace string) bool {
	return config.MCPServerEnabled(p, workspace)
}

func mcpRuntimeState(status string) string {
	switch status {
	case "connected":
		return "ready"
	case "initializing":
		return "connecting"
	case "failed":
		return "issue"
	default:
		return "idle"
	}
}

func mcpAvailability(v ServerView) string {
	if !v.Enabled {
		return "disabled"
	}
	switch v.RuntimeState {
	case "ready":
		return "connected"
	case "connecting":
		return "starting"
	case "issue":
		if v.RequiresLaunchApproval {
			return "project_auth_changed"
		}
		if v.AuthStatus == "required" || v.AuthStatus == "possible" {
			return "auth_required"
		}
		return "start_failed"
	default:
		// Idle enabled servers are available on demand, not "disconnected".
		return "available_on_demand"
	}
}

func mcpActionForView(v ServerView) string {
	if v.RequiresLaunchApproval {
		return "authorize"
	}
	if v.AuthStatus == "required" {
		return "authenticate"
	}
	if v.RuntimeState == "issue" {
		return "retry"
	}
	return "none"
}

func finalizeServerView(v ServerView) ServerView {
	if v.ToolList == nil {
		v.ToolList = []ToolView{}
	}
	if v.Args == nil {
		v.Args = []string{}
	}
	if v.EnvKeys == nil {
		v.EnvKeys = []string{}
	}
	if v.HeaderKeys == nil {
		v.HeaderKeys = []string{}
	}
	v.ToolCount = v.Tools
	if v.ToolCount == 0 && len(v.ToolList) > 0 {
		v.ToolCount = len(v.ToolList)
		v.Tools = v.ToolCount
	}
	v.Installed = v.Configured || v.BuiltIn || v.Status != ""
	if v.Source == "" {
		switch {
		case v.BuiltIn:
			v.Source = "builtin"
		case v.ManagedByPlugin != "":
			v.Source = "plugin"
		case v.Configured:
			v.Source = "user"
		}
	}
	if v.RuntimeState == "" {
		v.RuntimeState = mcpRuntimeState(v.Status)
	}
	v.Availability = mcpAvailability(v)
	if v.Action == "" {
		v.Action = mcpActionForView(v)
	}
	return v
}

func withPluginConfig(v ServerView, p config.PluginEntry) ServerView {
	return withPluginConfigInWorkspace(v, p, "")
}

func withPluginConfigInWorkspace(v ServerView, p config.PluginEntry, workspace string) ServerView {
	tt := p.Type
	if tt == "" {
		tt = "stdio"
	}
	v.Transport = tt
	v.Configured = true
	v.Installed = true
	v.Source, v.ConfigSource = mcpServerSource(p.Source)
	v.Enabled = mcpEntryEnabled(p, workspace)
	if !v.Enabled || v.Status == "disabled" {
		v.Status = "disabled"
		v.RuntimeState = "idle"
	}
	if v.RuntimeState == "" {
		v.RuntimeState = mcpRuntimeState(v.Status)
	}
	v.Command = p.Command
	v.Args = append([]string(nil), p.Args...)
	v.URL = p.URL
	v.CallTimeoutSeconds = p.CallTimeoutSeconds
	v.ToolTimeoutSeconds = cloneStringIntMap(p.ToolTimeoutSeconds)
	// Configured MCP entries are explicit installs, including project sources.
	v.RequiresLaunchApproval = false
	v.AuthConfigured = mcpdiag.HasAuthConfig(p.Headers, p.Env, p.URL)
	v.EnvKeys = nil
	v.HeaderKeys = nil
	if len(p.Env) > 0 {
		v.EnvKeys = make([]string, 0, len(p.Env))
		for k := range p.Env {
			v.EnvKeys = append(v.EnvKeys, k)
		}
		sort.Strings(v.EnvKeys)
	}
	if len(p.Headers) > 0 {
		v.HeaderKeys = make([]string, 0, len(p.Headers))
		for k := range p.Headers {
			v.HeaderKeys = append(v.HeaderKeys, k)
		}
		sort.Strings(v.HeaderKeys)
	}
	auth := mcpdiag.DiagnoseAuth(v.Transport, v.Status, v.Error, v.URL, v.AuthConfigured)
	v.AuthStatus = auth.Status
	v.AuthURL = auth.URL
	return v
}

func mcpServerSource(source config.MCPConfigSource) (kind, configSource string) {
	switch source {
	case config.MCPSourceProjectConfig:
		return "project", "deepzora.toml"
	case config.MCPSourceProjectMCPJSON:
		return "project", ".mcp.json"
	case config.MCPSourcePluginPackage:
		return "plugin", "plugin"
	case config.MCPSourceUserConfig:
		return "user", "config.toml"
	default:
		return "", ""
	}
}

const skillRootsCacheTTL = 10 * time.Second

func (a *App) cachedSkillRootsView(workspaceRoots ...string) []SkillRootView {
	workspaceRoot := "."
	if len(workspaceRoots) > 0 {
		workspaceRoot = workspaceRoots[0]
	}
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	cfg, _ := config.LoadForRootReadOnly(workspaceRoot)
	userCfg := config.LoadForEdit(config.UserConfigPath())
	key := skillRootsCacheKey(workspaceRoot, cfg, userCfg)

	now := time.Now()
	a.skillRootsMu.Lock()
	if a.skillRootsCache.key == key && now.Sub(a.skillRootsCache.at) < skillRootsCacheTTL {
		roots := cloneSkillRootViews(a.skillRootsCache.roots)
		a.skillRootsMu.Unlock()
		return roots
	}
	a.skillRootsMu.Unlock()

	roots := skillRootsViewFrom(workspaceRoot, cfg, userCfg)

	a.skillRootsMu.Lock()
	a.skillRootsCache = skillRootsCache{
		key:   key,
		at:    now,
		roots: cloneSkillRootViews(roots),
	}
	a.skillRootsMu.Unlock()
	return roots
}

func (a *App) invalidateSkillRootsCache() {
	a.skillRootsMu.Lock()
	a.skillRootsCache = skillRootsCache{}
	a.skillRootsMu.Unlock()
}

func skillRootsViewFrom(workspaceRoot string, cfg, userCfg *config.Config) []SkillRootView {
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	var custom []string
	var excluded []string
	maxDepth := 3
	if cfg != nil {
		custom = cfg.SkillCustomPaths()
		excluded = cfg.SkillExcludedPaths()
		maxDepth = cfg.SkillMaxDepth()
	}
	var pluginPaths map[string][]string
	var pluginAgentPaths map[string][]string
	if cfg != nil {
		pluginPaths = cfg.PluginPackageSkillOwners()
		pluginAgentPaths = cfg.PluginPackageAgentOwners()
	}
	st := skill.New(skill.Options{ProjectRoot: workspaceRoot, CustomPaths: custom, PluginPaths: pluginPaths, PluginAgentPaths: pluginAgentPaths, ExcludedPaths: excluded, MaxDepth: maxDepth, DisableBuiltins: true, Stderr: io.Discard})
	counts := map[string]int{}
	skillItems := map[string][]SkillRootSkillView{}
	roots := st.Roots()
	for _, sk := range st.SlashList() {
		root := skillDisplayRoot(sk, roots)
		counts[root]++
		skillItems[root] = append(skillItems[root], SkillRootSkillView{
			Name:         sk.Name,
			Description:  sk.Description,
			Scope:        string(sk.Scope),
			RunAs:        string(sk.RunAs),
			Plugin:       sk.Plugin,
			Model:        sk.Model,
			Effort:       sk.Effort,
			AllowedTools: append([]string{}, sk.AllowedTools...),
			Color:        sk.Color,
			Invocation:   "/" + sk.SlashName(),
		})
	}
	for root := range skillItems {
		sort.Slice(skillItems[root], func(i, j int) bool {
			return skillItems[root][i].Invocation < skillItems[root][j].Invocation
		})
	}
	userConfigured := map[string]bool{}
	if userCfg != nil {
		for _, p := range userCfg.Skills.Paths {
			userConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
	}
	effectiveConfigured := map[string]bool{}
	effectiveExcluded := map[string]bool{}
	if cfg != nil {
		for _, p := range cfg.Skills.Paths {
			effectiveConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
		for _, p := range cfg.Skills.ExcludedPaths {
			effectiveExcluded[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
	}
	out := []SkillRootView{}
	seenRoots := map[string]int{}
	for _, r := range roots {
		dir := canonicalSkillPathForRoot(r.Dir, workspaceRoot)
		view := SkillRootView{
			Dir:        r.Dir,
			Scope:      string(r.Scope),
			Priority:   r.Priority + 1,
			Status:     string(r.Status),
			Enabled:    true,
			Configured: r.Scope == skill.ScopeCustom && (userConfigured[dir] || effectiveConfigured[dir]),
			Removable:  true,
			Skills:     counts[dir],
			SkillItems: skillItems[dir],
		}
		if idx, ok := seenRoots[dir]; ok {
			out[idx] = mergeDuplicateSkillRootView(out[idx], view)
			continue
		}
		seenRoots[dir] = len(out)
		out = append(out, view)
	}
	if cfg != nil {
		for _, p := range cfg.Skills.Paths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			dir := canonicalSkillPathForRoot(p, workspaceRoot)
			enabled := !effectiveExcluded[dir]
			status := "inactive"
			warning := "configured in project/user config but not active in this workspace"
			if !enabled {
				status = "disabled"
				warning = ""
			}
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: dir, Scope: string(skill.ScopeCustom), Status: status, Enabled: enabled,
				Configured: true, Removable: true, Warning: warning,
			}, workspaceRoot)
		}
		for _, p := range cfg.Skills.ExcludedPaths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			dir := canonicalSkillPathForRoot(p, workspaceRoot)
			scope := skillRootScopeForPath(p, workspaceRoot)
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: dir, Scope: string(scope), Status: "disabled", Enabled: false,
				Configured: scope == skill.ScopeCustom || effectiveConfigured[dir], Removable: true,
			}, workspaceRoot)
		}
	}
	if userCfg != nil {
		userExcluded := map[string]bool{}
		for _, p := range userCfg.Skills.ExcludedPaths {
			userExcluded[canonicalSkillPathForRoot(p, workspaceRoot)] = true
		}
		for _, p := range userCfg.Skills.Paths {
			if rootActive(out, p, workspaceRoot) {
				continue
			}
			enabled := !userExcluded[canonicalSkillPathForRoot(p, workspaceRoot)]
			status := "inactive"
			warning := "configured in user config but not active in this workspace; project [skills].paths may override it"
			if !enabled {
				status = "disabled"
				warning = ""
			}
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir:        canonicalSkillPathForRoot(p, workspaceRoot),
				Scope:      string(skill.ScopeCustom),
				Status:     status,
				Enabled:    enabled,
				Configured: true,
				Removable:  true,
				Warning:    warning,
			}, workspaceRoot)
		}
		for _, p := range userCfg.Skills.ExcludedPaths {
			if rootActive(out, p, workspaceRoot) || userConfigured[canonicalSkillPathForRoot(p, workspaceRoot)] {
				continue
			}
			scope := skillRootScopeForPath(p, workspaceRoot)
			appendSkillRootView(&out, &seenRoots, SkillRootView{
				Dir: canonicalSkillPathForRoot(p, workspaceRoot), Scope: string(scope), Status: "disabled", Enabled: false,
				Configured: scope == skill.ScopeCustom, Removable: true,
			}, workspaceRoot)
		}
	}
	return out
}

func appendSkillRootView(out *[]SkillRootView, seen *map[string]int, view SkillRootView, workspaceRoot string) {
	dir := canonicalSkillPathForRoot(view.Dir, workspaceRoot)
	if idx, ok := (*seen)[dir]; ok {
		(*out)[idx] = mergeDuplicateSkillRootView((*out)[idx], view)
		return
	}
	(*seen)[dir] = len(*out)
	*out = append(*out, view)
}

func mergeDuplicateSkillRootView(existing, duplicate SkillRootView) SkillRootView {
	existing.Configured = existing.Configured || duplicate.Configured
	existing.Removable = existing.Removable || duplicate.Removable
	if existing.Status != "ok" && duplicate.Status == "ok" {
		existing.Status = duplicate.Status
		existing.Enabled = duplicate.Enabled
	}
	if existing.Skills == 0 && duplicate.Skills > 0 {
		existing.Skills = duplicate.Skills
		existing.SkillItems = duplicate.SkillItems
	}
	if existing.Warning == "" {
		existing.Warning = duplicate.Warning
	}
	return existing
}

func skillRootsCacheKey(workspaceRoot string, cfg, userCfg *config.Config) string {
	type cacheKey struct {
		CWD       string   `json:"cwd"`
		Custom    []string `json:"custom"`
		Plugins   []string `json:"plugins"`
		Excluded  []string `json:"excluded"`
		MaxDepth  int      `json:"maxDepth"`
		UserPaths []string `json:"userPaths"`
	}
	workspaceRoot = normalizeWorkspaceRoot(workspaceRoot)
	key := cacheKey{CWD: canonicalSkillPathForRoot(workspaceRoot, workspaceRoot), MaxDepth: 3}
	if cfg != nil {
		key.Custom = canonicalSkillPathsForRoot(cfg.SkillCustomPaths(), workspaceRoot)
		for path, owners := range cfg.PluginPackageSkillOwners() {
			for _, owner := range owners {
				key.Plugins = append(key.Plugins, canonicalSkillPathForRoot(path, workspaceRoot)+"\x00"+owner)
			}
		}
		sort.Strings(key.Plugins)
		key.Excluded = canonicalSkillPathsForRoot(cfg.SkillExcludedPaths(), workspaceRoot)
		key.MaxDepth = cfg.SkillMaxDepth()
	}
	if userCfg != nil {
		key.UserPaths = canonicalSkillPathsForRoot(userCfg.Skills.Paths, workspaceRoot)
	}
	b, err := json.Marshal(key)
	if err != nil {
		return fmt.Sprintf("%s|%v|%v|%v|%d|%v", key.CWD, key.Custom, key.Plugins, key.Excluded, key.MaxDepth, key.UserPaths)
	}
	return string(b)
}

func canonicalSkillPathsForRoot(paths []string, workspaceRoot string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, canonicalSkillPathForRoot(p, workspaceRoot))
	}
	sort.Strings(out)
	return out
}

func normalizeWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(root)
}

// canonicalSkillPathForRoot mirrors skill.Store's path resolution while keeping
// comparisons independent of the desktop process CWD. Config may intentionally
// contain relative paths; those are relative to the active workspace.
func canonicalSkillPathForRoot(path, workspaceRoot string) string {
	path = config.ExpandVars(strings.TrimSpace(path))
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(normalizeWorkspaceRoot(workspaceRoot), path)
	}
	return config.CanonicalSkillPath(path)
}

func cloneSkillRootViews(in []SkillRootView) []SkillRootView {
	out := make([]SkillRootView, len(in))
	for i, r := range in {
		out[i] = r
		out[i].SkillItems = append([]SkillRootSkillView(nil), r.SkillItems...)
	}
	return out
}

func rootActive(roots []SkillRootView, path string, workspaceRoots ...string) bool {
	workspaceRoot := "."
	if len(workspaceRoots) > 0 {
		workspaceRoot = workspaceRoots[0]
	}
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	for _, r := range roots {
		if canonicalSkillPathForRoot(r.Dir, workspaceRoot) == want {
			return true
		}
	}
	return false
}

// PickSkillFolder opens a directory picker for adding custom skill roots. It only
// returns a path; AddSkillPath performs normalization and writes config.
func (a *App) PickSkillFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur, _ := os.Getwd()
	dir, err := a.nativeHost().OpenDirectoryDialog(a.ctx, nativeDialogOptions{
		Title:            "Choose skills folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return normalizeSkillPath(dir), nil
}

// PickPluginFolder opens a directory picker for choosing a local plugin package
// source. It returns the selected directory path; plugin install/plan performs
// manifest validation and decides whether to copy or link the package.
func (a *App) PickPluginFolder() (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	cur := a.activeWorkspaceRoot()
	if strings.TrimSpace(cur) == "" {
		cur, _ = os.Getwd()
	}
	dir, err := a.nativeHost().OpenDirectoryDialog(a.ctx, nativeDialogOptions{
		Title:            "Choose plugin folder",
		DefaultDirectory: dialogDefaultDirectory(cur),
	})
	if err != nil || dir == "" {
		return "", err
	}
	return filepath.Clean(dir), nil
}

// AddSkillPath adds a custom skill root to the user config and rebuilds the
// controller so the skills index and slash menu reflect it immediately.
func (a *App) AddSkillPath(path string) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		if isConventionSkillRoot(path, workspaceRoot) {
			return c.RestoreSkillPath(path)
		}
		return c.AddSkillPath(path)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// RemoveSkillPath removes a skill source from the user config and rebuilds. For
// convention roots, it records a pseudo-delete in excluded_paths.
func (a *App) RemoveSkillPath(path string) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		removed, err := c.RemoveSkillPath(path)
		if err != nil || removed {
			return err
		}
		return c.ExcludeSkillPath(path)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// SetSkillPathEnabled persists a reversible source toggle and rebuilds the
// controller so the source is immediately included or excluded from discovery.
func (a *App) SetSkillPathEnabled(path string, enabled bool) error {
	path = normalizeSkillPath(path)
	workspaceRoot := a.activeWorkspaceRoot()
	field := "paths"
	if isConventionSkillRoot(path, workspaceRoot) {
		field = "excluded_paths"
	}
	err := a.applySkillConfigChange(field, "skills source", func(c *config.Config) error {
		return c.SetSkillPathEnabled(path, enabled)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

// RefreshSkills rebuilds the controller without changing config, reloading skill
// discovery, the system prompt index, and slash completions.
func (a *App) RefreshSkills() error {
	a.invalidateSkillRootsCache()
	if err := a.rebuild(); err != nil {
		// The skill cache is already invalidated; refresh the runtime once the
		// other window releases the session lease.
		if _, ok := a.deferredRebuildWarning("skills", err); ok {
			return nil
		}
		return err
	}
	return nil
}

// ReloadCommands rescans command directories and hot-swaps without restarting
// the controller — no MCP disconnect, no hook rerun.
func (a *App) ReloadCommands() error {
	if a.ctx == nil {
		return nil
	}
	_, ctrl := a.activeTabAndCtrl()
	if ctrl == nil {
		return fmt.Errorf("no active session")
	}
	if ctrl.Running() {
		return fmt.Errorf("wait for the current turn to finish, then retry")
	}
	return ctrl.ReloadCommands(a.ctx)
}

// SetSkillEnabled persists a skill toggle and rebuilds the controller so the
// prompt index, slash menu, and skill tools reflect it immediately.
func (a *App) SetSkillEnabled(name string, enabled bool) error {
	err := a.applySkillConfigChange("disabled_skills", "skill", func(c *config.Config) error {
		return c.SetSkillEnabled(name, enabled)
	})
	if err == nil {
		a.invalidateSkillRootsCache()
	}
	return err
}

func normalizeSkillPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, path[2:])
			}
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	info, err := os.Stat(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if info.Mode().IsRegular() {
		if filepath.Base(path) == skill.SkillFile {
			return filepath.Clean(filepath.Dir(filepath.Dir(path)))
		}
		return filepath.Clean(filepath.Dir(path))
	}
	if info.IsDir() {
		if _, err := os.Stat(filepath.Join(path, skill.SkillFile)); err == nil {
			return filepath.Clean(filepath.Dir(path))
		}
	}
	return filepath.Clean(path)
}

func isConventionSkillRoot(path, workspaceRoot string) bool {
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	if want == "" {
		return false
	}
	bases := []string{normalizeWorkspaceRoot(workspaceRoot)}
	if home, err := os.UserHomeDir(); err == nil {
		bases = append(bases, home)
	}
	for _, base := range bases {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		for _, dir := range config.ConventionDirs {
			if want == canonicalSkillPathForRoot(filepath.Join(base, dir, skill.SkillsDirname), workspaceRoot) {
				return true
			}
		}
	}
	return false
}

func skillRootScopeForPath(path, workspaceRoot string) skill.Scope {
	want := canonicalSkillPathForRoot(path, workspaceRoot)
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range config.ConventionDirs {
			if want == canonicalSkillPathForRoot(filepath.Join(home, dir, skill.SkillsDirname), workspaceRoot) {
				return skill.ScopeGlobal
			}
		}
	}
	if isConventionSkillRoot(path, workspaceRoot) {
		return skill.ScopeProject
	}
	return skill.ScopeCustom
}

func skillRootPath(path string) string {
	if filepath.Base(path) == skill.SkillFile {
		return filepath.Dir(path)
	}
	return path
}

func skillDisplayRoot(sk skill.Skill, roots []skill.Root) string {
	cleanPath := filepath.Clean(sk.Path)
	for _, r := range roots {
		if r.Scope != sk.Scope {
			continue
		}
		cleanRoot := filepath.Clean(r.Dir)
		prefix := cleanRoot + string(filepath.Separator)
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, prefix) {
			return config.CanonicalSkillPath(r.Dir)
		}
	}
	return config.CanonicalSkillPath(filepath.Dir(skillRootPath(sk.Path)))
}

func skillSourceDir(sk skill.Skill, roots []SkillRootView) string {
	path := strings.TrimSpace(sk.Path)
	if path == "" || strings.HasPrefix(path, "(builtin") {
		return ""
	}
	cleanPath := config.CanonicalSkillPath(path)
	bestDir := ""
	bestLen := -1
	for _, root := range roots {
		if root.Scope != "" && root.Scope != string(sk.Scope) {
			continue
		}
		cleanRoot := config.CanonicalSkillPath(root.Dir)
		if cleanRoot == "" {
			continue
		}
		prefix := cleanRoot + string(filepath.Separator)
		if cleanPath != cleanRoot && !strings.HasPrefix(cleanPath, prefix) {
			continue
		}
		if len(cleanRoot) > bestLen {
			bestDir = root.Dir
			bestLen = len(cleanRoot)
		}
	}
	if bestDir != "" {
		return bestDir
	}
	return config.CanonicalSkillPath(filepath.Dir(skillRootPath(path)))
}

// MCPServerInput is the drawer's "add server" form. Transport is "stdio" (Command
// + Args + Env) or "http"/"sse" (URL). Mirrors config.PluginEntry's writable shape.
type MCPServerInput struct {
	Name               string            `json:"name"`
	Transport          string            `json:"transport"`
	Command            string            `json:"command"`
	Args               []string          `json:"args"`
	URL                string            `json:"url"`
	Env                map[string]string `json:"env"`
	Headers            map[string]string `json:"headers"`
	AutoStart          *bool             `json:"autoStart"`
	CallTimeoutSeconds *int              `json:"callTimeoutSeconds"`
	ToolTimeoutSeconds map[string]int    `json:"toolTimeoutSeconds"`
}

func mcpServerInputEntry(in MCPServerInput) config.PluginEntry {
	entry := config.PluginEntry{
		Name:               strings.TrimSpace(in.Name),
		Type:               normalizeMCPTransport(in.Transport),
		Command:            strings.TrimSpace(in.Command),
		Args:               append([]string(nil), in.Args...),
		URL:                strings.TrimSpace(in.URL),
		Env:                in.Env,
		Headers:            in.Headers,
		AutoStart:          in.AutoStart,
		CallTimeoutSeconds: mcpIntValue(in.CallTimeoutSeconds),
		ToolTimeoutSeconds: cloneStringIntMap(in.ToolTimeoutSeconds),
		Source:             config.MCPSourceUserConfig,
	}
	entry, _ = config.NormalizePluginCommandLine(entry)
	return entry
}

// InstallMCPServer is the desktop's high-level install transaction. A normal
// handshake failure leaves no config behind; authentication-required servers
// are retained so the user can complete OAuth and retry. Only a ready result is
// published to every controller sharing the Host.
func (a *App) InstallMCPServer(in MCPServerInput) (plugin.MCPInstallResult, error) {
	defer a.lockMCPMutation("add")()

	_, ctrl, root := a.activeMCPRuntime()
	if ctrl == nil {
		return plugin.MCPInstallResult{}, fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return plugin.MCPInstallResult{}, err
	}
	defer releaseGates()

	entry := mcpServerInputEntry(in)
	if entry.Name == "" {
		return plugin.InstallResultForError(entry.Name, fmt.Errorf("MCP server name is required")), nil
	}
	if _, found, lookupErr := desktopEffectiveMCPServer(root, entry.Name); lookupErr != nil {
		return plugin.MCPInstallResult{}, lookupErr
	} else if found {
		return plugin.InstallResultForError(entry.Name, fmt.Errorf("MCP server %q is already installed", entry.Name)), nil
	}

	controllers := a.mcpControllersSharingHost(host, entry.Name, ctrl)
	toolCount, connectErr := ctrl.ConnectMCPServer(entry)
	if connectErr != nil {
		result := plugin.InstallResultForError(entry.Name, connectErr)
		if result.State != "action_required" {
			if host != nil {
				host.ClearFailure(entry.Name)
			}
			return result, nil
		}
		if err := a.saveDesktopMCPServer(root, entry); err != nil {
			return plugin.MCPInstallResult{}, err
		}
		if err := persistMCPInstallActivation(entry, root); err != nil {
			_, rollbackErr := a.removeDesktopMCPServer(root, entry.Name)
			if host != nil {
				host.ClearFailure(entry.Name)
			}
			return plugin.MCPInstallResult{}, errors.Join(err, rollbackErr)
		}
		a.bumpExtensionGeneration()
		recordMCPFailure(ctrl, entry, connectErr)
		return result, nil
	}
	var publishErrs []error
	for _, target := range controllers {
		if target.ctrl == ctrl || !target.enabled {
			continue
		}
		if _, err := target.ctrl.ConnectMCPServer(entry); err != nil {
			publishErrs = append(publishErrs, err)
		}
	}
	if err := errors.Join(publishErrs...); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, fmt.Errorf("publish MCP tools: %w", err)
	}
	if err := a.saveDesktopMCPServer(root, entry); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, err
	}
	if err := persistMCPInstallActivation(entry, root); err != nil {
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		_, rollbackErr := a.removeDesktopMCPServer(root, entry.Name)
		// The first disconnect happened while the just-saved config still
		// existed, so controller runtimes retained it as disabled. Reconcile once
		// more after rollback removes the config to prevent a phantom proxy entry.
		disconnectMCPServerControllers(entry.Name, ctrl, controllers)
		return plugin.MCPInstallResult{}, errors.Join(err, rollbackErr)
	}
	a.bumpExtensionGeneration()
	return plugin.ReadyInstallResult(entry.Name, toolCount), nil
}
func persistMCPInstallActivation(entry config.PluginEntry, root string) error {
	store := config.DefaultMCPActivationStore()
	if !entry.ShouldAutoStart() {
		return store.ClearServer(entry, root)
	}
	return store.SetServerEnabled(entry, root, true)
}

// UpdateMCPServer edits a persisted external MCP server. The name is the stable
// identity; callers must remove + add if they want to rename a server.
func (a *App) UpdateMCPServer(name string, in MCPServerInput) error {
	defer a.lockMCPMutation("update")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	if strings.TrimSpace(in.Name) != "" && strings.TrimSpace(in.Name) != name {
		return fmt.Errorf("renaming MCP servers is not supported; remove and add a new server")
	}
	updated, found, err := a.desktopMCPServerForEdit(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	original := updated
	updated.Type = normalizeMCPTransport(in.Transport)
	updated.Command = strings.TrimSpace(in.Command)
	updated.Args = append([]string(nil), in.Args...)
	updated.URL = strings.TrimSpace(in.URL)
	if in.Env != nil {
		updated.Env = in.Env
	}
	if in.Headers != nil {
		updated.Headers = in.Headers
	}
	if in.AutoStart != nil {
		value := *in.AutoStart
		updated.AutoStart = &value
	}
	if in.CallTimeoutSeconds != nil {
		updated.CallTimeoutSeconds = *in.CallTimeoutSeconds
	}
	if in.ToolTimeoutSeconds != nil {
		updated.ToolTimeoutSeconds = cloneStringIntMap(in.ToolTimeoutSeconds)
	}
	updated, _ = config.NormalizePluginCommandLine(updated)
	if updated.Type == "stdio" {
		updated.URL = ""
	} else {
		updated.Command = ""
		updated.Args = nil
	}
	enabled := false
	for _, target := range controllers {
		enabled = enabled || target.enabled
	}
	if !enabled {
		return a.saveDesktopMCPServerAndBump(root, updated)
	}
	spec, specErr := a.mcpLaunchSpecForEntry(root, updated)
	if specErr != nil {
		return specErr
	}
	if spec.RequireLaunchApproval {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := plugin.AuthorizeProjectSpecLaunch(ctx, spec); err != nil {
			return err
		}
	}
	disconnectMCPServerControllers(name, ctrl, controllers)
	if err := reconnectMCPServerControllers(updated, controllers); err != nil {
		rollbackErr := reconnectMCPServerControllers(original, controllers)
		recordMCPFailure(ctrl, updated, err)
		return errors.Join(err, rollbackErr)
	}
	if err := a.saveDesktopMCPServer(root, updated); err != nil {
		disconnectMCPServerControllers(name, ctrl, controllers)
		rollbackErr := reconnectMCPServerControllers(original, controllers)
		return errors.Join(err, rollbackErr)
	}
	a.bumpExtensionGeneration()
	return nil
}

// RemoveMCPServer disconnects a live server and drops it from config (the row's ✕).
// Uninstall also clears durable activation overrides for that server.
func (a *App) RemoveMCPServer(name string) error {
	defer a.lockMCPMutation("remove")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	if err := ensureMCPServerDirectlyWritable(root, name); err != nil {
		return err
	}
	entry, hasEntry, _ := desktopEffectiveMCPServer(root, name)
	removed, err := a.removeDesktopMCPServer(root, name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no removable MCP server named %q", name)
	}
	if hasEntry {
		_ = config.DefaultMCPActivationStore().ClearServer(entry, root)
	}
	authCleanupErr := reconcileRemovedMCPAuthentication(name, a.mcpWorkspaceRoots(root))
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	restoreMCPServerFallbacks(name, controllers)
	a.clearMCPServerTabState(name, controllers)
	a.bumpExtensionGeneration()
	return authCleanupErr
}

// restoreMCPServerFallbacks makes a lower-priority declaration immediately
// available after its project override is removed. Registration is cache-first:
// it restores cached tools or a connect placeholder without starting a process.
func restoreMCPServerFallbacks(name string, controllers []mcpControllerTarget) {
	for _, target := range controllers {
		root := target.ctrl.WorkspaceRoot()
		cfg, err := config.LoadForRoot(root)
		if err != nil {
			slog.Warn("desktop: reload MCP fallback after remove", "name", name, "workspace", root, "err", err)
			continue
		}
		entry, found := findPluginEntry(cfg.Plugins, name)
		if !found || !mcpEntryEnabled(entry, root) {
			continue
		}
		if _, err := target.ctrl.RegisterMCPServerOnDemand(entry); err != nil {
			slog.Warn("desktop: restore MCP fallback after remove", "name", name, "workspace", root, "err", err)
		}
	}
}

// ReconnectMCPServer disconnects the server if it is already connected (to force
// a fresh handshake and tool re-registration), then reconnects.  Failures are
// recorded on the Host so the UI can render them.
func (a *App) ReconnectMCPServer(name string) error {
	defer a.lockMCPMutation("reconnect")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	host, releaseGates, err := a.lockMCPHostTurnGates("MCP server", ctrl)
	if err != nil {
		return err
	}
	defer releaseGates()
	entry, found, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	if err := config.RecordExplicitStart(entry, root); err != nil {
		return err
	}
	controllers := a.mcpControllersSharingHost(host, name, ctrl)
	for i := range controllers {
		if controllers[i].ctrl == ctrl {
			controllers[i].enabled = true
		}
	}
	disconnectMCPServerControllers(name, ctrl, controllers)
	if host != nil {
		host.ClearFailure(name)
	}
	if err := reconnectMCPServerControllers(entry, controllers); err != nil {
		recordMCPFailure(ctrl, entry, err)
		return err
	}
	a.mu.Lock()
	delete(tab.disabledMCP, name)
	a.mu.Unlock()
	a.bumpExtensionGeneration()
	return nil
}

// SetMCPServerEnabled is the durable enable/disable switch for an installed MCP
// server. It writes $DEEPZORA_HOME/mcp-activation.json and updates the live
// registry: disable removes tools and may stop the process; enable restores
// cached tools and starts the process only on the next real tool call.
func (a *App) SetMCPServerEnabled(name string, enabled bool) error {
	defer a.lockMCPMutation("set-enabled")()

	tab, ctrl, root := a.activeMCPRuntime()
	if tab == nil || ctrl == nil {
		return fmt.Errorf("no active session")
	}
	a.mu.RLock()
	hostKey := tab.SharedHostKey
	a.mu.RUnlock()
	if err := rebuildControllerActiveWorkErrorFor(ctrl, "MCP server"); err != nil {
		return err
	}
	configuredEntry, hasConfiguredEntry, err := desktopEffectiveMCPServer(root, name)
	if err != nil {
		return err
	}
	if !hasConfiguredEntry {
		return fmt.Errorf("no configured MCP server named %q", name)
	}
	activationStore := config.DefaultMCPActivationStore()
	scope, workspaceFP, source, owner := config.ActivationIdentity(configuredEntry, root)
	previousEnabled, previousFound, err := activationStore.Lookup(scope, workspaceFP, source, owner, configuredEntry.Name)
	if err != nil {
		return err
	}
	if err := activationStore.SetServerEnabled(configuredEntry, root, enabled); err != nil {
		return err
	}
	a.bumpExtensionGeneration()
	if enabled {
		// Restore cached tools (or a cache-miss connect stub) without forcing a
		// process start. Explicit install/retry remains the readiness-probed path.
		_, err := a.registerConfiguredMCPServerForTab(tab, name)
		if err == nil {
			a.mu.Lock()
			delete(tab.disabledMCP, name)
			a.mu.Unlock()
			return nil
		}
		var rollbackErr error
		if previousFound {
			rollbackErr = activationStore.SetServerEnabled(configuredEntry, root, previousEnabled)
		} else {
			rollbackErr = activationStore.ClearServer(configuredEntry, root)
		}
		return errors.Join(err, rollbackErr)
	}
	if s, ok := findMCPServerView(ctrl, name); ok {
		s.Status = "disabled"
		s.Enabled = false
		s.Error = ""
		s = finalizeServerView(s)
		a.mu.Lock()
		if tab.disabledMCP == nil {
			tab.disabledMCP = map[string]ServerView{}
		}
		tab.disabledMCP[name] = s
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, []ServerView{s})
		a.mu.Unlock()
	} else {
		s := finalizeServerView(withPluginConfig(ServerView{Name: name, Status: "disabled", Enabled: false}, configuredEntry))
		a.mu.Lock()
		if tab.disabledMCP == nil {
			tab.disabledMCP = map[string]ServerView{}
		}
		tab.disabledMCP[name] = s
		tab.mcpOrder = mergeServerOrder(tab.mcpOrder, []ServerView{s})
		a.mu.Unlock()
	}
	if hostKey != "" {
		ctrl.UnregisterMCPServerTools(name)
	} else {
		ctrl.DisconnectMCPServer(name)
	}
	return nil
}

func (a *App) registerConfiguredMCPServerForTab(tab *WorkspaceTab, name string) (int, error) {
	a.mu.RLock()
	var ctrl control.SessionAPI
	root := ""
	if tab != nil {
		ctrl = tab.Ctrl
		root = tab.WorkspaceRoot
	}
	a.mu.RUnlock()
	if ctrl == nil {
		return 0, fmt.Errorf("no active session")
	}
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return 0, err
	}
	for _, p := range cfg.Plugins {
		if p.Name == name {
			return ctrl.RegisterMCPServerOnDemand(p)
		}
	}
	return 0, fmt.Errorf("no configured MCP server named %q", name)
}

func (a *App) desktopMCPServerForEdit(root, name string) (config.PluginEntry, bool, error) {
	// Edit the same effective declaration the runtime selected. The entry's
	// provenance is retained so saveDesktopMCPServer writes it back to the
	// owning project/global file instead of promoting it across scopes.
	return desktopEffectiveMCPServer(root, name)
}

// desktopEffectiveMCPServer returns the same merged entry the runtime starts.
// Its provenance identifies the exact project or global declaration that edit
// and remove operations must mutate.
func desktopEffectiveMCPServer(root, name string) (config.PluginEntry, bool, error) {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return config.PluginEntry{}, false, err
	}
	p, ok := findPluginEntry(cfg.Plugins, name)
	return p, ok, nil
}

func (a *App) saveDesktopMCPServer(root string, entry config.PluginEntry) error {
	if err := ensureMCPServerDirectlyWritable(root, entry.Name); err != nil {
		return err
	}
	_, err := config.UpsertPluginKeepingDecision(root, entry)
	return err
}

func ensureMCPServerDirectlyWritable(root, name string) error {
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return err
	}
	if owner, ok := cfg.PluginPackageOwner(name); ok {
		return fmt.Errorf("MCP server %q is managed by plugin %q; disable or remove the plugin instead", name, owner)
	}
	return nil
}

func (a *App) removeDesktopMCPServer(root, name string) (bool, error) {
	_, removed, _, err := config.RemovePluginFromEffectiveSourceForRoot(root, name)
	return removed, err
}

func findPluginEntry(entries []config.PluginEntry, name string) (config.PluginEntry, bool) {
	for _, p := range entries {
		if p.Name == name {
			return p, true
		}
	}
	return config.PluginEntry{}, false
}

func normalizeMCPTransport(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "http", "streamable-http":
		return "http"
	case "sse":
		return "sse"
	case "", "stdio":
		return "stdio"
	default:
		return strings.ToLower(strings.TrimSpace(transport))
	}
}

func mcpIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func cloneStringIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	out := make(map[string]int, len(values))
	maps.Copy(out, values)
	return out
}

func mcpConnected(ctrl control.SessionAPI, name string) bool {
	if ctrl == nil || ctrl.Host() == nil {
		return false
	}
	for _, s := range ctrl.Host().Servers() {
		if s.Name == name {
			return true
		}
	}
	return false
}

func recordMCPFailure(ctrl control.SessionAPI, e config.PluginEntry, err error) {
	if ctrl == nil || ctrl.Host() == nil || err == nil {
		return
	}
	exp := e.ExpandedPlugin()
	ctrl.Host().RecordFailure(plugin.Spec{
		Name:    exp.Name,
		Type:    exp.Type,
		Command: exp.Command,
		Args:    exp.Args,
		Env:     exp.Env,
		URL:     exp.URL,
		Headers: exp.Headers,
	}, err)
}

func findMCPServerView(ctrl control.SessionAPI, name string) (ServerView, bool) {
	if ctrl == nil || ctrl.Host() == nil {
		return ServerView{}, false
	}
	for _, s := range ctrl.Host().Servers() {
		if s.Name == name {
			return pluginServerToView(s), true
		}
	}
	for _, f := range ctrl.Host().Failures() {
		if f.Name == name {
			return ServerView{
				Name: f.Name, Transport: f.Transport, Status: "failed", Error: f.Error,
				RequiresLaunchApproval: f.RequiresLaunchApproval,
			}, true
		}
	}
	return ServerView{}, false
}

func pluginToolsToView(tools []plugin.ToolInfo) []ToolView {
	if len(tools) == 0 {
		return []ToolView{}
	}
	out := make([]ToolView, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolView{
			Name: t.Name, Description: t.Description, ReadOnlyHint: t.ReadOnlyHint, DestructiveHint: t.DestructiveHint, SchemaError: t.SchemaError,
		})
	}
	return out
}

func sameStringList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func orderServerViews(servers []ServerView, order []string) []ServerView {
	pos := make(map[string]int, len(order))
	for i, name := range order {
		pos[name] = i
	}
	sort.SliceStable(servers, func(i, j int) bool {
		pi, iok := pos[servers[i].Name]
		pj, jok := pos[servers[j].Name]
		switch {
		case iok && jok:
			return pi < pj
		case iok:
			return true
		case jok:
			return false
		default:
			return false
		}
	})
	return servers
}

func mergeServerOrder(order []string, servers []ServerView) []string {
	seen := make(map[string]bool, len(order)+len(servers))
	next := make([]string, 0, len(order)+len(servers))
	for _, name := range order {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		next = append(next, name)
	}
	for _, s := range servers {
		if s.Name == "" || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		next = append(next, s.Name)
	}
	return next
}

func removeServerOrder(order []string, name string) []string {
	if name == "" || len(order) == 0 {
		return order
	}
	next := order[:0]
	for _, n := range order {
		if n != name {
			next = append(next, n)
		}
	}
	return next
}

// ModelInfo is one (provider, model) the bottom switcher can pick. Ref ("provider/
// model") is what SetModel takes; Provider/Model are for display.
type ModelInfo struct {
	Ref           string `json:"ref"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Current       bool   `json:"current"`
	ContextWindow int    `json:"contextWindow,omitempty"`
	Vision        bool   `json:"vision,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
}

type EffortInfo struct {
	Options   []provider.ReasoningOption `json:"options,omitempty"`
	Supported bool                       `json:"supported"`
	Current   string                     `json:"current"`
	Default   string                     `json:"default"`
	Levels    []string                   `json:"levels"`
}

// Models flattens the configured providers into their (provider, model) pairs —
// the switcher's options — marking the active one. A vendor with a `models` list
// yields one entry per model, all sharing the same endpoint/key. Unconfigured
// providers are skipped. Result is non-nil: the frontend reads .length, so a nil
// slice (JSON null) would crash the switcher on an empty list.
func (a *App) Models() []ModelInfo {
	return a.ModelsForTab("")
}

// mergeExtensionModelInfos adds namespaced plugin models from the controller's
// merged provider catalog. Base descriptors are already represented by out;
// plugin refs need no provider-access gate because enabling the package grants
// access. A nil catalog leaves the config-backed list untouched.
func mergeExtensionModelInfos(out []ModelInfo, catalog []provider.Descriptor, curModel string) []ModelInfo {
	if len(catalog) == 0 {
		return out
	}
	seen := make(map[string]bool, len(out)+len(catalog))
	for _, info := range out {
		seen[info.Ref] = true
	}
	for _, d := range catalog {
		ref := strings.TrimSpace(d.Ref)
		owner := providerext.PluginRefOwner(ref)
		if ref == "" || owner == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		providerName := "plugin/" + owner
		model := strings.TrimPrefix(ref, providerName+"/")
		out = append(out, ModelInfo{Ref: ref, Provider: providerName, Model: model, Current: ref == curModel})
	}
	return out
}

// extensionModelDescriptor finds a plugin-namespaced ref in a controller's
// merged catalog: an exact match, or the prefix form where ref names the
// provider and the descriptor adds the model segment. Non-plugin refs never
// match — they belong to the config catalog.
func extensionModelDescriptor(catalog []provider.Descriptor, ref string) (provider.Descriptor, bool) {
	ref = strings.TrimSpace(ref)
	if providerext.PluginRefOwner(ref) == "" {
		return provider.Descriptor{}, false
	}
	for _, d := range catalog {
		if d.Ref == ref || strings.HasPrefix(d.Ref, ref+"/") {
			return d, true
		}
	}
	return provider.Descriptor{}, false
}

func modelProviderAccessAllowed(access []string, name string) bool {
	if access == nil {
		return true
	}
	name = strings.TrimSpace(name)
	for _, candidate := range access {
		if strings.TrimSpace(candidate) == name {
			return true
		}
	}
	return false
}

// providerCatalogForTab returns the tab controller's merged provider catalog
// (extension sidecar providers over the config base), or nil when the tab has
// no live controller or no sidecar declared providers.
func (a *App) providerCatalogForTab(tab *WorkspaceTab) []provider.Descriptor {
	if tab == nil {
		return nil
	}
	if ctrl := a.controllerForTab(tab); ctrl != nil {
		return ctrl.ProviderCatalog()
	}
	return nil
}

type activeRuntimeWork struct {
	running        bool
	pendingPrompt  bool
	backgroundJobs int
}

func controllerActiveRuntimeWork(ctrl control.SessionAPI) activeRuntimeWork {
	if ctrl == nil {
		return activeRuntimeWork{}
	}
	status := ctrl.RuntimeStateSnapshot()
	return activeRuntimeWork{
		running:        status.Running,
		pendingPrompt:  status.PendingPrompt,
		backgroundJobs: status.BackgroundJobs,
	}
}

func (w activeRuntimeWork) active() bool {
	return w.running || w.pendingPrompt || w.backgroundJobs > 0
}

func controllerHasActiveRuntimeWork(ctrl control.SessionAPI) bool {
	return controllerActiveRuntimeWork(ctrl).active()
}

// rebuildBusyError reports a rebuild rejected because the controller still has
// a running turn, pending prompt, or background jobs. Typed so the
// deferred-rebuild retry loop can keep waiting instead of giving up.
type rebuildBusyError struct {
	setting string
	work    activeRuntimeWork
}

func (e *rebuildBusyError) Error() string {
	return fmt.Sprintf(
		"active work is still running; running=%t; pending_prompt=%t; background_jobs=%d; finish or cancel the current turn, answer pending prompts, and stop background jobs before changing %s",
		e.work.running,
		e.work.pendingPrompt,
		e.work.backgroundJobs,
		e.setting,
	)
}

func rebuildControllerActiveWorkErrorFor(ctrl control.SessionAPI, setting string) error {
	work := controllerActiveRuntimeWork(ctrl)
	if setting == "model" || setting == "saved model settings" {
		if !control.ModelReplacementBlocked(ctrl) {
			return nil
		}
		if concrete, ok := ctrl.(*control.Controller); ok {
			work.backgroundJobs = len(concrete.ModelReplacementJobs())
		}
		return &rebuildBusyError{setting: setting, work: work}
	}
	if !work.active() {
		return nil
	}
	return &rebuildBusyError{setting: setting, work: work}
}

type sessionLeaseBusyError struct {
	setting string
	err     error
}

func (e *sessionLeaseBusyError) Error() string {
	// Current writer errors may include local identity details; user-facing
	// surfaces render this wrapper instead. An empty setting means the failure gated opening
	// the session itself (startup bind), not changing a setting on it.
	setting := strings.TrimSpace(e.setting)
	if setting == "" {
		return "this session is already open in another Deepzora window or still running in the background; close the other window or open a copy"
	}
	return fmt.Sprintf("this session is already open in another Deepzora window or still running in the background; close the other window or open a copy before changing %s", setting)
}

func (e *sessionLeaseBusyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func userFacingSessionLeaseError(setting string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, session.ErrWriterOwned) {
		return &sessionLeaseBusyError{setting: setting, err: err}
	}
	return err
}

// SetModel switches the active model and carries the current conversation into the
// new model's session, so the chat continues seamlessly and subsequent turns use
// the new model. No-op if name is already active or the controller is down.
func (a *App) SetModel(name string) error {
	return a.SetModelForTab("", name)
}

type modelSwitchTiming struct {
	Total          time.Duration
	LockWait       time.Duration
	Prepare        time.Duration
	Config         time.Duration
	Snapshot       time.Duration
	Build          time.Duration
	LeaseAndResume time.Duration
	SwapAndPersist time.Duration
	Outcome        string
}

func (a *App) SetModelForTab(tabID, name string) (retErr error) {
	if name == "" {
		return nil
	}
	if a.isRemoteTab(tabID) {
		return a.SetRemoteTabModel(tabID, name)
	}
	if a.ctx == nil {
		return nil
	}
	tab := a.tabByID(tabID)
	if tab == nil {
		return nil
	}
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	a.mu.RLock()
	currentModel := tab.model
	a.mu.RUnlock()
	if name == currentModel {
		return nil
	}
	timing := modelSwitchTiming{}
	totalStarted := time.Now()
	defer a.recordModelSwitchTiming(tab.ID, &timing, totalStarted, &retErr)
	// Same build+swap shape as rebuildSetting; hold the same lock so a settings
	// rebuild (manual or from the deferred-rebuild retry loop) and a model
	// switch cannot interleave on one tab.
	stageStarted := time.Now()
	a.runtimeRebuildMu.Lock()
	timing.LockWait = time.Since(stageStarted)
	defer a.runtimeRebuildMu.Unlock()
	stageStarted = time.Now()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	prevPath := a.sessionPathForSettingsRebuild(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" {
		a.attachExistingSessionRuntime(tab, prevPath, a.ctx)
	}
	if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "model"); err != nil {
		return err
	}
	if err := a.validateTabControllerWorkspace(tab); err != nil {
		return err
	}
	prevPath = a.sessionPathForSettingsRebuild(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" && a.attachExistingSessionRuntime(tab, prevPath, a.ctx) {
		if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "model"); err != nil {
			return err
		}
	}
	timing.Prepare = time.Since(stageStarted)
	// Snapshot the tab profile under a.mu: SetModeForTab/SetGoalForTab and the
	// event sink write these fields under the lock while this rebuild runs
	// off-lock.
	stageStarted = time.Now()
	snap := a.tabRuntimeSnapshot(tab)
	runtime := snap.normalizedRuntime()
	cfg, err := config.LoadForRoot(snap.workspaceRoot)
	if err != nil {
		return err
	}
	entry, ok := cfg.ResolveModel(name)
	pluginRef := false
	if !ok {
		// Plugin-namespaced refs belong to extension sidecars: validate them
		// against the tab controller's merged catalog instead of the config.
		if d, found := extensionModelDescriptor(a.providerCatalogForTab(tab), name); found {
			pluginRef = true
			ok = true
			name = d.Ref
		}
	}
	if !ok {
		return fmt.Errorf("unknown model %q", name)
	}
	if !pluginRef {
		if !modelProviderAccessAllowed(cfg.Desktop.ProviderAccess, entry.Name) {
			return fmt.Errorf("model %q is not available because provider %q is not added", name, entry.Name)
		}
		name = entry.Name + "/" + entry.Model
	}
	effortOverride := config.RebindSessionEffort(cfg, snap.model, name, snap.effort)
	timing.Config = time.Since(stageStarted)

	stageStarted = time.Now()
	oldCtrl := a.controllerForTab(tab)
	if oldCtrl != nil {
		if err := a.snapshotTabForAction(tab, "changing model"); err != nil {
			return err
		}
	}
	timing.Snapshot = time.Since(stageStarted)

	// Preserve the shared plugin host across controller rebuilds — the tab
	// stays in the same workspace root, so MCP processes must not be restarted.
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)

	stageStarted = time.Now()
	newCtrl, err := a.buildDesktopControllerReplacement(tab, oldCtrl, boot.Options{
		RuntimeReload:        boot.RuntimeReload{SystemPromptPolicy: boot.PreserveSystemPrompt},
		Model:                name,
		RequireKey:           false,
		StatsSource:          "desktop",
		TaskStore:            a.taskStore(),
		OnConfigLoadWarnings: a.configLoadWarningsHandler(),
		Sink:                 snap.sink,
		WorkspaceRoot:        snap.workspaceRoot,
		SessionDir:           sessionDirForSnapshot(snap),
		SessionService:       a.desktopSessionService(sessionDirForSnapshot(snap)),
		EffortOverride:       cloneStringPtr(effortOverride),
		SharedHost:           sharedHost, BrowserExecutor: a.browserExecutorForRuntime(tab.ID, snap.sink),
		SharedSkillWatchService: a.sharedSkillWatchService(),
		MCPHostProfile:          plugin.HostProfileDesktopApps,
		PinnedContextLoader:     pinnedContextLoader(snap.workspaceRoot),
		BeforeInboxDispatch:     a.beforeInboxDispatch,
		OnSessionTitleChanged:   a.onSessionTitleChanged,
		// Keep the private temporary directory across model switches (#7575).
		SessionTemp: sessionTempFromController(oldCtrl),
	})
	if err != nil {
		return err
	}
	timing.Build = time.Since(stageStarted)

	configureControllerRuntime(newCtrl, oldCtrl, runtime)

	stageStarted = time.Now()
	restoredRuntime, err := normalizeRestoredControllerRuntime(newCtrl, runtime)
	if err != nil {
		discardReplacementController(newCtrl, oldCtrl)
		return err
	}
	timing.LeaseAndResume = time.Since(stageStarted)
	stageStarted = time.Now()
	a.mu.Lock()
	if err := a.validateTabReplacementLocked(tab, "switching model"); err != nil {
		// The tab was closed/replaced while we built the new controller off-lock;
		// adopting it now would leak the runtime onto an orphaned tab and pin the
		// session lease forever.
		a.mu.Unlock()
		discardReplacementController(newCtrl, oldCtrl)
		return err
	}
	if err := activateReplacementController(oldCtrl, newCtrl); err != nil {
		a.mu.Unlock()
		discardReplacementController(newCtrl, oldCtrl)
		return fmt.Errorf("switching model: activate replacement runtime: %w", err)
	}
	tab.Ctrl = newCtrl
	tab.model = name
	tab.effort = cloneStringPtr(effortOverride)
	tab.Label = newCtrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, restoredRuntime)
	// Supersede any in-flight startup build: it would otherwise finish later,
	// overwrite this controller, and release/steal the tab's session lease.
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	if oldCtrl != nil {
		retireReplacedController(oldCtrl, newCtrl)
	}
	// A refresh queued during this build still owns its newer sequence.
	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	// A model switch changes the pricing context; discard the session-local
	// automatic wallet hint and let the next balance response rebind it.
	tab.clearRuntimeDisplayCurrency()
	a.notifyTabRuntimeRebuilt(tab)
	timing.SwapAndPersist = time.Since(stageStarted)
	return nil
}

func (a *App) Effort() EffortInfo {
	return a.EffortForTab("")
}

func (a *App) EffortForTab(tabID string) EffortInfo {
	entry, err := a.currentProviderEntryForTab(tabID)
	if err != nil {
		return EffortInfo{Current: "auto", Levels: []string{}}
	}
	cap := config.EffortCapabilityForEntry(entry)
	if !cap.Supported {
		return EffortInfo{Supported: false, Current: "auto", Default: cap.Default, Levels: []string{}}
	}
	levels := cap.Levels
	if levels == nil {
		levels = []string{}
	}
	return EffortInfo{Supported: true, Current: config.EffortDisplay(entry), Default: cap.Default, Levels: levels, Options: config.ReasoningCapabilityForEntry(entry).Options}
}

func (a *App) SetEffortForTab(tabID, level string) error {
	tab := a.tabByID(tabID)
	if tab == nil {
		if strings.TrimSpace(tabID) == "" {
			entry, err := a.currentProviderEntryForTab("")
			if err != nil {
				return err
			}
			effort, err := config.NormalizeEffort(entry, level)
			if err != nil {
				return err
			}
			return a.applyProviderEffortConfig(entry, effort)
		}
		return fmt.Errorf("tab %q not found", tabID)
	}
	// Build+swap path; serialize with the other rebuild paths (see
	// runtimeRebuildMu). The tab==nil branch above goes through
	// applyProviderEffortConfig → rebuildSetting, which takes the lock itself.
	pendingSequence := a.deferredRebuildSequence(tab.ID)
	a.runtimeRebuildMu.Lock()
	defer a.runtimeRebuildMu.Unlock()
	tab.turnStartMu.Lock()
	defer tab.turnStartMu.Unlock()
	prevPath := a.currentSessionPathFor(tab)
	// Recomputing prevPath after this attach would be a dead store: it is
	// unconditionally derived again after ensureTabControllerWorkspace below.
	if a.controllerForTab(tab) == nil && prevPath != "" {
		a.attachExistingSessionRuntime(tab, prevPath, a.ctx)
	}
	if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "effort"); err != nil {
		return err
	}
	if err := a.validateTabControllerWorkspace(tab); err != nil {
		return err
	}
	prevPath = a.currentSessionPathFor(tab)
	if a.controllerForTab(tab) == nil && prevPath != "" && a.attachExistingSessionRuntime(tab, prevPath, a.ctx) {
		if err := rebuildControllerActiveWorkErrorFor(a.controllerForTab(tab), "effort"); err != nil {
			return err
		}
	}
	snap := a.tabRuntimeSnapshot(tab)
	runtime := snap.normalizedRuntime()
	entry, err := a.currentProviderEntryForTab(tabID)
	if err != nil {
		return err
	}
	modelRef := entry.Name + "/" + entry.Model
	effort, err := config.NormalizeEffort(entry, level)
	if err != nil {
		return err
	}
	oldCtrl := a.controllerForTab(tab)
	if oldCtrl != nil {
		if err := a.snapshotTabForAction(tab, "changing effort"); err != nil {
			return err
		}
	}
	sharedHost := a.lookupSharedHost(snap.sharedHostKey)
	newCtrl, err := a.buildDesktopControllerReplacement(tab, oldCtrl, boot.Options{
		RuntimeReload:        boot.RuntimeReload{SystemPromptPolicy: boot.PreserveSystemPrompt},
		Model:                modelRef,
		RequireKey:           false,
		StatsSource:          "desktop",
		TaskStore:            a.taskStore(),
		OnConfigLoadWarnings: a.configLoadWarningsHandler(),
		Sink:                 snap.sink,
		WorkspaceRoot:        snap.workspaceRoot,
		SessionDir:           sessionDirForSnapshot(snap),
		SessionService:       a.desktopSessionService(sessionDirForSnapshot(snap)),
		EffortOverride:       &effort,
		SharedHost:           sharedHost, BrowserExecutor: a.browserExecutorForRuntime(tab.ID, snap.sink),
		SharedSkillWatchService: a.sharedSkillWatchService(),
		MCPHostProfile:          plugin.HostProfileDesktopApps,
		PinnedContextLoader:     pinnedContextLoader(snap.workspaceRoot),
		BeforeInboxDispatch:     a.beforeInboxDispatch,
		OnSessionTitleChanged:   a.onSessionTitleChanged,
		// Keep the private temporary directory across effort switches (#7575).
		SessionTemp: sessionTempFromController(oldCtrl),
	})
	if err != nil {
		return err
	}

	configureControllerRuntime(newCtrl, oldCtrl, runtime)
	path := ""
	restoredRuntime, err := normalizeRestoredControllerRuntime(newCtrl, runtime)
	if err != nil {
		discardReplacementController(newCtrl, oldCtrl)
		return err
	}
	a.mu.Lock()
	if err := a.validateTabReplacementLocked(tab, "switching effort"); err != nil {
		a.mu.Unlock()
		discardReplacementController(newCtrl, oldCtrl)
		return err
	}
	if err := activateReplacementController(oldCtrl, newCtrl); err != nil {
		a.mu.Unlock()
		discardReplacementController(newCtrl, oldCtrl)
		return fmt.Errorf("switching effort: activate replacement runtime: %w", err)
	}
	tab.Ctrl = newCtrl
	tab.model = modelRef
	tab.effort = &effort
	tab.Label = newCtrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, restoredRuntime)
	clearTabStartupError(tab)
	tab.Ready = true
	a.supersedeTabBuildLocked(tab)
	a.saveTabsLocked()
	a.mu.Unlock()
	if oldCtrl != nil {
		retireReplacedController(oldCtrl, newCtrl)
	}
	a.clearDeferredRebuildVersion(tab.ID, pendingSequence)
	a.rememberTabSessionPath(tab, path)
	a.notifyTabRuntimeRebuilt(tab)
	return nil
}

func (a *App) applyProviderEffortConfig(entry *config.ProviderEntry, effort string) error {
	return a.applyConfigChange(func(cfg *config.Config) error {
		if _, ok := cfg.Provider(entry.Name); !ok {
			if err := cfg.UpsertProvider(*entry); err != nil {
				return err
			}
		}
		if entry.Kind == "anthropic" && effort != "" && entry.Thinking == "" {
			if err := cfg.SetProviderThinking(entry.Name, "adaptive"); err != nil {
				return err
			}
		}
		for _, name := range providerEffortTargetNames(cfg, entry) {
			if err := cfg.SetProviderEffort(name, effort); err != nil {
				return err
			}
		}
		return nil
	})
}

func providerEffortTargetNames(cfg *config.Config, entry *config.ProviderEntry) []string {
	if cfg == nil || entry == nil {
		return nil
	}
	out := []string{entry.Name}
	seen := map[string]bool{entry.Name: true}
	kind := officialProviderKindFromEntry(*entry)
	if kind == "" {
		return out
	}
	var family []string
	switch kind {
	case "deepseek":
		family = []string{"deepseek", "deepseek-flash", "deepseek-pro"}
	}
	for _, name := range family {
		if seen[name] {
			continue
		}
		p, ok := cfg.Provider(name)
		if !ok || officialProviderKindFromEntry(*p) != kind {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// DirEntry is one entry in the "@" file-reference menu.
type DirEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	IsDir       bool   `json:"isDir"`
	DisplayName string `json:"displayName,omitempty"`
	DisplayPath string `json:"displayPath,omitempty"`
}

// FilePreview is a bounded, read-only file payload for the workspace side panel.
type FilePreview struct {
	Path       string `json:"path"`
	Body       string `json:"body"`
	Size       int64  `json:"size"`
	Truncated  bool   `json:"truncated"`
	Binary     bool   `json:"binary"`
	Version    string `json:"version,omitempty"`
	NextOffset int64  `json:"nextOffset,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Mime       string `json:"mime,omitempty"`
	URL        string `json:"url,omitempty"`
	Err        string `json:"err,omitempty"`
}

// PresentedTextPage is one version-fenced UTF-8 continuation for a declared
// text deliverable. Pages append to a single document; callers must discard a
// page when Version differs from the first preview.
type PresentedTextPage struct {
	Path       string `json:"path"`
	Body       string `json:"body"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"nextOffset"`
	Size       int64  `json:"size"`
	HasMore    bool   `json:"hasMore"`
	Version    string `json:"version"`
}

type WorkspaceChangeView struct {
	Path             string   `json:"path"`
	OldPath          string   `json:"oldPath,omitempty"`
	Sources          []string `json:"sources"`
	GitStatus        string   `json:"gitStatus,omitempty"`
	Turns            []int    `json:"turns,omitempty"`
	LatestPrompt     string   `json:"latestPrompt,omitempty"`
	LatestTime       int64    `json:"latestTime,omitempty"`
	CanSessionRevert bool     `json:"canSessionRevert,omitempty"`
}

type WorkspaceChangesView struct {
	Files        []WorkspaceChangeView `json:"files"`
	GitAvailable bool                  `json:"gitAvailable"`
	GitErr       string                `json:"gitErr,omitempty"`
	GitBranch    string                `json:"gitBranch,omitempty"`
	Added        int                   `json:"added,omitempty"`
	Removed      int                   `json:"removed,omitempty"`
	Incomplete   bool                  `json:"incomplete,omitempty"`
}

type WorkspaceChangeDetailView struct {
	Diff      *string `json:"diff,omitempty"`
	Source    string  `json:"source,omitempty"`
	Added     int     `json:"added,omitempty"`
	Removed   int     `json:"removed,omitempty"`
	Binary    bool    `json:"binary,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

const filePreviewLimit = 2 * 1024 * 1024 // 2 MiB — full file preview for the workspace panel
const presentedTextPageLimit = 512 * 1024
const fileRefSearchLimit = 20

var previewMediaMIMEs = map[string]string{
	".aac":  "audio/aac",
	".bmp":  "image/bmp",
	".flac": "audio/flac",
	".gif":  "image/gif",
	".htm":  "text/html; charset=utf-8",
	".html": "text/html; charset=utf-8",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".m4a":  "audio/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".mp3":  "audio/mpeg",
	".mp4":  "video/mp4",
	".oga":  "audio/ogg",
	".ogg":  "audio/ogg",
	".ogv":  "video/ogg",
	".pdf":  "application/pdf",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".wav":  "audio/wav",
	".webm": "video/webm",
	".webp": "image/webp",
}

func trimUTF8PartialSuffix(data []byte) []byte {
	if utf8.Valid(data) {
		return data
	}
	for i := len(data) - 1; i >= 0 && len(data)-i <= utf8.UTFMax; i-- {
		if !utf8.RuneStart(data[i]) {
			continue
		}
		if !utf8.Valid(data[:i]) || utf8.FullRune(data[i:]) {
			return data
		}
		return data[:i]
	}
	return data
}

func workspaceFileVersion(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func previewMediaKind(path string) (kind string, mime string) {
	mime = previewMediaMIMEs[strings.ToLower(filepath.Ext(path))]
	if mime == "" {
		return "", ""
	}
	if strings.HasPrefix(mime, "image/") {
		return "image", mime
	}
	if strings.HasPrefix(mime, "audio/") {
		return "audio", mime
	}
	if strings.HasPrefix(mime, "video/") {
		return "video", mime
	}
	if mime == "application/pdf" {
		return "pdf", mime
	}
	if strings.HasPrefix(mime, "text/html") {
		return "html", mime
	}
	return "", ""
}

func (a *App) workspaceTargetForTab(tabID string) (string, control.SessionAPI, bool) {
	tabID = strings.TrimSpace(tabID)
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		if tabID == "" {
			return ".", nil, true
		}
		return "", nil, false
	}
	return tab.WorkspaceRoot, tab.Ctrl, true
}

func workspaceBaseFromRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || root == "." {
		return os.Getwd()
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return filepath.Clean(root), nil
}

func workspacePathForBase(base, rel string) (string, bool, error) {
	base = filepath.Clean(base)
	if rel == "" {
		return "", false, os.ErrInvalid
	}
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, rel)
	}
	path = filepath.Clean(path)
	r, err := filepath.Rel(base, path)
	if err != nil {
		return "", false, err
	}
	if r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
		return "", false, os.ErrPermission
	}
	return path, true, nil
}

// ListDir lists one directory level (directories first, then files, each
// alphabetical) for the "@" file-reference menu. rel resolves against the active
// tab workspace. The menu navigates one level at a time, never recursively —
// bounded for huge trees.
func (a *App) ListDir(rel string) []DirEntry {
	return a.ListDirForTab("", rel)
}

// ListDirForTab is the tab-scoped variant used by multi-tab frontend surfaces.
func (a *App) ListDirForTab(tabID, rel string) []DirEntry {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return []DirEntry{}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return []DirEntry{}
	}
	return listDirForWorkspaceTarget(base, ctrl, rel)
}

func listDirForWorkspaceTarget(base string, ctrl control.SessionAPI, rel string) []DirEntry {
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if entries, handled := browser.ListExternalFolderRefDir(rel); handled {
			return externalFolderDirEntries(entries)
		}
	}
	dir := base
	if rel != "" {
		path, ok, err := workspacePathForBase(base, rel)
		if err != nil || !ok {
			return []DirEntry{}
		}
		dir = path
	}
	realDir, realBase, err := canonicalPathWithin(base, dir)
	if err != nil {
		return []DirEntry{}
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return []DirEntry{}
	}
	dirs, files := []DirEntry{}, []DirEntry{}
	for _, e := range es {
		name := e.Name()
		info, err := e.Info()
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			info, err = linkedEntryInTree(realBase, realDir, filepath.Join(dir, name))
		}
		if err != nil || fileref.SkipBrowseEntry(name, info.IsDir()) {
			continue
		}
		if info.IsDir() {
			dirs = append(dirs, DirEntry{Name: name, IsDir: true})
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		files = append(files, DirEntry{Name: name, IsDir: false})
	}
	sort.Slice(dirs, func(i, j int) bool { return fileref.NaturalLess(dirs[i].Name, dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return fileref.NaturalLess(files[i].Name, files[j].Name) })
	return append(dirs, files...)
}

// linkedEntryInTree stats a link's target when it resolves inside the
// workspace and not onto the folder being listed or one of its ancestors; a
// folder that contains itself would be expanded without end.
func linkedEntryInTree(realBase, realDir, link string) (os.FileInfo, error) {
	target, _, err := canonicalPathWithin(realBase, link)
	if err != nil {
		return nil, err
	}
	if up, err := filepath.Rel(target, realDir); err == nil && filepath.IsLocal(up) {
		return nil, os.ErrPermission
	}
	return os.Stat(target)
}

// SearchFileRefs finds workspace files by basename for bare "@token" completion.
func (a *App) SearchFileRefs(query string) []DirEntry {
	return a.SearchFileRefsForTab("", query)
}

// SearchFileRefsForTab is the tab-scoped variant used by multi-tab frontend surfaces.
func (a *App) SearchFileRefsForTab(tabID, query string) []DirEntry {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return []DirEntry{}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return []DirEntry{}
	}
	return searchFileRefsForWorkspaceTarget(base, ctrl, query)
}

func searchFileRefsForWorkspaceTarget(base string, ctrl control.SessionAPI, query string) []DirEntry {
	results := fileref.Search(base, query, fileRefSearchLimit)
	out := make([]DirEntry, 0, len(results))
	for _, r := range results {
		out = append(out, DirEntry{Name: r.Path, IsDir: r.IsDir})
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		out = append(out, externalFolderDirEntries(browser.SearchExternalFolderRefs(query, fileRefSearchLimit))...)
	}
	return out
}

type externalFolderRefBrowser interface {
	ListExternalFolderRefDir(tokenPath string) ([]control.ExternalFolderRefEntry, bool)
	SearchExternalFolderRefs(query string, limit int) []control.ExternalFolderRefEntry
	ExternalFolderRefLocalPath(tokenPath string) (path, displayPath string, ok bool)
}

func externalFolderRefBrowserFromController(ctrl control.SessionAPI) externalFolderRefBrowser {
	if browser, ok := ctrl.(externalFolderRefBrowser); ok {
		return browser
	}
	return nil
}

func externalFolderDirEntries(entries []control.ExternalFolderRefEntry) []DirEntry {
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, DirEntry{
			Name:        e.Name,
			Path:        e.Path,
			IsDir:       e.IsDir,
			DisplayName: e.DisplayName,
			DisplayPath: e.DisplayPath,
		})
	}
	return out
}

func (a *App) workspaceOrExternalPathForTab(tabID, rel string) (string, bool, error) {
	path, _, ok, err := a.tabPathAndBase(tabID, rel)
	return path, ok, err
}

// tabPathAndBase also returns the workspace base the path was joined under,
// empty for a session-authorized external folder reference.
func (a *App) tabPathAndBase(tabID, rel string) (string, string, bool, error) {
	root, ctrl, ok := a.workspaceTargetForTab(tabID)
	if !ok {
		return "", "", false, os.ErrNotExist
	}
	if browser := externalFolderRefBrowserFromController(ctrl); browser != nil {
		if path, _, ok := browser.ExternalFolderRefLocalPath(rel); ok {
			return path, "", true, nil
		}
	}
	base, err := workspaceBaseFromRoot(root)
	if err != nil {
		return "", "", false, err
	}
	path, ok, err := workspacePathForBase(base, rel)
	return path, base, ok, err
}

// ReadFile returns a small text preview for a file under the current workspace
// or a session-authorized external folder ref.
func (a *App) ReadFile(rel string) FilePreview {
	return a.ReadFileForTab("", rel)
}

// ReadFileForTab returns a preview resolved against the requested tab.
func (a *App) ReadFileForTab(tabID, rel string) FilePreview {
	path, base, ok, err := a.tabPathAndBase(tabID, rel)
	if err != nil || !ok {
		return FilePreview{Path: rel, Err: "invalid path"}
	}
	// The lexical join is not containment: a linked component can leave the tree.
	if base != "" {
		if _, _, err := canonicalPathWithin(base, path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return FilePreview{Path: rel, Err: "invalid path"}
		}
	}
	return a.readFilePathForTab(tabID, rel, path, false)
}

func (a *App) readFilePathForTab(tabID, displayPath, path string, forceSource bool) FilePreview {
	out := FilePreview{Path: displayPath}
	info, err := os.Stat(path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	if info.IsDir() {
		out.Err = "path is a directory"
		return out
	}
	if !info.Mode().IsRegular() {
		out.Err = "path is not a regular file"
		return out
	}
	out.Size = info.Size()
	out.Version = workspaceFileVersion(info)
	if kind, mime := previewMediaKind(path); kind != "" && !forceSource {
		var token string
		store := a.ensureMediaTokenStore()
		if kind == "html" {
			allowedRoot := filepath.Dir(path)
			if root, _, found := a.workspaceTargetForTab(tabID); found {
				if base, baseErr := workspaceBaseFromRoot(root); baseErr == nil {
					if relative, relativeErr := filepath.Rel(base, path); relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
						allowedRoot = base
					}
				}
			}
			token, err = store.createHTML(path, allowedRoot, info.Name(), mime, info)
			if err != nil {
				out.Err = err.Error()
				return out
			}
		} else {
			token = store.create(path, info.Name(), mime, kind, info.Size(), info.ModTime())
		}
		// Document tabs explicitly revoke their token on close. A two-hour
		// ceiling keeps long-running HTML/media previews alive without leaving
		// abandoned capabilities unbounded after a renderer crash.
		store.extend(token, 2*time.Hour)
		out.Kind = kind
		out.Mime = mime
		out.URL = "/__deepzora_workspace_media/" + token + "/" + url.PathEscape(info.Name())
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		out.Err = err.Error()
		return out
	}
	defer f.Close()

	buf := make([]byte, filePreviewLimit+1)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		out.Err = err.Error()
		return out
	}
	data := buf[:n]
	if len(data) > filePreviewLimit {
		data = data[:filePreviewLimit]
		out.Truncated = true
	}

	// Check for BOM first (just the first 2-3 bytes — always complete
	// even at a truncation boundary). BOM-prefixed files skip the NUL
	// check since UTF-16 normally contains 0x00 for ASCII characters.
	bomKind := fileenc.DetectQuick(data)
	if bomKind != fileenc.UTF8 {
		enc, _ := fileenc.Detect(data)
		if enc == fileenc.LossyUTF8 {
			out.Binary = true
			return out
		}
		decoded := fileenc.Decode(data, enc)
		out.Body = string(decoded)
		return out
	}

	// No BOM — NUL in raw bytes is a binary signal.
	if bytes.Contains(data, []byte{0}) {
		out.Binary = true
		return out
	}

	// A truncated preview can end inside a character; the fragment keeps the
	// whole ones and the next page starts at the split one.
	detect := fileenc.Detect
	if out.Truncated {
		detect = fileenc.DetectFragment
	}
	enc, data := detect(data)
	if out.Truncated {
		out.NextOffset = int64(len(data))
	}
	if enc == fileenc.LossyUTF8 {
		out.Body = strings.ToValidUTF8(string(data), "\uFFFD")
		return out
	}
	out.Body = string(fileenc.Decode(data, enc))
	return out
}

func (a *App) presentedPathForTab(tabID, toolCallID, requested string) (string, error) {
	result := a.ToolResultForTab(tabID, toolCallID)
	if !presentedFileDeclared(result, requested) {
		return "", os.ErrPermission
	}
	root, _, found := a.workspaceTargetForTab(tabID)
	if !found {
		return "", os.ErrPermission
	}
	declared := requested
	var resolved string
	if path, ok, err := a.workspaceOrExternalPathForTab(tabID, declared); err == nil && ok {
		resolved = path
	} else {
		if !filepath.IsAbs(declared) {
			return "", os.ErrPermission
		}
		resolved = filepath.Clean(declared)
	}
	resolved, err := validatePresentedReadPath(resolved)
	if err != nil {
		return "", err
	}
	if !readPolicyAllowsPath(root, resolved) {
		return "", os.ErrPermission
	}
	return resolved, nil
}

func readPolicyAllowsPath(workspaceRoot, resolved string) bool {
	cfg, err := config.LoadForRootWithoutCredentialsReadOnly(workspaceRoot)
	if err != nil {
		return false
	}
	return !builtin.ReadPathForbidden(boot.RuntimeForbidReadRoots(cfg, workspaceRoot), resolved)
}

func validatePresentedReadPath(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", os.ErrInvalid
	}
	return path, nil
}

func presentedFileDeclared(result *control.ToolResultData, requested string) bool {
	if result == nil || result.Name != "present" || strings.TrimSpace(requested) == "" {
		return false
	}
	for _, file := range result.PresentedFiles {
		if file.Path == requested {
			return true
		}
	}
	return false
}

// ReadPresentedFileForTab resolves a resource through the trusted metadata of
// the built-in present call. This permits an explicitly declared absolute file
// without turning the generic workspace reader into an arbitrary-path API.
func (a *App) ReadPresentedFileForTab(tabID, toolCallID, path string) FilePreview {
	resolved, err := a.presentedPathForTab(tabID, toolCallID, path)
	if err != nil {
		return FilePreview{Path: path, Err: err.Error()}
	}
	return a.readFilePathForTab(tabID, path, resolved, false)
}

func (a *App) ReadPresentedFileSourceForTab(tabID, toolCallID, path string) FilePreview {
	resolved, err := a.presentedPathForTab(tabID, toolCallID, path)
	if err != nil {
		return FilePreview{Path: path, Err: err.Error()}
	}
	return a.readFilePathForTab(tabID, path, resolved, true)
}

// ReadPresentedTextPageForTab appends a bounded UTF-8 page to a trusted
// present preview. The expected version prevents a reader from joining bytes
// from two revisions when the file changes between requests.
func (a *App) ReadPresentedTextPageForTab(tabID, toolCallID, path string, offset int64, expectedVersion string) (PresentedTextPage, error) {
	resolved, err := a.presentedPathForTab(tabID, toolCallID, path)
	if err != nil {
		return PresentedTextPage{}, err
	}
	return readPresentedTextPage(resolved, path, offset, expectedVersion)
}

func readPresentedTextPage(resolved, displayPath string, offset int64, expectedVersion string) (PresentedTextPage, error) {
	f, err := os.Open(resolved)
	if err != nil {
		return PresentedTextPage{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return PresentedTextPage{}, err
	}
	current, err := os.Lstat(resolved)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) {
		return PresentedTextPage{}, os.ErrPermission
	}
	version := workspaceFileVersion(info)
	if expectedVersion == "" || expectedVersion != version {
		return PresentedTextPage{}, fmt.Errorf("file changed; reload before loading more")
	}
	if offset < 0 || offset > info.Size() {
		return PresentedTextPage{}, os.ErrInvalid
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return PresentedTextPage{}, err
	}
	buf := make([]byte, presentedTextPageLimit+utf8.UTFMax)
	n, readErr := f.Read(buf)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return PresentedTextPage{}, readErr
	}
	data := buf[:n]
	if len(data) > presentedTextPageLimit {
		data = trimUTF8PartialSuffix(data[:presentedTextPageLimit])
	}
	if bytes.Contains(data, []byte{0}) || !utf8.Valid(data) {
		return PresentedTextPage{}, fmt.Errorf("additional pages require UTF-8 text")
	}
	next := offset + int64(len(data))
	if next == offset && next < info.Size() {
		return PresentedTextPage{}, fmt.Errorf("could not advance text page")
	}
	after, err := f.Stat()
	if err != nil || workspaceFileVersion(after) != version {
		return PresentedTextPage{}, fmt.Errorf("file changed; reload before loading more")
	}
	return PresentedTextPage{
		Path: displayPath, Body: string(data), Offset: offset, NextOffset: next,
		Size: info.Size(), HasMore: next < info.Size(), Version: version,
	}, nil
}

// CreateWorkspaceBrowserPreviewForTab returns a loopback-only URL for a
// validated preview resource. The browser never receives file:// or the app's
// privileged resource origin.
func (a *App) CreateWorkspaceBrowserPreviewForTab(tabID, rel string) (string, error) {
	preview := a.ReadFileForTab(tabID, rel)
	if preview.Err != "" {
		return "", errors.New(preview.Err)
	}
	if preview.URL == "" {
		return "", errors.New("this file type cannot be opened in the built-in browser")
	}
	origin, err := a.ensureWorkspacePreviewOrigin()
	if err != nil {
		return "", err
	}
	a.extendWorkspaceBrowserPreviewToken(preview.URL)
	return origin + preview.URL, nil
}

func (a *App) CreatePresentedBrowserPreviewForTab(tabID, toolCallID, path string) (string, error) {
	preview := a.ReadPresentedFileForTab(tabID, toolCallID, path)
	if preview.Err != "" {
		return "", errors.New(preview.Err)
	}
	if preview.URL == "" {
		return "", errors.New("this file type cannot be opened in the built-in browser")
	}
	origin, err := a.ensureWorkspacePreviewOrigin()
	if err != nil {
		return "", err
	}
	a.extendWorkspaceBrowserPreviewToken(preview.URL)
	return origin + preview.URL, nil
}

func (a *App) extendWorkspaceBrowserPreviewToken(resourceURL string) {
	const prefix = "/__deepzora_workspace_media/"
	trimmed := strings.TrimPrefix(resourceURL, prefix)
	if trimmed == resourceURL {
		return
	}
	token := strings.SplitN(trimmed, "/", 2)[0]
	if token != "" {
		a.ensureMediaTokenStore().extend(token, 2*time.Hour)
	}
}

// RevokeWorkspaceBrowserPreview drops the file binding and invalidates only
// URLs minted by this app's unprivileged preview origin. Browser tab close
// calls this best-effort.
func (a *App) RevokeWorkspaceBrowserPreview(rawURL string) {
	a.releaseFileBrowserPreviewURL(rawURL)
	a.revokeWorkspaceBrowserPreview(rawURL)
}

func (a *App) revokeWorkspaceBrowserPreview(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	a.mu.RLock()
	p := a.presentPreview
	a.mu.RUnlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	origin := p.origin
	p.mu.Unlock()
	if origin == "" || u.Scheme+"://"+u.Host != origin {
		return
	}
	a.revokeWorkspaceMediaPath(u.Path)
}

// RevokeWorkspaceMediaPreview releases a relative resource capability used by
// the document workspace when its renderer unmounts or changes files.
func (a *App) RevokeWorkspaceMediaPreview(resourceURL string) {
	u, err := url.Parse(resourceURL)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return
	}
	a.revokeWorkspaceMediaPath(u.Path)
}

func (a *App) revokeWorkspaceMediaPath(resourcePath string) {
	const prefix = "/__deepzora_workspace_media/"
	trimmed := strings.TrimPrefix(resourcePath, prefix)
	if trimmed == resourcePath {
		return
	}
	if token := strings.SplitN(trimmed, "/", 2)[0]; token != "" {
		a.ensureMediaTokenStore().revoke(token)
	}
}

func (a *App) OpenPresentedPathForTab(tabID, toolCallID, path string) error {
	resolved, err := a.presentedPathForTab(tabID, toolCallID, path)
	if err != nil {
		return err
	}
	return openWorkspacePath(resolved)
}

// ResolvePresentedPathForTab returns the source host's absolute path only
// after revalidating the trusted present result and the current read policy.
func (a *App) ResolvePresentedPathForTab(tabID, toolCallID, path string) (string, error) {
	return a.presentedPathForTab(tabID, toolCallID, path)
}

func (a *App) RevealPresentedPathForTab(tabID, toolCallID, path string) error {
	resolved, err := a.presentedPathForTab(tabID, toolCallID, path)
	if err != nil {
		return err
	}
	return revealPath(resolved)
}

func (a *App) SavePresentedPathAsForTab(tabID, toolCallID, path string) (string, error) {
	resolved, err := a.presentedPathForTab(tabID, toolCallID, path)
	if err != nil {
		return "", err
	}
	return a.SaveLocalPathAs(resolved)
}

// OpenWorkspacePathForTab opens a path resolved against the requested tab.
func (a *App) OpenWorkspacePathForTab(tabID, rel string) error {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return os.ErrInvalid
	}
	return openWorkspacePath(path)
}

// RevealWorkspacePathForTab reveals a path resolved against the requested tab.
func (a *App) RevealWorkspacePathForTab(tabID, rel string) error {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return os.ErrInvalid
	}
	return revealPath(path)
}

// SaveWorkspacePathAsForTab copies a session-scoped workspace or authorized
// external file to a destination chosen by the user.
func (a *App) SaveWorkspacePathAsForTab(tabID, rel string) (string, error) {
	path, ok, err := a.workspaceOrExternalPathForTab(tabID, rel)
	if err != nil || !ok {
		return "", os.ErrInvalid
	}
	return a.SaveLocalPathAs(path)
}

// RevealPath shows an arbitrary absolute path in the native file manager.
func (a *App) RevealPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return os.ErrInvalid
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return revealPath(path)
}

var revealPath = defaultRevealPath

func defaultRevealPath(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return proc.VisibleCommand("open", "-R", path).Start()
	case "windows":
		// explorer.exe lives in %SystemRoot%, which isn't always on PATH (the
		// launch environment can strip it), so resolve it directly rather than
		// relying on a PATH lookup.
		explorer := "explorer.exe"
		root := os.Getenv("SystemRoot")
		if root == "" {
			root = os.Getenv("windir")
		}
		if root != "" {
			explorer = filepath.Join(root, "explorer.exe")
		}
		return proc.VisibleCommand(explorer, "/select,", path).Start()
	default:
		dir := path
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			dir = filepath.Dir(path)
		}
		return proc.VisibleCommand("xdg-open", dir).Start()
	}
}

func (a *App) noticeForTab(tabID, text string) {
	tab := a.tabByID(tabID)
	if tab != nil && tab.sink != nil {
		tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: text})
	}
}

func (a *App) warnForTab(tabID, text string) {
	tab := a.tabByID(tabID)
	if tab != nil && tab.sink != nil {
		tab.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: text})
	}
}

func (a *App) runEffortCommandForTab(tabID, input string) {
	entry, err := a.currentProviderEntryForTab(tabID)
	if err != nil {
		a.noticeForTab(tabID, "effort: "+err.Error())
		return
	}
	cap := config.EffortCapabilityForEntry(entry)
	args := strings.Fields(input)
	if !cap.Supported && !(len(args) == 2 && args[1] == "auto") {
		a.noticeForTab(tabID, fmt.Sprintf("effort is not configurable for %s", entry.Name))
		return
	}
	if len(args) < 2 {
		a.noticeForTab(tabID, fmt.Sprintf("effort for %s: %s (default: %s; options: %s)", entry.Name, config.EffortDisplay(entry), cap.Default, strings.Join(cap.Levels, "|")))
		return
	}
	if len(args) > 2 {
		a.noticeForTab(tabID, "usage: /effort "+strings.Join(cap.Levels, "|"))
		return
	}
	effort, err := config.NormalizeEffort(entry, args[1])
	if err != nil {
		a.noticeForTab(tabID, err.Error())
		return
	}
	if err := a.SetEffortForTab(tabID, args[1]); err != nil {
		a.noticeForTab(tabID, "effort: "+err.Error())
		return
	}
	display := effort
	if display == "" {
		display = "auto"
	}
	a.noticeForTab(tabID, fmt.Sprintf("effort for %s set to %s", entry.Name, display))
}

func (a *App) currentProviderEntryForTab(tabID string) (*config.ProviderEntry, error) {
	a.mu.RLock()
	ref := ""
	workspaceRoot := ""
	effortOverride := (*string)(nil)
	if tab := a.tabByIDLocked(tabID); tab != nil {
		ref = tab.model
		workspaceRoot = tab.WorkspaceRoot
		effortOverride = cloneStringPtr(tab.effort)
	}
	a.mu.RUnlock()
	cfg, err := config.LoadForRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref) == "" {
		ref = cfg.DefaultModel
	}
	resolved, _, ok := cfg.ResolveModelWithFallback(ref)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", ref)
	}
	entry, ok := cfg.ResolveModel(resolved)
	if !ok {
		return nil, fmt.Errorf("unknown model %q", resolved)
	}
	if effortOverride != nil {
		entry.Effort = *effortOverride
	}
	return entry, nil
}

// PickExportFile opens the native save dialog and returns the selected path. It
// returns "" when the user cancels.
func (a *App) PickExportFile(defaultFilename, mimeType string) (string, error) {
	if a.ctx == nil {
		return "", nil
	}
	defaultFilename = safeExportFilename(defaultFilename)
	ext := strings.ToLower(filepath.Ext(defaultFilename))
	path, err := a.nativeHost().SaveFileDialog(a.ctx, nativeDialogOptions{
		Title:                "Export session",
		DefaultDirectory:     dialogDefaultDirectory(a.activeWorkspaceRoot()),
		DefaultFilename:      defaultFilename,
		CanCreateDirectories: true,
		Filters:              exportFileFilters(mimeType, ext),
	})
	if err != nil || path == "" {
		return "", err
	}
	if ext != "" && filepath.Ext(path) == "" {
		path += ext
	}
	return path, nil
}

// SaveExportFile writes an exported session payload to a path previously picked
// by PickExportFile. An empty path is treated as a cancelled export.
func (a *App) SaveExportFile(path, payload string, base64Encoded bool) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	var data []byte
	var err error
	if base64Encoded {
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return fmt.Errorf("decode export payload: %w", err)
		}
	} else {
		data = []byte(payload)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return exportOperationError("save export file", path, err)
	}
	return nil
}

// SaveExportImageFiles writes one or more base64-encoded image parts. A single
// image keeps the native save dialog's normal overwrite semantics. Multi-part
// exports use numbered sibling paths and never overwrite an existing sibling;
// every payload is staged before any target is committed, and a failed commit
// removes only files created by this call.
func (a *App) SaveExportImageFiles(path string, payloads []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if len(payloads) == 0 {
		return errors.New("no image payloads to export")
	}
	if len(payloads) == 1 {
		return a.SaveExportFile(path, payloads[0], true)
	}

	targets := make([]string, len(payloads))
	for i := range payloads {
		targets[i] = numberedExportPath(path, i, len(payloads))
	}

	return saveExclusiveExportPayloads(targets, len(payloads), func(index int) ([]byte, error) {
		decoded, err := base64.StdEncoding.DecodeString(payloads[index])
		if err != nil {
			return nil, fmt.Errorf("decode export image part %d: %w", index+1, err)
		}
		return decoded, nil
	})
}

type stagedExportFile struct {
	targetPath string
	tempPath   string
}

type committedExportFile struct {
	path string
	info os.FileInfo
}

const exportTempCreateAttempts = 100

func saveExclusiveExportPayloads(targets []string, payloadCount int, payloadAt func(int) ([]byte, error)) error {
	if len(targets) == 0 || len(targets) != payloadCount || payloadAt == nil {
		return errors.New("invalid export image batch")
	}
	for _, target := range targets {
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("export file already exists: %s", filepath.Base(target))
		} else if !errors.Is(err, os.ErrNotExist) {
			return exportOperationError("inspect export target", target, err)
		}
	}

	staged := make([]stagedExportFile, 0, len(targets))
	defer func() {
		for _, file := range staged {
			_ = os.Remove(file.tempPath)
		}
	}()
	for i, target := range targets {
		payload, err := payloadAt(i)
		if err != nil {
			return err
		}
		file, finalMode, err := createExportTempFile(filepath.Dir(target))
		if err != nil {
			return exportOperationError("stage export file", target, err)
		}
		tempPath := file.Name()
		staged = append(staged, stagedExportFile{targetPath: target, tempPath: tempPath})
		if _, err = file.Write(payload); err == nil {
			err = file.Sync()
		}
		// Keep staged payloads private while they are incomplete, then restore
		// the same umask-adjusted mode used by SaveExportFile before publishing.
		if err == nil {
			err = file.Chmod(finalMode)
		}
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return exportOperationError("stage export file", target, err)
		}
	}

	committed := make([]committedExportFile, 0, len(staged))
	for _, file := range staged {
		info, err := commitStagedExportFile(file.tempPath, file.targetPath)
		if err != nil {
			rollbackCommittedExportFiles(committed)
			return exportOperationError("save export file", file.targetPath, err)
		}
		committed = append(committed, committedExportFile{path: file.targetPath, info: info})
	}
	return nil
}

// createExportTempFile reserves a cryptographically random sibling path with
// the same requested mode as a normal export. It immediately narrows the mode
// while bytes are staged; the caller restores finalMode only after the payload
// has been completely written and synced.
func createExportTempFile(dir string) (*os.File, os.FileMode, error) {
	for range exportTempCreateAttempts {
		var suffix [12]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, 0, fmt.Errorf("generate export temp name: %w", err)
		}
		path := filepath.Join(dir, ".deepzora-export-"+hex.EncodeToString(suffix[:]))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err == nil {
			err = file.Chmod(0o600)
		}
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return nil, 0, err
		}
		return file, info.Mode().Perm(), nil
	}
	return nil, 0, errors.New("could not reserve a unique export temp file")
}

func commitStagedExportFile(tempPath, targetPath string) (os.FileInfo, error) {
	stagedInfo, err := os.Lstat(tempPath)
	if err != nil {
		return nil, err
	}
	// A hard link publishes a fully written staged file atomically and fails if
	// the target already exists. Some filesystems do not support hard links, so
	// fall back to an exclusive create while preserving the no-overwrite rule.
	if err := os.Link(tempPath, targetPath); err == nil {
		current, statErr := os.Lstat(targetPath)
		if statErr != nil {
			removeExportFileIfSame(targetPath, stagedInfo)
			return nil, statErr
		}
		if !os.SameFile(current, stagedInfo) {
			return nil, errors.New("export target changed while it was being saved")
		}
		return stagedInfo, nil
	}

	source, err := os.Open(tempPath)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	info, statErr := target.Stat()
	if statErr == nil {
		_, err = io.Copy(target, source)
	}
	if err == nil && statErr == nil {
		err = target.Sync()
	}
	if closeErr := target.Close(); err == nil && statErr == nil {
		err = closeErr
	}
	if statErr != nil {
		err = statErr
	}
	if err != nil {
		removeExportFileIfSame(targetPath, info)
		return nil, err
	}
	return info, nil
}

func rollbackCommittedExportFiles(files []committedExportFile) {
	for _, file := range files {
		removeExportFileIfSame(file.path, file.info)
	}
}

func removeExportFileIfSame(path string, created os.FileInfo) {
	if created == nil {
		return
	}
	current, err := os.Lstat(path)
	if err == nil && os.SameFile(current, created) {
		_ = os.Remove(path)
	}
}

func exportOperationError(operation, path string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return fmt.Errorf("%s %s: %w", operation, filepath.Base(path), pathErr.Err)
	}
	return fmt.Errorf("%s %s: %w", operation, filepath.Base(path), err)
}

func safeExportFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "deepzora-session.md"
	}
	return filepath.Base(name)
}

func exportFileFilters(mimeType, ext string) []nativeFileFilter {
	switch mimeType {
	case "text/markdown":
		return []nativeFileFilter{{DisplayName: "Markdown (*.md)", Pattern: "*.md"}}
	case "application/json":
		return []nativeFileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}}
	case "application/pdf":
		return []nativeFileFilter{{DisplayName: "PDF (*.pdf)", Pattern: "*.pdf"}}
	case "image/png":
		return []nativeFileFilter{{DisplayName: "PNG image (*.png)", Pattern: "*.png"}}
	}
	if ext != "" {
		return []nativeFileFilter{{DisplayName: strings.ToUpper(strings.TrimPrefix(ext, ".")) + " files (*" + ext + ")", Pattern: "*" + ext}}
	}
	return []nativeFileFilter{{DisplayName: "All files (*.*)", Pattern: "*.*"}}
}

// memory panel (frontend ⇄ controller)

type MemoryImport struct {
	Path       string `json:"path"`
	SourcePath string `json:"sourcePath"`
}

// MemoryDoc is one resolved instruction file with applicability metadata.
type MemoryDoc struct {
	Path       string         `json:"path"`
	Scope      string         `json:"scope"`
	Directory  string         `json:"directory,omitempty"`
	Body       string         `json:"body"`
	Imports    []MemoryImport `json:"imports"`
	Depth      int            `json:"depth"`
	Order      int            `json:"order"`
	Precedence int            `json:"precedence"`
}

type InstructionDiagnostic struct {
	Code       string `json:"code"`
	Path       string `json:"path"`
	SourcePath string `json:"sourcePath,omitempty"`
	Line       int    `json:"line,omitempty"`
	Message    string `json:"message"`
}

// MemoryFact is one saved auto-memory, surfaced read-only in the panel.
type MemoryFact struct {
	ID          string `json:"id,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Body        string `json:"body"`
	Freshness   string `json:"freshness"`
}

type MemoryConflict struct {
	Key         string `json:"key"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	GlobalID    string `json:"globalId"`
	GlobalName  string `json:"globalName"`
	Resolution  string `json:"resolution"`
}

type MemoryRecallHit struct {
	ID        string  `json:"id"`
	Revision  int     `json:"revision"`
	Name      string  `json:"name"`
	Title     string  `json:"title,omitempty"`
	Type      string  `json:"type"`
	Scope     string  `json:"scope"`
	Score     float64 `json:"score"`
	Freshness string  `json:"freshness"`
	Reason    string  `json:"reason"`
	Snippet   string  `json:"snippet"`
}

type MemoryRecallTrace struct {
	Query      string            `json:"query"`
	Hits       []MemoryRecallHit `json:"hits"`
	Omitted    int               `json:"omitted"`
	CharBudget int               `json:"charBudget"`
	UsedChars  int               `json:"usedChars"`
	Suppressed string            `json:"suppressed,omitempty"`
}

// MemoryArchive is one archived auto-memory kept only for inspection.
type MemoryArchive struct {
	ID          string `json:"id,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Body        string `json:"body"`
	Freshness   string `json:"freshness"`
	Path        string `json:"path"`
	ArchivedAt  string `json:"archivedAt,omitempty"`
}

// MemoryScope is one writable quick-add target (scope id + the file it writes to).
type MemoryScope struct {
	Scope string `json:"scope"`
	Path  string `json:"path"`
}

// MemoryView is the whole memory panel payload: hierarchical docs, active saved
// facts, archived facts, and the writable scopes for the quick-add selector.
type MemoryView struct {
	Docs                   []MemoryDoc             `json:"docs"`
	Facts                  []MemoryFact            `json:"facts"`
	Archives               []MemoryArchive         `json:"archives"`
	Scopes                 []MemoryScope           `json:"scopes"`
	InstructionDiagnostics []InstructionDiagnostic `json:"instructionDiagnostics"`
	Conflicts              []MemoryConflict        `json:"conflicts"`
	LastRecall             MemoryRecallTrace       `json:"lastRecall"`
	StoreDir               string                  `json:"storeDir"`
	StoreGlobalDir         string                  `json:"storeGlobalDir,omitempty"`
	Available              bool                    `json:"available"`
}

// writableScopes are the quick-add targets the panel offers, broad → specific.
var writableScopes = []memory.Scope{memory.ScopeUser, memory.ScopeProject, memory.ScopeLocal}

// Memory returns the loaded memory for the panel: the DEEPZORA.md hierarchy,
// active/archived auto-memories, and the writable scopes. Read-only; mutations
// go through Remember / SaveDoc.
func (a *App) Memory() MemoryView {
	return a.memoryForCtrl(nil, true)
}

// MemoryForTab returns the loaded memory for a specific tab's controller,
// so the panel can show memory for any open project, not just the active tab.
// If the tab does not exist or has no controller, returns an empty view
// instead of falling back to the active tab (which would show the wrong data).
// An empty tabID is treated as "no tab specified" and falls back to the
// active tab for backward compatibility.
func (a *App) MemoryForTab(tabID string) MemoryView {
	if tabID == "" {
		return a.memoryForCtrl(nil, true)
	}
	return a.memoryForCtrl(a.ctrlByTabID(tabID), false)
}

func (a *App) memoryForCtrl(ctrl control.SessionAPI, fallback bool) MemoryView {
	view := emptyMemoryView()
	if ctrl == nil {
		if !fallback {
			return view
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return view
		}
	}
	set := ctrl.Memory()
	if set == nil {
		return view
	}
	view.StoreDir = set.Store.Dir
	view.StoreGlobalDir = set.Store.GlobalDir
	view.Available = true
	for _, d := range set.Docs {
		imports := make([]MemoryImport, 0, len(d.Imports))
		for _, imported := range d.Imports {
			imports = append(imports, MemoryImport{Path: imported.Path, SourcePath: imported.SourcePath})
		}
		view.Docs = append(view.Docs, MemoryDoc{
			Path: d.Path, Scope: string(d.Scope), Directory: d.Directory, Body: d.Body,
			Imports: imports, Depth: d.Depth, Order: d.Order, Precedence: d.Order,
		})
	}
	for _, diagnostic := range set.InstructionDiagnostics {
		view.InstructionDiagnostics = append(view.InstructionDiagnostics, InstructionDiagnostic{
			Code: diagnostic.Code, Path: diagnostic.Path, SourcePath: diagnostic.SourcePath,
			Line: diagnostic.Line, Message: diagnostic.Message,
		})
	}
	allFacts := set.Store.ListAll()
	for _, f := range allFacts {
		view.Facts = append(view.Facts, memoryFactView(f))
	}
	for _, conflict := range memory.FindOverrides(allFacts) {
		view.Conflicts = append(view.Conflicts, MemoryConflict{
			Key: conflict.Key, ProjectID: conflict.Project.ID, ProjectName: conflict.Project.Name,
			GlobalID: conflict.Global.ID, GlobalName: conflict.Global.Name, Resolution: "project_over_global",
		})
	}
	view.LastRecall = memoryRecallTraceView(ctrl.LastMemoryRecall())
	for _, f := range set.Store.ListArchived() {
		archivedAt := ""
		if !f.ArchivedAt.IsZero() {
			archivedAt = f.ArchivedAt.Format(time.RFC3339)
		}
		view.Archives = append(view.Archives, MemoryArchive{
			ID: f.ID, Revision: f.Revision, CreatedAt: formatMemoryTime(f.CreatedAt), UpdatedAt: formatMemoryTime(f.UpdatedAt),
			Name: f.Name, Title: f.Title, Description: f.Description, Type: string(f.Type), Scope: string(f.Scope), Body: f.Body,
			Freshness: memory.FreshnessFor(f.Memory, time.Now().UTC()), Path: f.Path, ArchivedAt: archivedAt,
		})
	}
	for _, sc := range writableScopes {
		if p := set.DocPath(sc); p != "" {
			view.Scopes = append(view.Scopes, MemoryScope{Scope: string(sc), Path: p})
		}
	}
	return view
}

func formatMemoryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func emptyMemoryView() MemoryView {
	return MemoryView{
		Docs: []MemoryDoc{}, Facts: []MemoryFact{}, Archives: []MemoryArchive{}, Scopes: []MemoryScope{},
		InstructionDiagnostics: []InstructionDiagnostic{}, Conflicts: []MemoryConflict{},
		LastRecall: MemoryRecallTrace{Hits: []MemoryRecallHit{}},
	}
}

// Remember quick-adds a one-line note to the doc-memory file for scope — the
// panel's explicit "remember" action, equivalent to typing "/remember <note>".
// An unknown scope falls back to project. Returns the file written.
func (a *App) Remember(scope, note string) (string, error) {
	return a.rememberForCtrl(nil, scope, note, true)
}

func (a *App) RememberForTab(tabID, scope, note string) (string, error) {
	if tabID == "" {
		return a.rememberForCtrl(nil, scope, note, true)
	}
	return a.rememberForCtrl(a.ctrlByTabID(tabID), scope, note, false)
}

func (a *App) rememberForCtrl(ctrl control.SessionAPI, scope, note string, fallback bool) (string, error) {
	if ctrl == nil {
		if !fallback {
			return "", nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return "", nil
		}
	}
	return ctrl.QuickAdd(parseScope(scope), note)
}

// Forget deletes a saved auto-memory by name — the panel's delete action for a
// fact the model owns. A no-op when no controller is attached.
func (a *App) Forget(name string) error {
	return a.forgetForCtrl(nil, name, true)
}

func (a *App) ForgetForTab(tabID, name string) error {
	if tabID == "" {
		return a.forgetForCtrl(nil, name, true)
	}
	return a.forgetForCtrl(a.ctrlByTabID(tabID), name, false)
}

func (a *App) forgetForCtrl(ctrl control.SessionAPI, name string, fallback bool) error {
	if ctrl == nil {
		if !fallback {
			return nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return nil
		}
	}
	return ctrl.ForgetMemory(name)
}

// RestoreArchivedMemory recovers one archived fact without replacing active
// memory. The store preserves its identity and creates a new audited revision.
func (a *App) RestoreArchivedMemory(archivePath string) (MemoryFact, error) {
	return a.restoreArchivedMemoryForCtrl(nil, archivePath, true)
}

func (a *App) RestoreArchivedMemoryForTab(tabID, archivePath string) (MemoryFact, error) {
	if tabID == "" {
		return a.restoreArchivedMemoryForCtrl(nil, archivePath, true)
	}
	return a.restoreArchivedMemoryForCtrl(a.ctrlByTabID(tabID), archivePath, false)
}

func (a *App) restoreArchivedMemoryForCtrl(ctrl control.SessionAPI, archivePath string, fallback bool) (MemoryFact, error) {
	if ctrl == nil {
		if !fallback {
			return MemoryFact{}, nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return MemoryFact{}, nil
		}
	}
	restored, err := ctrl.RestoreArchivedMemory(archivePath)
	if err != nil {
		return MemoryFact{}, err
	}
	return memoryFactView(restored), nil
}

func memoryFactView(f memory.Memory) MemoryFact {
	return MemoryFact{
		ID: f.ID, Revision: f.Revision, CreatedAt: formatMemoryTime(f.CreatedAt), UpdatedAt: formatMemoryTime(f.UpdatedAt),
		Name: f.Name, Title: f.Title, Description: f.Description, Type: string(f.Type), Scope: string(f.Scope), Body: f.Body,
		Freshness: memory.FreshnessFor(f, time.Now().UTC()),
	}
}

func memoryRecallTraceView(trace memory.RecallResult) MemoryRecallTrace {
	view := MemoryRecallTrace{
		Query: trace.Query, Hits: []MemoryRecallHit{}, Omitted: trace.Omitted,
		CharBudget: trace.CharBudget, UsedChars: trace.UsedChars, Suppressed: trace.Suppressed,
	}
	for _, hit := range trace.Hits {
		view.Hits = append(view.Hits, MemoryRecallHit{
			ID: hit.Memory.ID, Revision: hit.Memory.Revision, Name: hit.Memory.Name, Title: hit.Memory.Title,
			Type: string(hit.Memory.Type), Scope: string(hit.Memory.Scope), Score: hit.Score,
			Freshness: hit.Freshness, Reason: hit.Reason, Snippet: hit.Snippet,
		})
	}
	return view
}

func (a *App) MemoryRevisions(ref string) []MemoryFact {
	return a.memoryRevisionsForCtrl(nil, ref, true)
}

func (a *App) MemoryRevisionsForTab(tabID, ref string) []MemoryFact {
	if tabID == "" {
		return a.memoryRevisionsForCtrl(nil, ref, true)
	}
	return a.memoryRevisionsForCtrl(a.ctrlByTabID(tabID), ref, false)
}

func (a *App) memoryRevisionsForCtrl(ctrl control.SessionAPI, ref string, fallback bool) []MemoryFact {
	out := []MemoryFact{}
	if ctrl == nil {
		if !fallback {
			return out
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return out
		}
	}
	for _, revision := range ctrl.MemoryRevisions(ref) {
		out = append(out, memoryFactView(revision))
	}
	return out
}

func (a *App) RestoreMemoryRevision(ref string, revision int) (MemoryFact, error) {
	return a.restoreMemoryRevisionForCtrl(nil, ref, revision, true)
}

func (a *App) RestoreMemoryRevisionForTab(tabID, ref string, revision int) (MemoryFact, error) {
	if tabID == "" {
		return a.restoreMemoryRevisionForCtrl(nil, ref, revision, true)
	}
	return a.restoreMemoryRevisionForCtrl(a.ctrlByTabID(tabID), ref, revision, false)
}

func (a *App) restoreMemoryRevisionForCtrl(ctrl control.SessionAPI, ref string, revision int, fallback bool) (MemoryFact, error) {
	if ctrl == nil {
		if !fallback {
			return MemoryFact{}, nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return MemoryFact{}, nil
		}
	}
	restored, err := ctrl.RestoreMemory(ref, revision)
	if err != nil {
		return MemoryFact{}, err
	}
	return memoryFactView(restored), nil
}

// SaveDoc overwrites a memory doc with the panel editor's contents. The controller
// validates path against the recognized memory files. Returns the file written.
func (a *App) SaveDoc(path, body string) (string, error) {
	return a.saveDocForCtrl(nil, path, body, true)
}

func (a *App) SaveDocForTab(tabID, path, body string) (string, error) {
	if tabID == "" {
		return a.saveDocForCtrl(nil, path, body, true)
	}
	return a.saveDocForCtrl(a.ctrlByTabID(tabID), path, body, false)
}

func (a *App) saveDocForCtrl(ctrl control.SessionAPI, path, body string, fallback bool) (string, error) {
	if ctrl == nil {
		if !fallback {
			return "", nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return "", nil
		}
	}
	return ctrl.SaveDoc(path, body)
}

// parseScope maps a frontend scope id to a memory.Scope, defaulting to project.
func parseScope(s string) memory.Scope {
	switch memory.Scope(s) {
	case memory.ScopeUser:
		return memory.ScopeUser
	case memory.ScopeLocal:
		return memory.ScopeLocal
	default:
		return memory.ScopeProject
	}
}

// taskStore is the Store backing the task monitor panel.
func (a *App) taskStore() taskmonitor.WriteStore {
	return taskcatalog.ObservedStore()
}

// taskControl returns the process-wide ControlService backing the task
// monitor panel. A single instance keeps control operations serialized within
// this process (across processes the FileStore's per-task lock still
// arbitrates), and avoids re-creating the service on every Wails call.
func (a *App) taskControl() *taskmonitor.ControlService {
	a.taskCtrlOnce.Do(func() {
		a.taskCtrl = taskmonitor.NewControlService(a.taskStore())
	})
	return a.taskCtrl
}

func (a *App) projectDir() string {
	return a.activeWorkspaceRoot()
}

type taskMonitorTabTarget struct {
	projectDir  string
	sessionDir  string
	sessionPath string
	sessionID   string
}

// taskMonitorTargetForTab snapshots the workspace and session identity owned by
// tabID. Wails dispatches bound calls concurrently, so resolving the active tab
// inside a task operation would allow a later tab switch to retarget it.
func (a *App) taskMonitorTargetForTab(tabID string) (taskMonitorTabTarget, error) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return taskMonitorTabTarget{}, fmt.Errorf("task monitor tab id is required")
	}

	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return taskMonitorTabTarget{}, fmt.Errorf("task monitor tab %q is unavailable", tabID)
	}
	workspaceRoot := strings.TrimSpace(tab.WorkspaceRoot)
	sessionID := strings.TrimSpace(tab.SessionID)
	ctrl := tab.Ctrl
	a.mu.RUnlock()
	projectDir := workspaceRoot
	if projectDir == "" {
		projectDir = "."
	}
	sessionDir := desktopSessionDir(workspaceRoot)
	if ctrl != nil {
		if dir := strings.TrimSpace(ctrl.SessionDir()); dir != "" {
			sessionDir = dir
		}
		if id := controllerTaskSessionID(ctrl); id != "" {
			sessionID = id
		}
	}
	target := taskMonitorTabTarget{projectDir: projectDir, sessionDir: sessionDir, sessionID: sessionID}
	if sessionID != "" {
		target.sessionPath = sessionRoute(sessionID)
	}
	return target, nil
}

func (a *App) OpenTaskSessionForTab(tabID, taskID string) (taskmonitor.ControlResult, error) {
	target, err := a.taskMonitorTargetForTab(tabID)
	if err != nil {
		return taskmonitor.ControlResult{}, err
	}
	return a.taskControl().OpenTaskSession(a.ctx, target.projectDir, taskID)
}

type desktopTaskJobKiller struct {
	app        *App
	projectDir string
}

func (k desktopTaskJobKiller) Kill(sessionID, taskID string) bool {
	return k.kill(sessionID, taskID, "", false)
}

func (k desktopTaskJobKiller) KillOwned(sessionID, taskID, ownerID string) bool {
	if ownerID == "" {
		return false
	}
	return k.kill(sessionID, taskID, ownerID, true)
}

func (k desktopTaskJobKiller) kill(sessionID, taskID, ownerID string, requireOwner bool) bool {
	// Legacy task records without a session ID cannot be routed safely because
	// jobs.Manager IDs restart at task-1 for each controller.
	if k.app == nil || sessionID == "" || strings.TrimSpace(k.projectDir) == "" {
		return false
	}
	unlockRuntime := k.app.lockRuntimeMutation("stop task")
	defer unlockRuntime()

	k.app.mu.RLock()
	tabs := k.app.runtimeTabsLocked()
	controllers := make([]control.SessionAPI, 0, len(tabs))
	for _, tab := range tabs {
		if tab != nil && tab.Ctrl != nil && sameProjectRoot(tab.WorkspaceRoot, k.projectDir) {
			controllers = append(controllers, tab.Ctrl)
		}
	}
	k.app.mu.RUnlock()

	for _, ctrl := range controllers {
		if controllerTaskSessionID(ctrl) != sessionID {
			continue
		}
		if requireOwner {
			owner, ok := ctrl.(interface{ TaskRuntimeOwnerID() string })
			if !ok || owner.TaskRuntimeOwnerID() != ownerID {
				continue
			}
		}
		if killer, ok := ctrl.(interface{ CancelJob(string) bool }); ok && killer.CancelJob(taskID) {
			return true
		}
	}
	return false
}

// onboardingKeyEnv is the default provider (deepseek) key from config.Default().
const onboardingKeyEnv = "DEEPSEEK_API_KEY"

// onboardingBalanceURL doubles as a zero-token connectivity + auth probe:
// billing.FetchWithClient surfaces 401/403 for a bad key.
const onboardingBalanceURL = "https://api.deepseek.com/user/balance"

var connectKeyBalanceFetch = billing.FetchWithClient

// NativeConfirmRequest is the payload for ConfirmAction — a native OS confirmation
// dialog that replaces web-style confirm() for destructive or important actions.
type NativeConfirmRequest struct {
	Title        string `json:"title"`
	Message      string `json:"message"`
	Detail       string `json:"detail"`
	ConfirmLabel string `json:"confirmLabel"`
	CancelLabel  string `json:"cancelLabel"`
	Destructive  bool   `json:"destructive"`
}

// ConfirmAction shows a native confirmation dialog and returns true when the user
// clicks the confirm button. For destructive actions the dialog type is Warning so
// the platform can apply its danger styling (red tint on macOS, etc.).
func (a *App) ConfirmAction(req NativeConfirmRequest) (bool, error) {
	if a.ctx == nil {
		return false, nil
	}
	dialogType := nativeDialogQuestion
	if req.Destructive {
		dialogType = nativeDialogWarning
	}
	confirm := req.ConfirmLabel
	if confirm == "" {
		confirm = "OK"
	}
	cancel := req.CancelLabel
	if cancel == "" {
		cancel = "Cancel"
	}
	title := req.Title
	if title == "" {
		title = req.Message
	}
	body := req.Message
	if req.Detail != "" {
		if body != "" {
			body += "\n\n" + req.Detail
		} else {
			body = req.Detail
		}
	}
	defaultBtn := confirm
	if req.Destructive {
		// On destructive actions, make cancel the default so Enter / Space
		// does NOT accidentally confirm. ESC always maps to CancelButton.
		defaultBtn = cancel
	}
	result, err := a.nativeHost().MessageDialog(a.ctx, nativeMessageOptions{
		Type:          dialogType,
		Title:         title,
		Message:       body,
		Buttons:       []string{confirm, cancel},
		DefaultButton: defaultBtn,
		CancelButton:  cancel,
	})
	if err != nil {
		return false, err
	}
	return result == confirm, nil
}

func (a *App) NeedsOnboarding() bool {
	cfg, err := config.LoadForRootReadOnly(a.activeWorkspaceRoot())
	if err != nil {
		// Configuration errors already surface through the startup error banner.
		// Do not cover their recovery path with an onboarding gate.
		return false
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if !modelProviderAccessAllowed(cfg.Desktop.ProviderAccess, p.Name) || !p.Configured() || len(p.ChatModelList()) == 0 {
			continue
		}
		return false
	}
	return true
}

// ConnectKey validates apiKey against the balance endpoint, persists it to
// Deepzora's global .env, and rebuilds the controller so the new key takes effect.
func (a *App) ConnectKey(apiKey string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("key is required")
	}
	ctx, cancel := context.WithTimeout(a.reqCtx(), 8*time.Second)
	defer cancel()
	if _, err := connectKeyBalanceFetch(ctx, nil, onboardingBalanceURL, apiKey); err != nil {
		return "", fmt.Errorf("validate: %w", err)
	}
	return a.AddOfficialProviderAccess("deepseek", apiKey)
}
