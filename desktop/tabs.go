package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"deepzora/desktop/internal/workspacestate"
	"deepzora/internal/agent"
	"deepzora/internal/billing"
	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/extension/providerext"
	"deepzora/internal/fileutil"
	"deepzora/internal/notify"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/sessiontitle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// WorkspaceTab

var errNoDesktopChatModel = errors.New("no desktop chat model is available; add a chat-capable provider in Settings > Model > Access")

func resolveDraftCreateModelStrict(cfg *config.Config, model string) (string, error) {
	if providerext.PluginRefOwner(model) != "" {
		return model, nil
	}
	resolved, ok := cfg.ResolveModel(model)
	if !ok {
		return "", fmt.Errorf("%w: %q", boot.ErrUnknownModel, model)
	}
	return resolved.Name + "/" + resolved.Model, nil
}

// WorkspaceTab is one open conversation tab in the desktop. Each tab owns an
// independent controller (its own agent, session, tool registry, plugin host,
// memory, permissions) scoped to a workspace root, so multiple projects and
// topics can be active concurrently without interfering.
type WorkspaceTab struct {
	ID                       string                     // stable random id
	Scope                    string                     // "project" | "global"
	WorkspaceRoot            string                     // project root dir (empty for global)
	SessionWorkspace         desktopTabWorkspace        // stable Workspace registry identity
	SharedHostKey            string                     // opaque key for the shared plugin host (set by buildTabController)
	TopicID                  string                     // topic within the project
	TopicTitle               string                     // display title
	topicTitleSource         string                     // auto or manual; controls localization at API boundaries
	SessionID                string                     // immutable v3 identity; empty for legacy/read-only tabs
	PendingCreateOperationID string                     // durable create reservation used before the first turn
	persistenceExtra         map[string]json.RawMessage // unknown desktop-tabs.json fields retained across rewrites
	SessionGeneration        uint64                     // bumps on session rotation (clear/new); frontend hydrate identity
	ReadOnly                 bool                       // true for external channel transcripts opened for browsing
	Takeover                 struct{ Spectator bool }   // handoff state grouped by its cross-runtime lifetime
	Ctrl                     control.SessionAPI         // nil while booting / on error
	Label                    string                     // model label (for the tab badge)
	Ready                    bool                       // true once boot.Build completes
	StartupErr               string                     // build error, surfaced to the frontend
	StartupErrLeaseHeld      bool                       // true when StartupErr can be retried after a session lease releases
	modelApplication         tabModelApplicationState   // guarded by App.mu; never persisted
	runtimeID                string                     // process-local SessionRuntime registry identity
	sink                     *tabEventSink              // routes events with this tab's ID
	buildCancel              context.CancelFunc         // cancels in-flight boot for tabs removed before Ready
	buildGeneration          uint64                     // identifies the current in-flight build
	// buildDone is closed exactly once when the build that owns buildDoneGen
	// terminates (success, failure, or superseded abandon). Topic-activation
	// completions wait on it to learn that the controller build finished
	// without polling. Guarded by App.mu alongside buildGeneration; always
	// nil-ed after close so a replacement build can install a fresh channel.
	buildDone       chan struct{}
	buildDoneGen    uint64
	buildExecution  *tabBuildExecution              // actual completion, never closed by supersession
	buildExecutions map[*tabBuildExecution]struct{} // includes superseded builds until actual exit
	removed         bool                            // set when the visible tab is pruned/closed before build completes
	turnStartMu     sync.Mutex                      // serializes foreground turn admission for this tab

	ActivityStatus string // transient project-tree status for the in-flight turn

	saveMu       sync.Mutex
	saving       bool
	saveAgain    bool
	saveFailures int
	// lastAutosaveWarnAt debounces the user-facing autosave-failure notice:
	// a persistently failing disk (AV hold, full volume) otherwise emits a
	// chat warning for every completed turn. Logs are never debounced.
	lastAutosaveWarnAt time.Time

	// closing is set under saveMu when the tab is being torn down. Once set,
	// tabSnapshotLoop stops taking new snapshot work and CloseTab waits on
	// saveCond until any in-flight snapshot finishes - so no background
	// snapshot can write a session file back to disk after CloseTab returns.
	// Without this, deleting a just-closed session races that write and the
	// session "resurrects" (#4384).
	closing  bool
	saveCond *sync.Cond

	// readTelemetry tracks files read during this tab's session.
	readTelemetry  []readFileRecord
	usageTelemetry sessionUsageStats
	// runtimeCostQuote is an automatic wallet-currency hint for the live tab.
	// It is deliberately outside usageTelemetry so it cannot be persisted into
	// telemetry/history or become configuration. Guarded by telemMu.
	runtimeCostDisplayCurrency string
	runtimeCostQuote           *billing.CostQuote
	runtimeCostGeneration      uint64 // invalidates stale wallet responses
	// telemetrySessionKey is the sessionRuntimeKey the telemetry above belongs
	// to. Controller-side session rotations (typed /new, bot /reset) bypass the
	// App bindings, so telemetry writers and readers re-key through
	// syncTelemetryToSession before trusting the in-memory totals — otherwise a
	// previous session's cost keeps accumulating under the new session and gets
	// persisted into its sidecar (#5850).
	telemetrySessionKey string
	telemMu             sync.Mutex

	model            string // active model ref (for meta)
	effort           *string
	mode             string // "normal" | "plan" | "yolo" | "plan-yolo"; yolo/full access is runtime-only
	goal             string
	toolApprovalMode string
	disabledMCP      map[string]ServerView
	mcpOrder         []string
	lastBuildResult  *boot.BuildResult // incremental extension reload

	PinnedFiles   []string
	pinnedFilesMu sync.RWMutex

	// metaExtras caches the expensive MetaForTab fields (git branch, image
	// input capability) computed off the request path by
	// refreshTabMetaExtras. Lock-free reads keep MetaForTab synchronous and
	// cheap; refresh dedup goes through metaExtrasRefreshing.
	metaExtras           atomic.Pointer[tabMetaExtras]
	metaExtrasRefreshing atomic.Bool
}

const (
	topicStatusThinking            = "thinking"
	topicStatusStreaming           = "streaming"
	topicStatusWaitingConfirmation = "waiting_confirmation"
	topicStatusBackgroundJob       = "background_job"
	topicStatusPaused              = "paused"
	topicStatusError               = "error"
)

type readFileRecord struct {
	Path      string `json:"path"`
	Turn      int    `json:"turn"`
	Time      int64  `json:"time"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type sessionUsageStats struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
	ReasoningTokens  int `json:"reasoningTokens"`
	CacheHitTokens   int `json:"cacheHitTokens"`
	CacheMissTokens  int `json:"cacheMissTokens"`
	CacheWriteTokens int `json:"cacheWriteTokens,omitempty"`
	// CacheWriteBilledTokens preserves provider-specific cache-write pricing
	// across persisted telemetry repricing without changing hit-rate totals.
	CacheWriteBilledTokens float64 `json:"cacheWriteBilledTokens,omitempty"`
	Estimated              bool    `json:"estimated,omitempty"`
	// LastUsedTokens is the executor-reported context fill (prompt+completion)
	// from the most recent turn. It is persisted so the status bar / context
	// panel can show a meaningful fill percentage after a session rebind
	// rebuilds the controller (which resets the in-memory executor state).
	LastUsedTokens int `json:"lastUsedTokens,omitempty"`
	// Per-turn token breakdown from the most recent turn. Persisted separately
	// from the cumulative totals above so the context-panel donut chart and
	// type breakdown survive a session rebind (which resets executor.LastUsage).
	LastPromptTokens     int     `json:"lastPromptTokens,omitempty"`
	LastCompletionTokens int     `json:"lastCompletionTokens,omitempty"`
	LastReasoningTokens  int     `json:"lastReasoningTokens,omitempty"`
	LastCacheHitTokens   int     `json:"lastCacheHitTokens,omitempty"`
	LastCacheMissTokens  int     `json:"lastCacheMissTokens,omitempty"`
	LastEstimated        bool    `json:"lastEstimated,omitempty"`
	RequestCount         int     `json:"requestCount"`
	ElapsedMs            int64   `json:"elapsedMs"`
	SessionCost          float64 `json:"sessionCost,omitempty"`
	SessionCurrency      string  `json:"sessionCurrency,omitempty"`
	// SessionCostComplete is false when any entry lacks a shared display valuation.
	SessionCostComplete bool `json:"sessionCostComplete,omitempty"`
	// CostLedger stores occurrence-time quotes keyed by model+source+fingerprint+rateDate.
	CostLedger *billing.Ledger `json:"costLedger,omitempty"`
	// SessionCostQuote is the aggregate quote for the current display currency.
	SessionCostQuote *billing.CostQuote          `json:"sessionCostQuote,omitempty"`
	Sources          map[string]usageSourceStats `json:"sources,omitempty"`

	activeTurnStartedAt int64
	sourceSessionCache  map[string]sourceSessionCacheCounters
}

type usageSourceStats struct {
	PromptTokens           int     `json:"promptTokens"`
	CompletionTokens       int     `json:"completionTokens"`
	TotalTokens            int     `json:"totalTokens"`
	ReasoningTokens        int     `json:"reasoningTokens"`
	CacheHitTokens         int     `json:"cacheHitTokens"`
	CacheMissTokens        int     `json:"cacheMissTokens"`
	CacheWriteTokens       int     `json:"cacheWriteTokens,omitempty"`
	CacheWriteBilledTokens float64 `json:"cacheWriteBilledTokens,omitempty"`
	Estimated              bool    `json:"estimated,omitempty"`
	RequestCount           int     `json:"requestCount"`
	SessionCost            float64 `json:"sessionCost,omitempty"`
	SessionCurrency        string  `json:"sessionCurrency,omitempty"`
}

type sourceSessionCacheCounters struct {
	Hit  int
	Miss int
}

func cloneSessionUsageStats(in sessionUsageStats) sessionUsageStats {
	out := in
	if len(in.Sources) > 0 {
		out.Sources = make(map[string]usageSourceStats, len(in.Sources))
		maps.Copy(out.Sources, in.Sources)
	}
	if len(in.sourceSessionCache) > 0 {
		out.sourceSessionCache = make(map[string]sourceSessionCacheCounters, len(in.sourceSessionCache))
		maps.Copy(out.sourceSessionCache, in.sourceSessionCache)
	}
	return out
}

func (s *sessionUsageStats) cacheTokenDelta(source string, u *provider.Usage, sessionHit, sessionMiss int) (hit, miss int) {
	if u != nil {
		hit = u.CacheHitTokens
		miss = u.CacheMissTokens
	}
	if source != event.UsageSourceExecutor && source != event.UsageSourcePlanner {
		return hit, miss
	}
	if sessionHit+sessionMiss <= 0 {
		return hit, miss
	}
	if s.sourceSessionCache == nil {
		s.sourceSessionCache = map[string]sourceSessionCacheCounters{}
	}
	prev, ok := s.sourceSessionCache[source]
	s.sourceSessionCache[source] = sourceSessionCacheCounters{Hit: sessionHit, Miss: sessionMiss}
	if !ok {
		return sessionHit, sessionMiss
	}
	if sessionHit < prev.Hit || sessionMiss < prev.Miss {
		if hit+miss > 0 {
			return hit, miss
		}
		return sessionHit, sessionMiss
	}
	return sessionHit - prev.Hit, sessionMiss - prev.Miss
}

type tabTelemetrySnapshot struct {
	Version   int               `json:"version"`
	ReadFiles []readFileRecord  `json:"readFiles"`
	Usage     sessionUsageStats `json:"usage"`
}

func cloneStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func cloneServerViewMap(in map[string]ServerView) map[string]ServerView {
	out := make(map[string]ServerView, len(in))
	for name, view := range in {
		view.EnvKeys = append([]string(nil), view.EnvKeys...)
		view.HeaderKeys = append([]string(nil), view.HeaderKeys...)
		out[name] = view
	}
	return out
}

func (t *WorkspaceTab) currentSessionIdentity() string {
	if t == nil {
		return ""
	}
	if id := strings.TrimSpace(t.SessionID); id != "" {
		return sessionRoute(id)
	}
	if identity, ok := t.Ctrl.(control.IdentityLifecycle); ok && identity.UsesExclusiveSession() {
		if ref, bound := identity.SessionRef(); bound {
			return sessionRoute(ref.SessionID)
		}
	}
	return ""
}

func (t *WorkspaceTab) hasActiveRuntimeWork() bool {
	if t == nil || t.Ctrl == nil {
		return false
	}
	status := t.Ctrl.RuntimeStateSnapshot()
	return status.Running || status.PendingPrompt || status.BackgroundJobs > 0
}

// sessionRuntimeKey accepts only current session routes, without filesystem I/O.
func sessionRuntimeKey(route string) string {
	locator := classifySessionLocator(route)
	if locator.kind == sessionLocatorCanonical {
		return sessionRoute(locator.ref.SessionID)
	}
	return ""
}

func detachedRuntimeTabID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "detached_" + hex.EncodeToString(sum[:8])
}

func (a *App) ensureDetachedSessionsLocked() {
	if a.detachedSessions == nil {
		a.detachedSessions = map[string]*WorkspaceTab{}
	}
}

func (a *App) runtimeTabsLocked() []*WorkspaceTab {
	seen := map[*WorkspaceTab]bool{}
	out := make([]*WorkspaceTab, 0, len(a.tabs)+len(a.detachedSessions))
	for _, tab := range a.tabs {
		if tab != nil && !seen[tab] {
			seen[tab] = true
			out = append(out, tab)
		}
	}
	for _, tab := range a.detachedSessions {
		if tab != nil && !seen[tab] {
			seen[tab] = true
			out = append(out, tab)
		}
	}
	return out
}

func (a *App) tabByEventSinkIDLocked(tabID string) *WorkspaceTab {
	if tab := a.tabs[tabID]; tab != nil {
		return tab
	}
	for _, tab := range a.detachedSessions {
		if tab != nil && tab.ID == tabID {
			return tab
		}
	}
	return nil
}

func (a *App) detachSessionRuntime(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	a.mu.RLock()
	ctrl := tab.Ctrl
	fallbackIdentity := strings.TrimSpace(tab.currentSessionIdentity())
	sink := tab.sink
	a.mu.RUnlock()
	identity := fallbackIdentity
	if ctrl != nil && tab.SessionID == "" {
		if p := strings.TrimSpace(ctrl.SessionRoute()); p != "" {
			identity = p
		}
	}
	key := sessionRuntimeKey(identity)
	if key == "" {
		return false
	}
	if sink != nil {
		sink.clearContext()
	}
	a.mu.Lock()
	setTabSessionIdentity(tab, identity)
	a.registerDetachedRuntimeLocked(tab)
	a.mu.Unlock()
	return true
}

func setTabSessionIdentity(tab *WorkspaceTab, identity string) {
	if tab == nil {
		return
	}
	locator := classifySessionLocator(identity)
	if locator.kind == sessionLocatorCanonical {
		tab.SessionID = locator.ref.SessionID
		return
	}
	tab.SessionID = ""
}

// cloneDetachedRuntimeTab copies a running tab's runtime state into a fresh
// detached tab. Callers must hold a.mu: the copied fields (Ctrl, Ready,
// ActivityStatus, disabledMCP, ...) are written under a.mu by bound methods
// and the event sink, and the disabledMCP map read would otherwise race those
// writers. key identifies the runtime registry entry; path is its display route.
func cloneDetachedRuntimeTab(tab *WorkspaceTab, key, path string) *WorkspaceTab {
	if tab == nil {
		return nil
	}
	tab.telemMu.Lock()
	readTelemetry := append([]readFileRecord(nil), tab.readTelemetry...)
	usageTelemetry := cloneSessionUsageStats(tab.usageTelemetry)
	telemetrySessionKey := tab.telemetrySessionKey
	tab.telemMu.Unlock()
	pinnedFiles := tab.pinnedFilesState()

	detached := &WorkspaceTab{
		ID:                  detachedRuntimeTabID(key),
		Scope:               tab.Scope,
		WorkspaceRoot:       tab.WorkspaceRoot,
		SessionWorkspace:    tab.SessionWorkspace,
		SharedHostKey:       tab.SharedHostKey,
		TopicID:             tab.TopicID,
		TopicTitle:          tab.TopicTitle,
		topicTitleSource:    tab.topicTitleSource,
		Ctrl:                tab.Ctrl,
		Label:               tab.Label,
		Ready:               tab.Ready,
		StartupErr:          tab.StartupErr,
		StartupErrLeaseHeld: tab.StartupErrLeaseHeld,
		modelApplication:    tab.modelApplication,
		lastBuildResult:     tab.lastBuildResult,
		runtimeID:           tab.runtimeID,
		sink:                tab.sink,
		ActivityStatus:      tab.ActivityStatus,
		readTelemetry:       readTelemetry,
		usageTelemetry:      usageTelemetry,
		telemetrySessionKey: telemetrySessionKey,
		model:               tab.model,
		effort:              cloneStringPtr(tab.effort),
		mode:                tab.mode,
		goal:                tab.goal,
		toolApprovalMode:    tab.toolApprovalMode,
		disabledMCP:         cloneServerViewMap(tab.disabledMCP),
		mcpOrder:            append([]string(nil), tab.mcpOrder...),
		PinnedFiles:         pinnedFiles,
	}
	if tab.SessionID != "" {
		setTabSessionIdentity(detached, sessionRoute(tab.SessionID))
	} else {
		setTabSessionIdentity(detached, path)
	}
	return detached
}

func (a *App) detachRuntimeForReplacement(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	// One a.mu critical section covers the membership check, the field
	// snapshot, the sink handover, and the re-publication:
	//   - the clone reads fields that bound methods and the event sink write
	//     under a.mu (ActivityStatus every event, disabledMCP is a map);
	//   - inserting the clone without re-checking a.tabs would resurrect a
	//     runtime that DeleteSession/TrashTopic/RemoveWorkspace already
	//     unlinked and closed (the "session resurrects" class, #4384);
	//   - publishing before the sink handover would let a concurrent
	//     attachExistingSessionRuntime claim a half-initialized clone.
	a.mu.Lock()
	detached := a.detachRuntimeForReplacementLocked(tab)
	a.mu.Unlock()
	return detached
}

// detachRuntimeForReplacementLocked transfers a visible tab's live runtime to
// the detached registry without releasing its controller's runtime binding.
// Callers must hold App.mu. The transfer itself performs no file or host I/O.
func (a *App) detachRuntimeForReplacementLocked(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	if tab.removed || a.tabs[tab.ID] != tab {
		return false
	}
	sourceIdentity := tab.currentSessionIdentity()
	key := sessionRuntimeKey(sourceIdentity)
	if key == "" {
		return false
	}
	detached := cloneDetachedRuntimeTab(tab, key, tab.currentSessionIdentity())
	if detached == nil {
		return false
	}
	if rt := a.runtimeForTabLocked(tab); rt != nil {
		rt.Owner = detached
		detached.runtimeID = rt.ID
		tab.runtimeID = ""
	}
	if detached.sink != nil {
		detached.sink.setBinding(detached.ID, nil)
		// clearContext (locked nil + drain the queued emitter), not a bare
		// ctx=nil: the latter both data-races s.ctx and leaves already-queued
		// events to flush onto the rebound tab after this session is backgrounded
		// (#5352 — stale "AI 不断输出" on the now-visible session).
		detached.sink.clearContext()
	}
	a.registerDetachedRuntimeLocked(detached)
	return true
}

// applyRuntimeTab moves source's runtime (controller, sink, telemetry)
// onto target. path is the real session path for display/persistence; the
// case-folded runtime key must never be written into SessionPath.
func applyRuntimeTab(target, source *WorkspaceTab, path string, appCtx context.Context, app *App) {
	if target == nil || source == nil {
		return
	}
	if app != nil && target.ID != source.ID {
		// Detached owners can acquire browser grants too. Retire the previous
		// surface's grant before publishing the runtime's new binding.
		app.forgetBrowserExecutorLocked(source.ID)
	}
	source.telemMu.Lock()
	readTelemetry := append([]readFileRecord(nil), source.readTelemetry...)
	usageTelemetry := cloneSessionUsageStats(source.usageTelemetry)
	telemetrySessionKey := source.telemetrySessionKey
	source.telemMu.Unlock()
	pinnedFiles := source.pinnedFilesState()

	// Share the runtime-owned display state before rebinding the sink. An event
	// already routed to source and one arriving on target after setBinding then
	// append under the same state lock instead of straddling two buffers.
	if source.sink != nil {
		source.sink.setBinding(target.ID, app)
		source.sink.setSessionGeneration(target.SessionGeneration)
		source.sink.setContext(appCtx)
	}

	target.Ctrl = source.Ctrl
	target.modelApplication.failure = source.modelApplication.failure
	target.lastBuildResult = source.lastBuildResult
	target.sink = source.sink
	if source.SessionID != "" {
		target.SessionID = source.SessionID
	} else {
		setTabSessionIdentity(target, path)
	}
	target.SharedHostKey = source.SharedHostKey
	target.Label = source.Label
	target.Ready = source.Ready && source.Ctrl != nil
	clearTabStartupError(target)
	target.ActivityStatus = source.ActivityStatus
	target.model = source.model
	target.effort = cloneStringPtr(source.effort)
	target.mode = source.mode
	target.goal = source.goal
	target.toolApprovalMode = source.toolApprovalMode
	target.disabledMCP = cloneServerViewMap(source.disabledMCP)
	target.mcpOrder = append([]string(nil), source.mcpOrder...)
	target.setPinnedFiles(pinnedFiles)
	target.replaceTelemetry(tabTelemetrySnapshot{ReadFiles: readTelemetry, Usage: usageTelemetry}, telemetrySessionKey)
	if app != nil {
		key := sessionRuntimeKey(path)
		rt := app.runtimeForTabLocked(source)
		targetRuntime := app.runtimeForTabLocked(target)
		if rt == nil {
			rt = targetRuntime
		}
		if rt == nil {
			rt = app.newSessionRuntimeLocked(source, key)
		} else if targetRuntime != nil && targetRuntime != rt {
			app.removeSessionRuntimeMappingsLocked(targetRuntime)
			target.runtimeID = ""
		}
		if source.Ctrl != nil && source.Ready {
			rt.Phase = sessionRuntimeReady
			rt.Issue = nil
			closeRuntimeReadyChannelLocked(rt)
		}
		rt.Owner = target
		if rt.Key != "" && rt.Key != key && app.runtimeBySessionKey[rt.Key] == rt {
			delete(app.runtimeBySessionKey, rt.Key)
		}
		rt.Key = key
		app.runtimeBySessionKey[key] = rt
		target.runtimeID = rt.ID
		source.runtimeID = ""
		if target.sink != nil {
			target.sink.setRuntimeEpoch(rt.Epoch)
		}
	}
}

func (a *App) attachExistingSessionRuntimeCore(tab *WorkspaceTab, path string, appCtx context.Context) bool {
	key := sessionRuntimeKey(path)
	if tab == nil || key == "" {
		return false
	}

	a.mu.Lock()
	if tab.removed || a.tabs[tab.ID] != tab {
		a.mu.Unlock()
		return false
	}
	if rt := a.runtimeBySessionKey[key]; rt != nil && !a.runtimeOwnerLiveLocked(rt) {
		a.removeSessionRuntimeMappingsLocked(rt)
	}
	registered := a.runtimeBySessionKey[key]
	if registered != nil && registered.Phase == sessionRuntimeStarting && registered.Owner != tab {
		// A starting runtime owns only an admission placeholder; its controller,
		// lease, and sink have not been published yet. Moving that tab would
		// supersede the owner build while the attaching build closes its own
		// candidate, leaving the session permanently starting with no controller.
		// claimSessionRuntime waits on readyCh and retries the attach after the
		// owner publishes a terminal phase. The owner itself may still adopt a
		// usable legacy runtime that predates the registry.
		a.mu.Unlock()
		return false
	}
	attachable := func(source *WorkspaceTab) bool {
		if source == nil || source.Ctrl == nil {
			return false
		}
		if rt := a.runtimeForTabLocked(source); rt != nil {
			return rt.Phase == sessionRuntimeReady
		}
		// Compatibility for visible/detached runtimes constructed before the
		// process-local registry existed.
		return source.Ready
	}
	detached := a.liveRuntimeTabMatchingLocked(tab, path)
	if detached != nil && a.tabs[detached.ID] == detached {
		detached = nil
	}
	if detached == nil && key != "" {
		if rt := a.runtimeBySessionKey[key]; rt != nil && rt.Owner != nil && rt.Owner != tab {
			detached = rt.Owner
			if a.tabs[detached.ID] == detached {
				detached = nil
			}
		}
	}
	if detached != nil {
		if !attachable(detached) {
			a.mu.Unlock()
			return false
		}
		a.unregisterDetachedRuntimeLocked(detached)
		applyRuntimeTab(tab, detached, runtimeAttachIdentity(detached, path), appCtx, a)
		if current := a.tabs[tab.ID]; current == tab {
			a.saveTabsLocked()
		}
		attachedCtrl := tab.Ctrl
		attachedSink := tab.sink
		attachedEpoch := a.runtimeEpochForTabLocked(tab)
		a.mu.Unlock()
		a.replayPendingPromptsAfterRuntimeAttach(tab.ID, attachedSink, attachedCtrl, attachedEpoch)
		return true
	}

	source := a.liveRuntimeTabMatchingLocked(tab, path)
	if source == nil && key != "" {
		if rt := a.runtimeBySessionKey[key]; rt != nil && rt.Owner != nil && rt.Owner != tab {
			source = rt.Owner
		}
	}
	if source == nil {
		a.mu.Unlock()
		return false
	}
	if !attachable(source) {
		a.mu.Unlock()
		return false
	}
	delete(a.tabs, source.ID)
	a.removeTabOrderLocked(source.ID)
	if a.activeTabID == source.ID {
		a.activeTabID = tab.ID
	}
	applyRuntimeTab(tab, source, runtimeAttachIdentity(source, path), appCtx, a)
	a.saveTabsLocked()
	attachedCtrl := tab.Ctrl
	attachedSink := tab.sink
	attachedEpoch := a.runtimeEpochForTabLocked(tab)
	a.mu.Unlock()
	if path != "" && !tab.ReadOnly {
		a.attachTakeoverMirror(tab.ID, path)
		go a.adoptSessionFromLocalServe(tab.ID, path)
	}

	a.replayPendingPromptsAfterRuntimeAttach(tab.ID, attachedSink, attachedCtrl, attachedEpoch)
	return true
}

func (t *WorkspaceTab) recordReadFile(rec readFileRecord) {
	t.telemMu.Lock()
	t.readTelemetry = append(t.readTelemetry, rec)
	t.telemMu.Unlock()
}

func (t *WorkspaceTab) recordTurnDone(now int64) {
	t.telemMu.Lock()
	if started := t.usageTelemetry.activeTurnStartedAt; started > 0 && now >= started {
		t.usageTelemetry.ElapsedMs += now - started
		t.usageTelemetry.activeTurnStartedAt = 0
	}
	t.telemMu.Unlock()
}

// contextTelemetryFromUsage returns the latest-attempt context shape for
// rebind-surviving Last* telemetry fields. Prefer Context* when set (multi-
// attempt sampling recovery); otherwise fall back to billable totals / the
// per-event cache delta already computed for this Usage event.
//
// When a Context shape is present, ContextCacheHit/Miss are kept even if both
// are zero — many providers omit cache splits, and falling back to the
// event's aggregated cache would re-inflate multi-attempt totals.
func contextTelemetryFromUsage(u *provider.Usage, eventCacheHit, eventCacheMiss int) (prompt, completion, reasoning, hit, miss int) {
	if u == nil {
		return 0, 0, 0, eventCacheHit, eventCacheMiss
	}
	if u.ContextPromptTokens > 0 || u.ContextCompletionTokens > 0 {
		return u.ContextPromptTokens, u.ContextCompletionTokens, u.ContextReasoningTokens,
			u.ContextCacheHitTokens, u.ContextCacheMissTokens
	}
	return u.PromptTokens, u.CompletionTokens, u.ReasoningTokens, eventCacheHit, eventCacheMiss
}

func (t *WorkspaceTab) recordUsage(e event.Event) {
	if e.Usage == nil {
		return
	}
	u := e.Usage
	source := strings.TrimSpace(e.UsageSource)
	if source == "" {
		source = event.UsageSourceExecutor
	}
	t.telemMu.Lock()
	t.usageTelemetry.PromptTokens += u.PromptTokens
	t.usageTelemetry.CompletionTokens += u.CompletionTokens
	t.usageTelemetry.TotalTokens += u.TotalTokens
	t.usageTelemetry.ReasoningTokens += u.ReasoningTokens
	cacheHitTokens, cacheMissTokens := t.usageTelemetry.cacheTokenDelta(source, u, e.SessionHit, e.SessionMiss)
	t.usageTelemetry.CacheHitTokens += cacheHitTokens
	t.usageTelemetry.CacheMissTokens += cacheMissTokens
	t.usageTelemetry.CacheWriteTokens += u.CacheWriteTokens
	t.usageTelemetry.CacheWriteBilledTokens += u.CacheWriteBilledTokens
	t.usageTelemetry.Estimated = t.usageTelemetry.Estimated || u.Estimated
	requestCount := u.RequestCount
	if requestCount <= 0 {
		requestCount = 1
	}
	t.usageTelemetry.RequestCount += requestCount
	if source == event.UsageSourceExecutor {
		// Persist the latest-attempt context shape for rebind fallback — never
		// the multi-attempt billable aggregate (PromptTokens/CompletionTokens
		// after stream recovery). ContextSnapshot semantics are latest
		// prompt+completion; Context* fields carry that shape.
		prompt, completion, reasoning, hit, miss := contextTelemetryFromUsage(u, cacheHitTokens, cacheMissTokens)
		t.usageTelemetry.LastUsedTokens = prompt + completion
		t.usageTelemetry.LastPromptTokens = prompt
		t.usageTelemetry.LastCompletionTokens = completion
		t.usageTelemetry.LastReasoningTokens = reasoning
		t.usageTelemetry.LastCacheHitTokens = hit
		t.usageTelemetry.LastCacheMissTokens = miss
		t.usageTelemetry.LastEstimated = u.Estimated
	}
	if t.usageTelemetry.Sources == nil {
		t.usageTelemetry.Sources = map[string]usageSourceStats{}
	}
	src := t.usageTelemetry.Sources[source]
	src.PromptTokens += u.PromptTokens
	src.CompletionTokens += u.CompletionTokens
	src.TotalTokens += u.TotalTokens
	src.ReasoningTokens += u.ReasoningTokens
	src.CacheHitTokens += cacheHitTokens
	src.CacheMissTokens += cacheMissTokens
	src.CacheWriteTokens += u.CacheWriteTokens
	src.CacheWriteBilledTokens += u.CacheWriteBilledTokens
	src.Estimated = src.Estimated || u.Estimated
	src.RequestCount += requestCount
	// Prefer the middleware CostQuote; fall back only when older emitters omit it.
	q := e.CostQuote
	if q == nil && e.Pricing != nil {
		q = event.EnsureCostQuote(e, nil)
	}
	if q != nil {
		if t.usageTelemetry.CostLedger == nil {
			t.usageTelemetry.CostLedger = billing.NewLedger()
		}
		tokens := billing.UsageTokens{
			PromptTokens:           u.PromptTokens,
			CompletionTokens:       u.CompletionTokens,
			CacheHitTokens:         cacheHitTokens,
			CacheMissTokens:        cacheMissTokens,
			CacheWriteTokens:       u.CacheWriteTokens,
			CacheWriteBilledTokens: u.CacheWriteBilledTokens,
			Estimated:              u.Estimated,
		}
		t.usageTelemetry.CostLedger.Add(*q, tokens, time.Now().UTC())
		display := billing.NormalizeCurrency(t.runtimeCostDisplayCurrency)
		if display == "" {
			display = billing.NormalizeCurrency(t.usageTelemetry.SessionCurrency)
		}
		if display == "" && q.Selected != nil {
			display = billing.NormalizeCurrency(q.Selected.Currency)
		}
		if display == "" {
			display = billing.NormalizeCurrency(q.Original.Currency)
		}
		total := t.usageTelemetry.CostLedger.Total(display)
		if t.runtimeCostDisplayCurrency != "" {
			t.runtimeCostQuote = &total
		} else {
			t.usageTelemetry.SessionCostQuote = &total
			t.usageTelemetry.SessionCostComplete = total.Complete
		}
		if total.Selected != nil {
			if t.runtimeCostDisplayCurrency == "" {
				t.usageTelemetry.SessionCost = total.Selected.Float64()
				t.usageTelemetry.SessionCurrency = total.SelectedCurrencyCode()
			}
			src.SessionCost += q.SelectedCostFloat()
			src.SessionCurrency = total.SelectedCurrencySymbol()
		} else {
			// Incomplete: never invent a zero total by wiping prior costs.
			if t.runtimeCostDisplayCurrency == "" {
				t.usageTelemetry.SessionCostComplete = false
				t.usageTelemetry.SessionCost = 0
				t.usageTelemetry.SessionCurrency = ""
			}
			if q.Selected == nil {
				src.SessionCurrency = billing.CurrencySymbol(q.Original.Currency)
			}
		}
	}
	t.usageTelemetry.Sources[source] = src
	t.telemMu.Unlock()
}

func (a *App) repriceTabUsageForCurrentCurrency(tab *WorkspaceTab) {
	if a == nil || tab == nil {
		return
	}
	a.mu.RLock()
	root := tab.WorkspaceRoot
	a.mu.RUnlock()
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		return
	}
	// Display preference only — automatic mode remains unresolved until a
	// wallet-aware surface supplies a session hint.
	display := cfg.ExplicitDisplayCurrency()
	if !tab.selectDisplayCurrency(display) {
		return
	}
	if identity := tab.currentSessionIdentity(); identity != "" {
		_ = saveTelemetryFor(identity, tab.telemetrySnapshot())
	}
}

func (t *WorkspaceTab) telemetrySnapshot() tabTelemetrySnapshot {
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	return t.telemetrySnapshotLocked()
}

func (t *WorkspaceTab) telemetrySnapshotLocked() tabTelemetrySnapshot {
	records := make([]readFileRecord, len(t.readTelemetry))
	copy(records, t.readTelemetry)
	usage := t.usageTelemetry
	if started := usage.activeTurnStartedAt; started > 0 {
		now := time.Now().UnixMilli()
		if now >= started {
			usage.ElapsedMs += now - started
		}
	}
	if len(t.usageTelemetry.Sources) > 0 {
		usage.Sources = make(map[string]usageSourceStats, len(t.usageTelemetry.Sources))
		maps.Copy(usage.Sources, t.usageTelemetry.Sources)
	}
	usage.activeTurnStartedAt = 0
	usage.sourceSessionCache = nil
	return tabTelemetrySnapshot{Version: 3, ReadFiles: records, Usage: usage}
}

// displayTelemetrySnapshot overlays the live wallet hint onto a copy used by
// UI reads. The persisted snapshot remains the occurrence-time/original view.
func (t *WorkspaceTab) displayTelemetrySnapshot() tabTelemetrySnapshot {
	t.telemMu.Lock()
	defer t.telemMu.Unlock()
	return t.displayTelemetrySnapshotLocked()
}

func (t *WorkspaceTab) displayTelemetrySnapshotLocked() tabTelemetrySnapshot {
	snapshot := t.telemetrySnapshotLocked()
	quote := t.runtimeCostQuote
	if quote == nil {
		return snapshot
	}
	snapshot.Usage.SessionCostQuote = quote
	snapshot.Usage.SessionCostComplete = quote.Complete
	if quote.Selected != nil {
		snapshot.Usage.SessionCost = quote.Selected.Float64()
		snapshot.Usage.SessionCurrency = quote.SelectedCurrencyCode()
	} else {
		snapshot.Usage.SessionCostComplete = false
		snapshot.Usage.SessionCost = 0
		snapshot.Usage.SessionCurrency = ""
	}
	return snapshot
}

func (t *WorkspaceTab) resetTelemetry(sessionPath string) {
	t.telemMu.Lock()
	t.readTelemetry = nil
	t.usageTelemetry = sessionUsageStats{}
	t.runtimeCostDisplayCurrency = ""
	t.runtimeCostQuote = nil
	t.runtimeCostGeneration++
	t.telemetrySessionKey = sessionRuntimeKey(sessionPath)
	t.telemMu.Unlock()
}

// syncTelemetryToSession keys the in-memory telemetry to the runtime's current
// session. When the runtime rotated to a different session underneath the tab
// (typed /new routes through Controller.Submit and never reaches App.NewSession),
// the previous session's totals must not bleed into the new one: swap in the
// new session's persisted sidecar, or start from zero when none exists. The
// sidecar is rewritten on every recorded event, so a reload never loses more
// than the sub-second in-memory delta of an in-flight record.
func (t *WorkspaceTab) syncTelemetryToSession(sessionPath string) {
	key := sessionRuntimeKey(sessionPath)
	if key == "" {
		return
	}
	t.telemMu.Lock()
	same := t.telemetrySessionKey == key
	t.telemMu.Unlock()
	if same {
		return
	}
	// File I/O stays outside telemMu; re-check the key after reacquiring in
	// case a concurrent sync or reset re-keyed the tab first.
	snapshot := loadTelemetryFor(sessionPath)
	t.telemMu.Lock()
	if t.telemetrySessionKey != key {
		t.readTelemetry = snapshot.ReadFiles
		t.usageTelemetry = snapshot.Usage
		t.runtimeCostDisplayCurrency = ""
		t.runtimeCostQuote = nil
		t.runtimeCostGeneration++
		t.telemetrySessionKey = key
	}
	t.telemMu.Unlock()
}

// tabEventSink wraps a parent event.Sink and prepends a tabId to every wire
// event so the frontend can route it to the correct tab's reducer.
//
// tabID and app are rebound while the controller keeps emitting when a running
// session is detached to the background or reattached to another tab, so they
// live under mu like ctx does (a bare field write would data-race Emit). Read
// them via binding(), write via setBinding().
type tabEventSink struct {
	tabID             string
	app               *App
	mu                sync.RWMutex
	ctx               context.Context
	runtimeEpoch      string
	sessionGeneration uint64 // source session binding generation
	runtimeEvents     asyncRuntimeEmitter
	turn              turnSubmissionState // stays reserved through the end of TurnDone fan-out
	// takeoverMirror, when set, forwards every event to the serve that used to
	// own this session so the remote tab keeps rendering after a local
	// takeover. Atomic so Emit reads it without the sink lock.
	takeoverMirror atomic.Pointer[takeoverMirror]
}

// setTakeoverMirror installs (or clears) the session-takeover frame mirror.
func (s *tabEventSink) setTakeoverMirror(m *takeoverMirror) {
	if s == nil {
		return
	}
	s.takeoverMirror.Store(m)
}

// binding snapshots the sink's current tab routing under the sink lock.
func (s *tabEventSink) binding() (string, *App) {
	if s == nil {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tabID, s.app
}

func (s *tabEventSink) runtimeEpochSnapshot() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtimeEpoch
}

func (s *tabEventSink) Emit(e event.Event) {
	// Typed-nil sinks can appear as non-nil event.Sink interfaces when a tab
	// controller is built before the tab sink binding is installed.
	if s == nil {
		return
	}
	if e.Kind == event.TurnStarted || e.Kind == event.TurnDone {
		s.mu.Lock()
		s.turn.phase = turnSubmissionActive
		s.mu.Unlock()
	}
	tabID, app := s.binding()
	var turnStartedAt int64
	if app != nil {
		if e.Kind == event.TurnDone {
			// Keep the legacy completion as a cheap missed-event safety net. The
			// hub owns the actual resource invalidation and coalesces this probe.
			app.reconcileWorkspaceForTab(tabID)
		}
		switch e.Kind {
		case event.TurnStarted:
			turnStartedAt = s.recordTurnStarted()
		case event.Usage:
			s.recordUsageTelemetry(e)
		case event.TurnDone:
			s.recordTurnDone()
		}
		if m := app.metrics.Load(); m != nil {
			m.observe(e)
			persistMetricsEvent(app, m, tabID, e)
		}
	}
	s.emitRuntimeEvent(eventChannel, toWireTabWithSubmission(e, tabID, s.runtimeEpochSnapshot(), s.submissionIDSnapshot(), turnStartedAt, s.sessionGenerationSnapshot()))
	if m := s.takeoverMirror.Load(); m != nil {
		m.forwardEvent(e)
	}
	if app != nil {
		if status, update := topicActivityStatusFromEvent(e); update {
			changed := app.setTabActivityStatus(tabID, status)
			if changed || isBackgroundJobLifecycleNotice(e) {
				// Runtime status is an in-memory projection, not catalog metadata.
				// Publish it directly so a turn never fans out into one catalog read
				// per expanded project folder.
				app.emitProjectTreeRuntimeChanged()
			}
		}
	}
	// Record read_file successes in the tab's telemetry.
	if e.Kind == event.ToolResult && e.Tool.Name == "read_file" && e.Tool.Err == "" {
		s.recordReadTelemetry(e)
	}
	// Persist after each turn so a force-kill loses at most the in-flight prompt.
	if e.Kind == event.TurnDone && app != nil {
		app.scheduleTabSnapshot(tabID)
	}
	if e.Kind == event.TurnDone {
		s.mu.Lock()
		s.turn = turnSubmissionState{}
		s.mu.Unlock()
	}
}

// tryBeginTurn reserves the tab until its TurnDone has finished fan-out. The
// controller clears RuntimeStateSnapshot().Running before it emits TurnDone, so the
// controller status alone leaves a window where a new turn can inherit the old
// turn's forwarder or have its replacement cleared by the old completion.
func (s *tabEventSink) tryBeginTurn(submissionID ...string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn.phase != turnSubmissionIdle {
		return false
	}
	s.turn = turnSubmissionState{phase: turnSubmissionReserved, submissionID: firstSubmissionID(submissionID)}
	return true
}

func (s *tabEventSink) cancelTurnStart() {
	s.mu.Lock()
	// A management command can finish after queued work has started. Only its
	// provisional reservation may be released; TurnDone owns a started turn.
	if s.turn.phase == turnSubmissionReserved {
		s.turn = turnSubmissionState{}
	}
	s.mu.Unlock()
}

func (s *tabEventSink) setContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

func (s *tabEventSink) context() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ctx
}

func (s *tabEventSink) emitRuntimeEvent(name string, payload ...any) {
	if s == nil {
		return
	}
	ctx := s.context()
	if ctx == nil {
		return
	}
	s.runtimeEvents.Emit(ctx, name, payload...)
}

type runtimeEventEmitFunc func(context.Context, string, ...any)

type runtimeEventEnvelope struct {
	ctx     context.Context
	name    string
	payload []any
}

// asyncRuntimeEmitter decouples the host event bridge from agent emission.
// Emit can block when the event channel backs up; callers enqueue in-order
// work and return without holding the agent event lock.
// runtimeEventsEmitFallback is the emit used when no per-instance override is
// installed. Production replaces it with the host RPC server emit in
// runHostRPC; the test binary swaps in a no-op via TestMain.
var runtimeEventsEmitFallback runtimeEventEmitFunc = func(_ context.Context, name string, _ ...any) {
	slog.Debug("desktop: runtime event dropped without a host shell", "name", name)
}

type asyncRuntimeEmitter struct {
	mu                     sync.Mutex
	emit                   runtimeEventEmitFunc
	queue                  []runtimeEventEnvelope
	head                   int
	running                bool
	configWarningsRevision atomic.Uint64
}

func (e *asyncRuntimeEmitter) Emit(ctx context.Context, name string, payload ...any) {
	if ctx == nil {
		return
	}
	item := runtimeEventEnvelope{
		ctx:     ctx,
		name:    name,
		payload: append([]any(nil), payload...),
	}
	e.mu.Lock()
	e.queue = append(e.queue, item)
	if !e.running {
		e.running = true
		go e.run()
	}
	e.mu.Unlock()
}

func (e *asyncRuntimeEmitter) Clear() {
	e.mu.Lock()
	clear(e.queue)
	e.queue = nil
	e.head = 0
	e.mu.Unlock()
}

func (e *asyncRuntimeEmitter) run() {
	for {
		e.mu.Lock()
		if e.head >= len(e.queue) {
			clear(e.queue)
			e.queue = nil
			e.head = 0
			e.running = false
			e.mu.Unlock()
			return
		}
		item := e.queue[e.head]
		var zero runtimeEventEnvelope
		e.queue[e.head] = zero
		e.head++
		if e.head > 64 && e.head*2 >= len(e.queue) {
			e.queue = append([]runtimeEventEnvelope(nil), e.queue[e.head:]...)
			e.head = 0
		}
		emit := e.emit
		if emit == nil {
			emit = runtimeEventsEmitFallback
		}
		e.mu.Unlock()

		emit(item.ctx, item.name, item.payload...)
	}
}

func topicActivityStatusFromEvent(e event.Event) (string, bool) {
	switch e.Kind {
	case event.TurnStarted, event.Reasoning, event.ToolDispatch, event.ToolProgress, event.ToolResultPreview, event.ToolResult, event.CompactionStarted, event.Retrying:
		return topicStatusThinking, true
	// A manual /compact runs outside a turn, so no TurnDone follows to clear it;
	// any other trigger compacts inside a running turn, which is still thinking.
	case event.CompactionDone:
		if e.Compaction.Trigger == agent.CompactionTriggerManual {
			return "", true
		}
		return topicStatusThinking, true
	case event.Text, event.Message:
		return topicStatusStreaming, true
	case event.ApprovalRequest, event.AskRequest:
		return topicStatusWaitingConfirmation, true
	case event.TurnDone:
		if status, ok := topicStatusFromTurnDone(e.Outcome); ok {
			return status, true
		}
		if e.Err != nil {
			return topicStatusError, true
		}
		return "", true
	case event.Notice:
		if isBackgroundJobLifecycleNotice(e) {
			return "", true
		}
		return "", false
	default:
		return "", false
	}
}

func isBackgroundJobLifecycleNotice(e event.Event) bool {
	if e.Kind != event.Notice {
		return false
	}
	text := strings.TrimSpace(e.Text)
	return strings.HasPrefix(text, "background ") &&
		(strings.Contains(text, " started: ") ||
			strings.Contains(text, " finished: ") ||
			strings.Contains(text, " failed: ") ||
			strings.Contains(text, " killed: "))
}

// notifyTabRuntimeRebuilt tells the frontend a tab's controller was replaced
// in place (model/effort/token-mode switch, clear-while-running). A rebuilt
// controller restarts its approval/ask id counter at "1", so tab-scoped
// frontend state keyed by prompt id (the attention-chime dedupe) must reset —
// unlike agent:ready, this event carries no reload semantics, so emitting it
// on every swap adds no hydration churn.
//
// Ordering matters: the reset must reach the frontend BEFORE the rebuilt
// controller's first approval/ask event, or the stale key still mutes it. The
// tab's agent events ride the tab sink's own async queue, so the notice goes
// through THAT queue — same lane, FIFO, guaranteed to arrive first. The
// App-level queue is only the fallback when the sink cannot deliver (no sink,
// or its webview context is cleared); it cannot order against sink traffic,
// but an unordered notice still beats none.
func (a *App) notifyTabRuntimeRebuilt(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	a.mu.Lock()
	epoch := a.advanceSessionRuntimeEpochLocked(tab)
	a.mu.Unlock()
	a.notifyTabRuntimeRebuiltAtEpoch(tab, epoch)
}

// notifyTabRuntimeRebuiltAtEpoch emits the rebuild fence for a transaction
// that advanced its epoch inside the controller/path/lease commit. Keeping the
// chosen epoch avoids a second generation bump after publication.
func (a *App) notifyTabRuntimeRebuiltAtEpoch(tab *WorkspaceTab, epoch string) {
	if tab == nil {
		return
	}
	a.mu.RLock()
	sink := tab.sink
	tabID := tab.ID
	ctrl, _ := tab.Ctrl.(*control.Controller)
	a.mu.RUnlock()
	if ctrl != nil {
		go ctrl.NotifyInboxRuntimeReady()
	}
	if sink != nil && sink.context() != nil {
		sink.emitRuntimeEvent("runtime:rebuilt", tabID, epoch)
		return
	}
	a.emitRuntimeEvent("runtime:rebuilt", tabID, epoch)
}

// replayPendingPromptsAfterRuntimeAttach publishes the runtime generation on
// the tab sink before asking the same controller to replay. Both events use the
// sink's FIFO queue, so the frontend cannot reject a valid prompt as belonging
// to the runtime that was just replaced.
func (a *App) replayPendingPromptsAfterRuntimeAttach(tabID string, sink *tabEventSink, ctrl control.SessionAPI, epoch string) {
	if ctrl == nil {
		return
	}
	if sink != nil && sink.context() != nil {
		// Use the sink captured in the same App.mu commit as ctrl. Re-reading the
		// tab here would let a concurrent replacement put the fence on a newer
		// sink while this older controller replays on the transferred one.
		sink.emitRuntimeEvent("runtime:rebuilt", tabID, epoch)
	} else {
		a.emitRuntimeEvent("runtime:rebuilt", tabID, epoch)
	}
	ctrl.ReplayPendingPrompts()
}

func (a *App) emitReady(ctx context.Context, tabID ...string) {
	a.mu.RLock()
	hook := a.readyHook
	a.mu.RUnlock()
	if hook != nil {
		hook()
		return
	}
	if ctx != nil {
		if len(tabID) > 0 && strings.TrimSpace(tabID[0]) != "" {
			a.runtimeEvents.Emit(ctx, "agent:ready", strings.TrimSpace(tabID[0]))
			return
		}
		a.runtimeEvents.Emit(ctx, "agent:ready")
	}
}

func (s *tabEventSink) recordReadTelemetry(e event.Event) {
	tabID, app := s.binding()
	if app == nil {
		return
	}
	app.mu.RLock()
	tab := app.tabByEventSinkIDLocked(tabID)
	var ctrl control.SessionAPI
	if tab != nil {
		ctrl = tab.Ctrl
	}
	app.mu.RUnlock()
	if tab == nil {
		return
	}
	turn := 0
	if ctrl != nil {
		turn = ctrl.Turn()
	}

	// Parse read_file args: {"path": "...", "offset": N, "limit": N}
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	path := e.Tool.Args
	offset := 0
	limit := 0
	if err := json.Unmarshal([]byte(e.Tool.Args), &args); err == nil && args.Path != "" {
		path = args.Path
		offset = args.Offset
		limit = args.Limit
	}

	truncated := e.Tool.Truncated || strings.Contains(e.Tool.Output, "truncated") ||
		strings.Contains(e.Tool.Output, "File truncated")

	_, sp := s.telemetryTab()
	if sp != "" {
		tab.syncTelemetryToSession(sp)
	}
	tab.recordReadFile(readFileRecord{
		Path:      path,
		Turn:      turn,
		Time:      time.Now().UnixMilli(),
		Offset:    offset,
		Limit:     limit,
		Truncated: truncated,
	})
	if sp != "" {
		_ = saveTelemetryFor(sp, tab.telemetrySnapshot())
	}
}

func (s *tabEventSink) recordTurnStarted() int64 {
	tab, sp := s.telemetryTab()
	if tab == nil {
		return 0
	}
	if sp != "" {
		tab.syncTelemetryToSession(sp)
	}
	startedAt := tab.recordTurnStarted(time.Now().UnixMilli())
	if sp != "" {
		_ = saveTelemetryFor(sp, tab.telemetrySnapshot())
	}
	return startedAt
}

func (s *tabEventSink) recordTurnDone() {
	tab, sp := s.telemetryTab()
	if tab == nil {
		return
	}
	if sp != "" {
		tab.syncTelemetryToSession(sp)
	}
	tab.recordTurnDone(time.Now().UnixMilli())
	if sp != "" {
		_ = saveTelemetryFor(sp, tab.telemetrySnapshot())
	}
}

func (s *tabEventSink) recordUsageTelemetry(e event.Event) {
	tab, sp := s.telemetryTab()
	if tab == nil {
		return
	}
	if sp != "" {
		tab.syncTelemetryToSession(sp)
	}
	tab.recordUsage(e)
	if sp != "" {
		_ = saveTelemetryFor(sp, tab.telemetrySnapshot())
	}
}

func (s *tabEventSink) telemetryTab() (*WorkspaceTab, string) {
	tabID, app := s.binding()
	if app == nil {
		return nil, ""
	}
	app.mu.RLock()
	tab := app.tabByEventSinkIDLocked(tabID)
	var ctrl control.SessionAPI
	if tab != nil {
		ctrl = tab.Ctrl
	}
	app.mu.RUnlock()
	if tab == nil {
		return nil, ""
	}
	if ctrl == nil {
		return tab, ""
	}
	if sp := ctrl.SessionRoute(); sp != "" {
		return tab, sp
	}
	if lifecycle, ok := ctrl.(control.IdentityLifecycle); ok {
		if ref, ok := lifecycle.SessionRef(); ok {
			return tab, sessionRoute(ref.SessionID)
		}
	}
	return tab, ""
}

// wire event with tab

func toWireTab(e event.Event, tabID string, runtimeEpoch ...string) wireEventTab {
	w := eventwire.ToWire(e)
	epoch := ""
	if len(runtimeEpoch) > 0 {
		epoch = runtimeEpoch[0]
	}
	return wireEventTab{
		Event:             w,
		TabID:             tabID,
		RuntimeEpoch:      epoch,
		SessionHitTokens:  e.SessionHit,
		SessionMissTokens: e.SessionMiss,
		SessionCost:       0, // filled by frontend accumulator per tab
		SessionCurrency:   "",
	}
}

// wireEventTab extends the shared event wire with tab routing info. The frontend reducer
// uses tabId to dispatch to the correct per-tab state.
type wireEventTab struct {
	eventwire.Event
	TabID             string `json:"tabId"`
	RuntimeEpoch      string `json:"runtimeEpoch,omitempty"`
	SessionGeneration uint64 `json:"sessionGeneration,omitempty"`
	TurnStartedAt     int64  `json:"turnStartedAt,omitempty"`
	// Session-cumulative tokens per tab.
	SessionHitTokens  int `json:"sessionHitTokens,omitempty"`
	SessionMissTokens int `json:"sessionMissTokens,omitempty"`
	// SessionCost is filled by the frontend's per-tab accumulator.
	SessionCost     float64 `json:"sessionCost,omitempty"`
	SessionCurrency string  `json:"sessionCurrency,omitempty"`
}

// Tab management on App

func enrichTabMeta(meta TabMeta) TabMeta {
	if meta.Active {
		meta.GitBranch = workspaceGitBranchForMeta(meta.WorkspaceRoot, meta.repo)
	}
	return meta
}

func enrichTabMetas(metas []TabMeta) []TabMeta {
	for i := range metas {
		if metas[i].Active {
			metas[i].GitBranch = workspaceGitBranchForMeta(metas[i].WorkspaceRoot, metas[i].repo)
		}
	}
	return metas
}

func (a *App) tabMeta(tab *WorkspaceTab, active bool) TabMeta {
	runtimeView := a.sessionRuntimeViewLocked(tab)
	sessionRevision, sessionDigest, _ := a.canonicalTabHistoryFingerprint(tab)
	isolated := tabUsesIsolatedWorktree(tab)
	m := TabMeta{
		ID:                tab.ID,
		Scope:             tab.Scope,
		WorkspaceRoot:     tab.WorkspaceRoot,
		WorkspaceID:       tab.SessionWorkspace.ID,
		WorkspaceName:     workspaceName(tab.WorkspaceRoot),
		WorkspacePath:     tab.WorkspaceRoot,
		TopicID:           tab.TopicID,
		TopicTitle:        a.localizedTopicTitle(tab.TopicTitle, tab.topicTitleSource),
		SessionID:         tab.SessionID,
		SessionRevision:   sessionRevision,
		SessionDigest:     sessionDigest,
		SessionGeneration: tab.SessionGeneration,
		ReadOnly:          tab.ReadOnly,
		TakenOver:         tab.Takeover.Spectator,
		Label:             tab.Label,
		Ready:             runtimeView.Phase == sessionRuntimeReady && tab.Ctrl != nil,
		Runtime:           runtimeView,
		TurnStartedAt:     tab.turnStartedAt(),
		Mode:              currentTabMode(tab),
		CollaborationMode: currentTabCollaborationMode(tab),
		ToolApprovalMode:  currentTabToolApprovalMode(tab),
		Goal:              currentTabGoal(tab),
		GoalStatus:        currentTabGoalStatus(tab),
		StartupErr:        tab.StartupErr,
		Active:            active,
		Cwd:               tab.WorkspaceRoot,
		IsolatedWorktree:  isolated,
	}
	if repo, ok := sessionWorkspaceRepo(tab.Ctrl, tab.WorkspaceRoot); ok {
		m.repo = repo
	}
	if strings.TrimSpace(tab.SessionID) != "" {
		m.Session = &session.SessionRef{HostID: "local", SessionID: strings.TrimSpace(tab.SessionID)}
	}
	switch tab.Scope {
	case "global":
		m.ProjectColor = globalProjectColor()
		m.WorkspaceName = globalProjectTitle()
	case "project":
		m.ProjectColor = projectColor(tab.WorkspaceRoot)
	}
	if tab.Ctrl != nil {
		m.setAuthenticationMeta(tab)
		status := tab.Ctrl.RuntimeStateSnapshot()
		m.GoalView = status.Goal
		m.Running = status.Running || status.PendingPrompt || status.BackgroundJobs > 0
		m.PendingPrompt = status.PendingPrompt
		m.BackgroundJobs = status.BackgroundJobs
		m.CancelRequested = status.CancelRequested
		m.Cancellable = status.Cancellable
		m.TurnID = status.TurnID
		m.TurnStatus = string(status.TurnStatus)
		m.TurnEventSeq = status.TurnEventSeq
	}
	return m
}

// ListTabs returns every open view container's metadata for the frontend chrome and sidebar.
func (a *App) ListTabs() []TabMeta {
	a.mu.RLock()
	out := make([]TabMeta, 0, len(a.tabs))
	ordered, needsRepair := a.orderedTabIDsSnapshotLocked()
	for _, id := range ordered {
		if tab := a.tabs[id]; tab != nil {
			out = append(out, a.tabMeta(tab, tab.ID == a.activeTabID))
		}
	}
	a.mu.RUnlock()
	if !needsRepair {
		return a.listTabsWithRemote(out)
	}

	a.mu.Lock()
	out = make([]TabMeta, 0, len(a.tabs))
	for _, id := range a.orderedTabIDsLocked() {
		if tab := a.tabs[id]; tab != nil {
			out = append(out, a.tabMeta(tab, tab.ID == a.activeTabID))
		}
	}
	a.mu.Unlock()
	return a.listTabsWithRemote(out)
}

// syncTabWorkspaceRootSpellings repoints visible and detached project runtimes
// at the registry spelling. Registry writes may adopt the caller's spelling,
// while the frontend compares roots exactly. Callers must not hold a.mu.
func (a *App) syncTabWorkspaceRootSpellings() {
	projects := loadProjectsFile().Projects
	a.mu.Lock()
	changed := false
	for _, tab := range a.tabs {
		changed = syncRuntimeWorkspaceRootSpelling(tab, projects) || changed
	}
	for _, tab := range a.detachedSessions {
		changed = syncRuntimeWorkspaceRootSpelling(tab, projects) || changed
	}
	if changed {
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	if changed {
		a.emitProjectTreeMetadataChanged()
	}
}

// OpenProjectTab builds a controller scoped to workspaceRoot and opens the
// session selected by the given topic. Topic selection resolves to a concrete
// session path first; the visible tab is then attached to that session runtime.
func (a *App) OpenProjectTab(workspaceRoot, topicID string) (TabMeta, error) {
	return a.openProjectTab(workspaceRoot, topicID)
}

func (a *App) openProjectTab(workspaceRoot, topicID string) (TabMeta, error) {
	if workspaceRoot == "" {
		return TabMeta{}, fmt.Errorf("workspaceRoot is required")
	}
	if abs, err := filepath.Abs(workspaceRoot); err == nil {
		workspaceRoot = abs
	}

	sessionPath, err := a.resolveTopicOpenPath(topicID)
	if err != nil {
		return TabMeta{}, err
	}
	return a.openTopicTabWithActivation("project", workspaceRoot, topicID, sessionPath, true)
}

func (a *App) openProjectTabInactive(workspaceRoot, topicID string) (TabMeta, error) {
	if workspaceRoot == "" {
		return TabMeta{}, fmt.Errorf("workspaceRoot is required")
	}
	if abs, err := filepath.Abs(workspaceRoot); err == nil {
		workspaceRoot = abs
	}

	sessionPath, err := a.resolveTopicOpenPath(topicID)
	if err != nil {
		return TabMeta{}, err
	}
	return a.openTopicTabWithActivation("project", workspaceRoot, topicID, sessionPath, false)
}

func (a *App) openGlobalTabInactive(topicID string) (TabMeta, error) {
	globalRoot := globalWorkspaceRoot()
	if err := os.MkdirAll(globalRoot, 0o755); err != nil {
		return TabMeta{}, fmt.Errorf("create global workspace: %w", err)
	}

	sessionPath, err := a.resolveTopicOpenPath(topicID)
	if err != nil {
		return TabMeta{}, err
	}
	return a.openTopicTabWithActivation("global", "", topicID, sessionPath, false)
}

func (a *App) openTopicTabWithActivation(scope, workspaceRoot, topicID, sessionPath string, activate bool, navigation ...uint64) (TabMeta, error) {
	if err := a.validatePlaceholderTopicOpen(topicID, sessionPath); err != nil {
		return TabMeta{}, err
	}
	target, canonical, err := a.canonicalTopicOpen(sessionPath)
	if err != nil {
		return TabMeta{}, err
	}
	var actualRoot string
	if canonical != nil {
		scope, workspaceRoot, topicID = target.Scope, target.WorkspaceRoot, target.TopicID
		actualRoot = desktopWorkspaceRoot(scope, workspaceRoot)
	} else {
		actualRoot = desktopWorkspaceRoot(scope, workspaceRoot)
	}
	releaseAdmission, err := a.beginProjectRuntimeAdmission(scope, actualRoot)
	if err != nil {
		return TabMeta{}, err
	}
	defer releaseAdmission()
	if strings.TrimSpace(scope) == "project" {
		saveWorkspace(actualRoot)
		if err := a.registerProjectRoot(actualRoot); err != nil {
			return TabMeta{}, err
		}
	}
	targetKey := sessionRuntimeKey(sessionPath)

	a.mu.Lock()
	if len(navigation) != 0 && a.desktopSessions.navigationSeq.Load() != navigation[0] {
		a.mu.Unlock()
		return TabMeta{}, errSessionNavigationSuperseded
	}

	if targetKey != "" {
		for _, tab := range a.tabs {
			if !topicTabReusableLocked(tab) {
				continue
			}
			if sessionRuntimeKeysOverlap(tab, sessionPath) {
				if activate {
					a.activeTabID = tab.ID
				}
				meta := a.tabMeta(tab, tab.ID == a.activeTabID)
				a.saveTabsLocked()
				a.mu.Unlock()
				return enrichTabMeta(meta), nil
			}
		}
	}

	for _, tab := range a.tabs {
		if targetKey == "" && topicTabReusableLocked(tab) && tabMatchesTopicTarget(tab, scope, workspaceRoot, topicID) {
			if activate {
				a.activeTabID = tab.ID
			}
			meta := a.tabMeta(tab, tab.ID == a.activeTabID)
			a.saveTabsLocked()
			a.mu.Unlock()
			// This branch only admits an empty target key, so the matched topic
			// already identifies the session. No continuation rebind is needed.
			return enrichTabMeta(meta), nil
		}
	}
	source := a.liveRuntimeTabMatchingLocked(nil, sessionPath)
	if source == nil && targetKey == "" {
		source = a.liveRuntimeTabMatchingTopicLocked(nil, scope, workspaceRoot, topicID)
	}
	if source != nil && a.tabs[source.ID] == source {
		source = nil
	}

	tab := a.newTopicTabLocked(scope, workspaceRoot, actualRoot, topicID, sessionPath, canonical)
	tabID := tab.ID

	a.tabs[tabID] = tab
	a.tabOrder = append(a.tabOrder, tabID)
	if activate {
		a.activeTabID = tabID
	}
	a.saveTabsLocked()
	meta := a.tabMeta(tab, tab.ID == a.activeTabID)
	a.mu.Unlock()

	if source != nil {
		if a.attachExistingSessionRuntime(tab, runtimeAttachIdentity(source, sessionPath), a.ctx) {
			a.mu.RLock()
			meta = a.tabMeta(tab, tab.ID == a.activeTabID)
			a.mu.RUnlock()
			if scope == "project" {
				a.emitProjectTreeRuntimeChanged()
			}
			return enrichTabMeta(meta), nil
		}
	}
	a.startTabControllerBuild(tab)
	if scope == "project" {
		a.emitProjectTreeRuntimeChanged()
	}
	return enrichTabMeta(meta), nil
}

// OpenGlobalTab opens a new global-scope tab (no project root). The global
// workspace root is the deepzora user config directory.
func (a *App) openGlobalTab(topicID string) (TabMeta, error) {
	globalRoot := globalWorkspaceRoot()
	if err := os.MkdirAll(globalRoot, 0o755); err != nil {
		return TabMeta{}, fmt.Errorf("create global workspace: %w", err)
	}

	sessionPath, err := a.resolveTopicOpenPath(topicID)
	if err != nil {
		return TabMeta{}, err
	}
	return a.openTopicTabWithActivation("global", "", topicID, sessionPath, true)
}

// OpenTopicSession opens a concrete saved session from the sidebar. Unlike
// OpenProjectTab/OpenGlobalTab, it does not resolve the topic to the latest
// session first; sessionPath is the runtime identity being selected.
func (a *App) openTopicSession(scope, workspaceRoot, topicID, sessionPath string) (TabMeta, error) {
	return a.openTopicSessionWithNavigation(scope, workspaceRoot, topicID, sessionPath, a.desktopSessions.navigationSeq.Add(1))
}

func (a *App) openTopicSessionWithNavigation(scope, workspaceRoot, topicID, route string, navigation uint64) (TabMeta, error) {
	if a.desktopSessions.navigationSeq.Load() != navigation {
		return TabMeta{}, errSessionNavigationSuperseded
	}
	if _, ok := parseSessionRoute(route); !ok {
		return TabMeta{}, newSessionOperationError("unsupported", "Use a current session identity.")
	}
	return a.openTopicTabWithActivation(scope, workspaceRoot, topicID, route, true, navigation)
}

// ActivateTopic opens a topic into the single visible conversation surface used
// by layouts without a tab strip. It delegates the actual open/reuse behavior to
// the classic tab path, then prunes every non-active visible tab so historical
// clicks do not accumulate hidden startup work.
//
// Interop with StartTopicActivation: a legacy ActivateTopic call supersedes any
// pending ticketed activation (its background completion becomes a no-op and a
// "cancelled" event is emitted for the old requestId), and ticketed
// activations supersede each other the same way. The synchronous return
// contract — TabMeta after the prune — is unchanged.
func (a *App) activateTopic(scope, workspaceRoot, topicID, sessionPath string) (TabMeta, error) {
	navigation := a.desktopSessions.navigationSeq.Add(1)
	a.singleSurfaceMu.Lock()
	defer a.singleSurfaceMu.Unlock()
	return a.activateTopicLocked(scope, workspaceRoot, topicID, sessionPath, navigation)
}

func (a *App) activateTopicLocked(scope, workspaceRoot, topicID, sessionPath string, navigation uint64) (TabMeta, error) {
	if a.desktopSessions.navigationSeq.Load() != navigation {
		return TabMeta{}, errSessionNavigationSuperseded
	}

	var meta TabMeta
	var err error
	if strings.TrimSpace(sessionPath) != "" {
		meta, err = a.openTopicSessionWithNavigation(scope, workspaceRoot, topicID, sessionPath, navigation)
	} else if strings.TrimSpace(scope) == "project" {
		meta, err = a.openProjectTab(workspaceRoot, topicID)
	} else {
		meta, err = a.openGlobalTab(topicID)
	}
	if err != nil {
		return TabMeta{}, err
	}
	// A legacy activation supersedes any pending ticketed activation: its
	// completion must not prune or publish after this call's own prune.
	if reqID, tabID := a.supersedePendingTopicActivation(meta.ID); reqID != "" {
		a.emitTopicActivation(TopicActivationEvent{RequestID: reqID, TabID: tabID, Phase: topicActivationPhaseCancelled})
	}
	return a.keepOnlyVisibleTab(meta.ID)
}

// EnsureBlankSurface mirrors EnsureBlankTab for no-tab-strip layouts: after
// creating or reusing a blank session, it removes other visible tabs while
// preserving running runtimes as detached background sessions.
func (a *App) EnsureBlankSurface(scope, workspaceRoot string) (TabMeta, error) {
	return a.ensureBlankSurface(scope, workspaceRoot)
}

func (a *App) ensureBlankSurface(scope, workspaceRoot string) (TabMeta, error) {
	navigation := a.desktopSessions.navigationSeq.Add(1)
	a.singleSurfaceMu.Lock()
	defer a.singleSurfaceMu.Unlock()
	if a.desktopSessions.navigationSeq.Load() != navigation {
		return TabMeta{}, errSessionNavigationSuperseded
	}

	meta, err := a.ensureBlankTab(scope, workspaceRoot)
	if err != nil {
		return TabMeta{}, err
	}
	// Same interop rule as ActivateTopic: this synchronous surface switch
	// supersedes any pending ticketed activation.
	if reqID, tabID := a.supersedePendingTopicActivation(meta.ID); reqID != "" {
		a.emitTopicActivation(TopicActivationEvent{RequestID: reqID, TabID: tabID, Phase: topicActivationPhaseCancelled})
	}
	return a.keepOnlyVisibleTab(meta.ID)
}

func tabMatchesTopicTarget(tab *WorkspaceTab, scope, workspaceRoot, topicID string) bool {
	if tab == nil || tab.Scope != scope || tab.TopicID != topicID {
		return false
	}
	if scope == "global" {
		return true
	}
	return sameProjectRoot(tab.WorkspaceRoot, workspaceRoot)
}

func tabInWorkspace(tab *WorkspaceTab, workspaceRoot string) bool {
	return tab != nil &&
		tab.Scope == "project" &&
		sameProjectRoot(tab.WorkspaceRoot, workspaceRoot)
}

// EnsureBlankTab activates the existing blank tab for the target scope, or
// creates one if none exists. Reusing a blank tab keeps repeated "new session"
// clicks from piling up empty conversations.
func (a *App) ensureBlankTab(scope, workspaceRoot string) (TabMeta, error) {
	scope = strings.TrimSpace(scope)
	if scope != "project" {
		scope = "global"
	}

	globalRoot := ""
	if scope == "project" {
		workspaceRoot = strings.TrimSpace(workspaceRoot)
		if workspaceRoot == "" {
			return TabMeta{}, fmt.Errorf("workspaceRoot is required")
		}
		if abs, err := filepath.Abs(workspaceRoot); err == nil {
			workspaceRoot = abs
		}
	} else {
		workspaceRoot = ""
		globalRoot = globalWorkspaceRoot()
		if err := os.MkdirAll(globalRoot, 0o755); err != nil {
			return TabMeta{}, fmt.Errorf("create global workspace: %w", err)
		}
	}

	var created *WorkspaceTab
	// Compute actual root early — both the indexed-topic fallback and the
	// new-topic path need it when constructing the tab below.
	actualRoot := workspaceRoot
	if scope == "global" {
		actualRoot = globalRoot
	}
	releaseAdmission, err := a.beginProjectRuntimeAdmission(scope, actualRoot)
	if err != nil {
		return TabMeta{}, err
	}
	defer releaseAdmission()
	if scope == "project" {
		saveWorkspace(workspaceRoot)
		if err := a.registerProjectRoot(workspaceRoot); err != nil {
			return TabMeta{}, err
		}
	}
	defaultModel, defaultToolApprovalMode := desktopNewSessionDefaults(scope, actualRoot)

	a.mu.Lock()
	var reusable *WorkspaceTab
	for _, id := range a.orderedTabIDsLocked() {
		tab := a.tabs[id]
		if a.blankTabMatchesTargetLocked(tab, scope, workspaceRoot) {
			if err := resetReusableBlankTabTitle(tab, scope, workspaceRoot); err != nil {
				a.mu.Unlock()
				return TabMeta{}, err
			}
			reusable = tab
			break
		}
	}
	if reusable != nil {
		a.mu.Unlock()
		if err := a.alignReusableBlankTabModel(reusable, defaultModel); err != nil {
			return TabMeta{}, err
		}
		a.mu.Lock()
		if reusable.removed || a.tabs[reusable.ID] != reusable {
			a.mu.Unlock()
			return TabMeta{}, fmt.Errorf("blank session changed while applying the default model; retry")
		}
		a.activeTabID = reusable.ID
		meta := a.tabMeta(reusable, true)
		a.saveTabsLocked()
		a.mu.Unlock()
		return enrichTabMeta(meta), nil
	}

	// New blank sessions start from global defaults for model and approval
	// posture, keeping execution-local settings (effort/floor/MCP) from the
	// active tab without letting it override global defaults (#4019).
	inheritedModel := defaultModel
	var inheritedEffort *string
	inheritedMode := tabModeForPlan(false)
	inheritedToolApprovalMode := defaultToolApprovalMode
	inheritedDisabledMCP := map[string]ServerView{}
	var inheritedMCPOrder []string
	if active := a.activeTabLocked(); active != nil {
		inheritedEffort = cloneStringPtr(active.effort)
		inheritedDisabledMCP = cloneServerViewMap(active.disabledMCP)
		inheritedMCPOrder = append([]string(nil), active.mcpOrder...)
	}

	topicID, canonical, err := a.indexedBlankTopicIDLocked(scope, workspaceRoot)
	if err != nil {
		a.mu.Unlock()
		return TabMeta{}, err
	}
	if topicID != "" {
		// Reuse a previously-indexed but unused blank topic instead of
		// creating a new one.  Build it inline (not via OpenProjectTab /
		// OpenGlobalTab) so it inherits settings from the active tab.
		tabID := a.newUniqueTabIDLocked()
		topicTitle := canonical.title
		created = &WorkspaceTab{
			ID:               tabID,
			Scope:            scope,
			WorkspaceRoot:    actualRoot,
			TopicID:          topicID,
			TopicTitle:       topicTitle,
			topicTitleSource: canonical.titleSource,
			model:            inheritedModel,
			effort:           inheritedEffort,
			mode:             inheritedMode,
			toolApprovalMode: inheritedToolApprovalMode,
			disabledMCP:      inheritedDisabledMCP,
			mcpOrder:         inheritedMCPOrder,
		}
		canonical.applyLocked(created)
		created.sink = &tabEventSink{tabID: tabID, app: a}
		a.tabs[tabID] = created
		a.tabOrder = append(a.tabOrder, tabID)
		a.activeTabID = tabID
		a.saveTabsLocked()
		a.mu.Unlock()

		// A new-session command returns an executable immutable identity. Build
		// and publish it before returning instead of exposing a pathless tab whose
		// eventual asynchronous startup could race a second create/delete action.
		return a.startCreatedSessionTab(created, actualRoot)
	}

	a.mu.Unlock()
	topic, err := a.createColdSessionTopic(scope, workspaceRoot, "")
	if err != nil {
		return TabMeta{}, err
	}
	path, err := a.resolveTopicOpenPath(topic.ID)
	if err != nil {
		return TabMeta{}, err
	}
	_, binding, err := a.canonicalTopicOpen(path)
	if err != nil {
		return TabMeta{}, err
	}
	if binding == nil {
		return TabMeta{}, fmt.Errorf("new session identity is unavailable")
	}
	topicID = topic.ID
	a.mu.Lock()

	tabID := a.newUniqueTabIDLocked()
	created = &WorkspaceTab{
		ID:               tabID,
		Scope:            scope,
		WorkspaceRoot:    actualRoot,
		TopicID:          topicID,
		TopicTitle:       defaultTopicTitle,
		topicTitleSource: topicTitleSourceAuto,
		model:            inheritedModel,
		effort:           inheritedEffort,
		mode:             inheritedMode,
		toolApprovalMode: inheritedToolApprovalMode,
		disabledMCP:      inheritedDisabledMCP,
		mcpOrder:         inheritedMCPOrder,
	}
	binding.applyLocked(created)
	created.sink = &tabEventSink{tabID: tabID, app: a}
	a.tabs[tabID] = created
	a.tabOrder = append(a.tabOrder, tabID)
	a.activeTabID = tabID
	a.saveTabsLocked()
	a.mu.Unlock()

	return a.startCreatedSessionTab(created, actualRoot)
}

func (a *App) startCreatedSessionTab(created *WorkspaceTab, actualRoot string) (TabMeta, error) {
	a.buildTabController(created)
	a.mu.RLock()
	meta := a.tabMeta(created, true)
	startupErr := created.StartupErr
	ready := created.Ctrl != nil && created.SessionID != ""
	a.mu.RUnlock()
	if !ready {
		return TabMeta{}, fmt.Errorf("create session runtime: %s", startupErr)
	}
	a.emitProjectTreeChangedEvent()
	return enrichTabMeta(meta), nil
}

// blankTabMatchesTargetLocked returns true if tab is a reusable blank tab
// matching the given scope/project root — no running controller, no real history.
func (a *App) blankTabMatchesTargetLocked(tab *WorkspaceTab, scope, workspaceRoot string) bool {
	if tab == nil || tab.Scope != scope {
		return false
	}
	if scope == "project" && !sameProjectRoot(tab.WorkspaceRoot, workspaceRoot) {
		return false
	}
	if tab.Ctrl == nil {
		// A cold durable identity is not a reusable draft. Its history must be
		// read through the session service after binding, never via a file probe.
		return tab.SessionID == ""
	}
	if tab.hasActiveRuntimeWork() {
		return false
	}
	return !messagesHaveConversationContent(tab.Ctrl.History())
}

func resetReusableBlankTabTitle(tab *WorkspaceTab, scope, workspaceRoot string) error {
	if tab == nil {
		return nil
	}
	topicID := strings.TrimSpace(tab.TopicID)
	if topicID == "" {
		return nil
	}
	if tab.topicTitleSource != topicTitleSourceAuto {
		return nil
	}
	tab.TopicTitle = defaultTopicTitle
	tab.topicTitleSource = topicTitleSourceAuto
	return nil
}

// indexedBlankTopicIDLocked returns an unopened, unnamed current session. A
// named session, pending creation, archived session or live runtime is never
// consumed by an automatic new-session command.
func (a *App) indexedBlankTopicIDLocked(scope, workspaceRoot string) (string, *canonicalTabBinding, error) {
	state, err := a.workspaceRegistry().Load(a.bootContext())
	if err != nil {
		return "", nil, err
	}
	workspaceID, ok, err := workspacestate.ResolveWorkspaceID(state, desktopWorkspaceRoot(scope, workspaceRoot))
	if err != nil || !ok {
		return "", nil, err
	}
	openTopics := map[string]bool{}
	openSessions := map[string]bool{}
	for _, tab := range a.tabs {
		if tab != nil && !tab.removed {
			openTopics[tab.TopicID] = true
			openSessions[tab.SessionID] = true
		}
	}
	service := a.desktopSessionService("")
	for _, id := range state.Workspaces[workspaceID].SessionIDs {
		p := state.Presentation[id]
		if state.SessionStates[id].Lifecycle != workspacestate.Active || p.TopicID == "" || p.Title != "" || openTopics[p.TopicID] || openSessions[id] {
			continue
		}
		if _, pending := state.PendingCreates[id]; pending {
			continue
		}
		ref := session.SessionRef{HostID: localDesktopHostID, SessionID: id}
		if _, live := service.Runtime(ref); live {
			continue
		}
		info, err := service.Query().Stat(a.bootContext(), ref)
		if err != nil {
			return "", nil, err
		}
		if info.EventSequence != 0 || info.TitleSequence != 0 || info.Turns != 0 {
			continue
		}
		return p.TopicID, &canonicalTabBinding{ref: ref, workspaceID: workspaceID, title: defaultTopicTitle, titleSource: topicTitleSourceAuto}, nil
	}
	return "", nil, nil
}

// ReorderTabs persists the full local+remote strip while keeping each
// registry's internal order independent.
func (a *App) ReorderTabs(tabIDs []string) error {
	a.remoteTabMu.Lock()
	remoteCount := len(a.remoteTabs)
	a.remoteTabMu.Unlock()
	a.mu.Lock()
	if len(tabIDs) != len(a.tabs)+remoteCount {
		a.mu.Unlock()
		return fmt.Errorf("tab order length mismatch")
	}
	seen := make(map[string]bool, len(tabIDs))
	next := make([]string, 0, len(a.tabs))
	nextRemote := make([]string, 0, remoteCount)
	for _, id := range tabIDs {
		if seen[id] {
			a.mu.Unlock()
			return fmt.Errorf("duplicate tab %q", id)
		}
		seen[id] = true
		if _, ok := a.tabs[id]; ok {
			next = append(next, id)
		} else {
			nextRemote = append(nextRemote, id)
		}
	}
	if len(next) != len(a.tabs) {
		a.mu.Unlock()
		return fmt.Errorf("tab order is missing local tabs")
	}
	a.remoteTabMu.Lock()
	remoteOK := len(nextRemote) == len(a.remoteTabs)
	if remoteOK {
		for _, id := range nextRemote {
			if a.remoteTabs[id] == nil {
				remoteOK = false
				break
			}
		}
	}
	if !remoteOK {
		a.remoteTabMu.Unlock()
		a.mu.Unlock()
		return fmt.Errorf("tab order is missing remote tabs")
	}
	a.remoteTabLayout.order = append([]string(nil), nextRemote...)
	a.remoteTabLayout.stripOrder = append([]string(nil), tabIDs...)
	a.remoteTabMu.Unlock()
	a.tabOrder = next
	dir, entries, activeID, version := a.saveTabsCollectLocked()
	a.mu.Unlock()
	a.saveTabsWrite(dir, entries, activeID, version)
	return nil
}

// CloseTab removes a visible tab. If the tab's session still has foreground or
// background work, the controller is detached so closing a view does not destroy
// the session runtime.
func (a *App) CloseTab(tabID string) error {
	return a.closeTab(tabID, true)
}

func (a *App) closeTabRuntime(tabID string, allowDetach bool) error {
	defer a.lockRuntimeMutation("close-tab")()
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	// The runtime mutation barrier is acquired before sessionRemovalMu. This waits
	// for a turn whose admission is already in progress, blocks later turns/builds,
	// and leaves the tab visible until an earlier MCP Host-wide gate completes.

	a.mu.Lock()
	tab, ok := a.tabs[tabID]
	if !ok {
		a.mu.Unlock()
		return fmt.Errorf("tab %q not found", tabID)
	}
	a.mu.Unlock()

	// Snapshot while the tab binding is still present, but outside a.mu because
	// snapshot recovery can re-enter App and acquire a.mu. sessionRemovalMu keeps
	// DeleteSession/topic/workspace removal from trashing the same files while
	// this save is in flight.
	if err := a.snapshotTab(tab); err != nil {
		slog.Warn("desktop: snapshot before closing tab failed", "tab", tabID, "err", err)
		return fmt.Errorf("save current session before closing tab: %w", err)
	}
	// A terminal belongs to the visible chat tab, even when another tab points
	// at the same project. Reap its PTY before removing the tab binding.
	if a.terminals != nil {
		a.terminals.closeForTab(tabID)
	}

	// Claim the mirror's farewell while this tab still owns its writer; a close
	// that returns early keeps the writer and hands the claim back.
	closingMirror, releaseMirrorClaim := a.claimTakeoverMirrorFarewell(a.currentSessionPathFor(tab))
	defer releaseMirrorClaim()

	a.mu.Lock()
	if current := a.tabs[tabID]; current != tab {
		a.mu.Unlock()
		if current == nil {
			return fmt.Errorf("tab %q not found", tabID)
		}
		return fmt.Errorf("tab %q changed while closing", tabID)
	}
	if !allowDetach && tab.hasActiveRuntimeWork() {
		a.mu.Unlock()
		return fmt.Errorf("task still has active work")
	}
	if tab.Ctrl == nil || !tab.hasActiveRuntimeWork() {
		a.markTabRemovedLocked(tab)
	}

	ordered := a.orderedTabIDsLocked()
	closedIndex := -1
	for i, id := range ordered {
		if id == tabID {
			closedIndex = i
			break
		}
	}
	delete(a.tabs, tabID)
	a.removeTabOrderLocked(tabID)
	wasActive := a.activeTabID == tabID
	if wasActive {
		a.activeTabID = ""
		if len(a.tabOrder) > 0 {
			nextIndex := max(closedIndex, 0)
			if nextIndex >= len(a.tabOrder) {
				nextIndex = len(a.tabOrder) - 1
			}
			a.activeTabID = a.tabOrder[nextIndex]
		}
	}
	a.saveTabsLocked()
	// Snapshot the teardown targets while still holding the lock: the tab is
	// no longer reachable from a.tabs after this section, but locked writers
	// holding stale pointers (rememberTabSessionPath)
	// can still write its fields under a.mu.
	closeCtrl := tab.Ctrl
	closeSink := tab.sink
	a.mu.Unlock()
	if a.workspaceHub != nil {
		a.workspaceHub.reconcileRoots()
	}

	// Tear down outside App.mu while retaining the lifecycle barrier acquired
	// before the tab binding was removed.
	if closeCtrl != nil {
		if allowDetach && controllerHasActiveRuntimeWork(closeCtrl) && a.detachSessionRuntime(tab) {
			// Detached runtimes retain their current writer and keep saving.
			return nil
		}
		a.quiesceTabAutosave(tab) // wait for any in-flight snapshot to finish
		closeCtrl.Cancel()
		closeCtrl.Close()
		// Release the shared plugin host reference. The host stays alive as
		// long as any other tab for the same workspace root holds a reference;
		// on the last release the host is closed and its subprocesses exit.
		a.releaseTabSharedHost(tab)
	}
	// The writer is released: tell Serve now so it hands the session straight
	// back instead of waiting for the writer to drop.
	a.endTakeoverMirrorForClosedTab(closingMirror)
	if closeSink != nil {
		closeSink.clearContext() // stop further emissions (nil ctx -> Emit becomes no-op)
	}
	return nil
}

func (a *App) applySingleSurfaceTabPolicy() error {
	a.singleSurfaceMu.Lock()
	defer a.singleSurfaceMu.Unlock()

	a.mu.RLock()
	tabID := a.activeTabID
	if tabID == "" || a.tabs[tabID] == nil {
		for _, id := range a.tabOrder {
			if a.tabs[id] != nil {
				tabID = id
				break
			}
		}
		if tabID == "" {
			for id := range a.tabs {
				tabID = id
				break
			}
		}
	}
	a.mu.RUnlock()
	if tabID == "" {
		return nil
	}
	_, err := a.keepOnlyVisibleTab(tabID)
	return err
}

func (a *App) removeVisibleTabRuntimeAdmissionHeld(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	a.mu.RLock()
	ctrl := tab.Ctrl
	a.mu.RUnlock()
	if ctrl != nil && controllerHasActiveRuntimeWork(ctrl) && a.detachSessionRuntime(tab) {
		return
	}
	if err := a.snapshotTab(tab); err != nil {
		slog.Warn("desktop: snapshot before removing visible tab runtime failed", "tab", tab.ID, "err", err)
	}
	a.markTabRemoved(tab)
	a.closeTabRuntimeAdmissionHeld(tab)
}

func (a *App) markTabRemoved(tab *WorkspaceTab) {
	a.mu.Lock()
	a.markTabRemovedLocked(tab)
	a.mu.Unlock()
}

func (a *App) markTabRemovedLocked(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	tab.removed = true
	if tab.buildCancel != nil {
		tab.buildCancel()
		tab.buildCancel = nil
	}
}

// tabBuildSupersededLocked reports whether an in-flight build lost ownership
// of its tab: the tab was removed/replaced, or a session rebind bumped
// buildGeneration to invalidate it. Generation 0 marks the synchronous
// rebuild paths, which serialize through runtimeRebuildMu instead and are
// never superseded by generation bumps. Callers must hold a.mu.
func (a *App) tabBuildSupersededLocked(tab *WorkspaceTab, generation uint64) bool {
	if tab == nil || tab.removed || a.shuttingDown.Load() || a.tabs[tab.ID] != tab {
		return true
	}
	return generation != 0 && tab.buildGeneration != generation
}

func (a *App) tabBuildSuperseded(tab *WorkspaceTab, generation uint64) bool {
	if tab == nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tabBuildSupersededLocked(tab, generation)
}

// supersedeTabBuildLocked invalidates any in-flight startup build and cancels
// its context. A synchronous rebuild (model/effort/token switch) that has
// already installed its controller calls this so a slower blank-session build
// cannot finish afterward, overwrite tab.Ctrl, and release or steal the
// runtime the switch just bound. Callers must hold a.mu.
func (a *App) supersedeTabBuildLocked(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	tab.buildGeneration++
	if tab.buildCancel != nil {
		tab.buildCancel()
		tab.buildCancel = nil
	}
}

// abandonSupersededBuild releases only the controller and shared-host
// reference acquired by that build; published replacement ownership is untouched.
func (a *App) abandonSupersededBuild(ctrl control.SessionAPI, rootKey string) {
	if ctrl != nil {
		ctrl.Close()
	}
	if rootKey != "" {
		a.releaseSharedHost(rootKey)
	}
}

func (a *App) clearTabBuildCancel(tab *WorkspaceTab, generation uint64, cancel context.CancelFunc, keepContext bool) {
	if cancel == nil {
		return
	}
	if !keepContext {
		defer cancel()
	}
	if tab == nil {
		return
	}
	a.mu.Lock()
	if tab.buildGeneration == generation {
		tab.buildCancel = nil
	}
	a.mu.Unlock()
}

func (a *App) closeTabRuntimeAdmissionHeld(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	a.mu.RLock()
	ctrl := tab.Ctrl
	sink := tab.sink
	a.mu.RUnlock()
	if ctrl != nil {
		a.quiesceTabAutosave(tab)
		ctrl.Cancel()
		ctrl.Close()
		a.releaseTabSharedHost(tab)
	}
	if sink != nil {
		sink.clearContext()
	}
	a.mu.Lock()
	a.releaseSessionRuntimeLocked(tab)
	a.mu.Unlock()
}

// buildTabController assembles a controller for a tab in the background, the
// same way buildController works for the single-controller App. On success it
// wires the controller and flips Ready; on failure it stores StartupErr.
func (a *App) startTabControllerBuild(tab *WorkspaceTab) {
	a.startTabControllerBuildMode(tab, false)
}

func (a *App) startTabControllerBuildMode(tab *WorkspaceTab, async bool) {
	buildCtx, cancel := context.WithCancel(a.bootContext())
	a.mu.Lock()
	// Historical shells are not ordinary dormant tabs. Only explicit
	// preparation may replace their source identity before a runtime starts.
	if tab == nil || tab.removed || a.shuttingDown.Load() {
		a.mu.Unlock()
		cancel()
		return
	}
	tab.buildGeneration++
	generation := tab.buildGeneration
	tab.buildCancel = cancel
	if tab.buildDone != nil {
		// Defensive: the owning build's terminal defer nils buildDone after
		// closing it, so a non-nil channel here means that build never ran its
		// defer. Close it anyway so activation completions never wait forever.
		close(tab.buildDone)
	}
	tab.buildDone = make(chan struct{})
	tab.buildDoneGen = generation
	execution := &tabBuildExecution{done: make(chan struct{}), cancel: cancel, generation: generation}
	tab.buildExecution = execution
	if tab.buildExecutions == nil {
		tab.buildExecutions = make(map[*tabBuildExecution]struct{})
	}
	tab.buildExecutions[execution] = struct{}{}
	a.mu.Unlock()
	run := func() {
		defer a.finishTabBuildExecution(tab, execution)
		a.buildTabControllerWithContext(tab, loadedTabSession{}, buildCtx, generation, cancel)
	}
	if a.ctx == nil && !async {
		run()
		return
	}
	go run()
}

func (a *App) buildTabController(tab *WorkspaceTab) {
	a.buildTabControllerWithLoadedSession(tab, loadedTabSession{})
}

type loadedTabSession struct {
	Path    string
	Session *agent.Session
}

func (a *App) buildTabControllerWithLoadedSession(tab *WorkspaceTab, loadedSession loadedTabSession) {
	a.buildTabControllerWithContext(tab, loadedSession, a.bootContext(), 0, nil)
}

func (a *App) desktopNotificationSender() notify.Sender {
	if a == nil {
		return notify.NewPlatformSender()
	}
	a.notificationSenderOnce.Do(func() {
		if a.notificationSender == nil {
			a.notificationSender = notify.NewPlatformSender()
		}
	})
	return a.notificationSender
}

func (a *App) desktopControllerSink(inner event.Sink, cfg config.NotificationsConfig) event.Sink {
	if !cfg.Enabled {
		return inner
	}
	sender := a.desktopNotificationSender()
	if sender == nil {
		return inner
	}
	return notify.NewSink(inner, sender, cfg)
}

// closeTabBuildDone signals waiters (topic-activation completions) that the
// build owning buildGeneration has terminated. Every build funnels through
// buildTabControllerWithContextCore, whose deferred call guarantees
// the channel startTabControllerBuild created is closed exactly once, on every
// terminal path — success, failure, and superseded abandon alike. Synchronous
// rebuild paths pass generation 0 and never created a channel.
func (a *App) closeTabBuildDone(tab *WorkspaceTab, buildGeneration uint64) {
	if tab == nil || buildGeneration == 0 {
		return
	}
	a.mu.Lock()
	if tab.buildDoneGen == buildGeneration && tab.buildDone != nil {
		close(tab.buildDone)
		tab.buildDone = nil
	}
	a.mu.Unlock()
}

func (a *App) buildTabControllerWithContext(tab *WorkspaceTab, loadedSession loadedTabSession, buildCtx context.Context, buildGeneration uint64, buildCancel context.CancelFunc) {
	a.buildTabControllerWithContextCore(tab, loadedSession, buildCtx, buildGeneration, buildCancel)
}

// buildTabControllerWithContextCore performs configuration, session routing,
// and extension boot outside runtimeAdmissionMu. Only publication enters the
// lifecycle barrier.
func (a *App) buildTabControllerWithContextCore(tab *WorkspaceTab, loadedSession loadedTabSession, buildCtx context.Context, buildGeneration uint64, buildCancel context.CancelFunc) {
	defer a.recoverToPending("buildTabController")
	keepBuildContext := false
	defer func() {
		a.clearTabBuildCancel(tab, buildGeneration, buildCancel, keepBuildContext)
	}()
	defer a.closeTabBuildDone(tab, buildGeneration)
	if hook := a.tabBuildStartHook; hook != nil && tab != nil {
		// Test-only gate: lets activation-ordering tests hold builds in flight
		// and release them out of order. Runs even for already-superseded
		// builds so the test can observe every build it started.
		hook(tab.ID)
	}
	appCtx := a.ctx
	a.reportManualBuildStage(tab, buildGeneration, "building_runtime")
	if a.tabBuildSuperseded(tab, buildGeneration) {
		return
	}
	if !a.prepareTabControllerWorkspace(tab, buildCtx, buildGeneration, appCtx) {
		return
	}

	// Snapshot the identity/profile fields under a.mu before the off-lock
	// stretch: session rebinding, recovery, and topic assignment write them
	// under the lock while this goroutine builds.
	a.mu.RLock()
	tabWorkspaceRoot := tab.WorkspaceRoot
	tabScope := tab.Scope
	tabTopicID := tab.TopicID
	tabSeedTitle := canonicalSeedTitle(tab.TopicTitle, tab.topicTitleSource)
	tabSessionID := tab.SessionID
	tabModel := tab.model
	tabSink := tab.sink
	tabCreateOperationID := tab.PendingCreateOperationID
	a.mu.RUnlock()

	root := tabWorkspaceRoot
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}

	// Load config for this tab's workspace root.
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		a.recordTabStartupFailure(tab, buildGeneration, appCtx, err)
		return
	}

	if a.tabBuildSuperseded(tab, buildGeneration) {
		return
	}
	if tabSink != nil {
		tabSink.setContext(appCtx)
	}

	sessionDir := currentStartupSession(tab, root, tabScope, tabSessionID)
	buildSessionService := a.desktopSessionService(sessionDir)
	model := strings.TrimSpace(tabModel)
	// The v3 event projection owns model selection. desktop-tabs.json only
	// remembers which immutable session to open, so stale UI state cannot select
	// a different provider when the process restarts.
	if strings.TrimSpace(tabSessionID) != "" && strings.TrimSpace(tabCreateOperationID) == "" {
		service := buildSessionService
		ref := session.SessionRef{HostID: service.HostID(), SessionID: strings.TrimSpace(tabSessionID)}
		if view, openErr := service.OpenSession(buildCtx, ref); openErr == nil && strings.TrimSpace(view.Recent.ModelRef) != "" {
			model = strings.TrimSpace(view.Recent.ModelRef)
		}

	}
	if model == "" {
		if def := strings.TrimSpace(cfg.DefaultModel); providerext.PluginRefOwner(def) != "" {
			// A plugin-namespaced default_model belongs to an extension
			// sidecar: the config catalog can never resolve it, but boot's
			// merged resolver can. Pass it through untouched.
			model = def
		} else {
			resolved, _, ok := cfg.ResolveDesktopNewSessionModel()
			if !ok {
				a.recordTabStartupFailure(tab, buildGeneration, appCtx, errNoDesktopChatModel)
				return
			}
			model = resolved
		}
	}
	requestedModel := model
	if strings.TrimSpace(tabCreateOperationID) != "" {
		resolved, resolveErr := resolveDraftCreateModelStrict(cfg, model)
		if resolveErr != nil {
			a.recordTabStartupFailure(tab, buildGeneration, appCtx, resolveErr)
			return
		}
		model = resolved
	} else if providerext.PluginRefOwner(model) == "" {
		// Plugin refs skip the config fallback: rerouting an unavailable
		// extension model onto a config provider would silently change the
		// session; boot's unknown-model error is the honest failure.
		if resolved, fallback, ok := cfg.ResolveModelWithFallback(model); ok {
			if fallback && strings.TrimSpace(tabModel) != "" {
				a.noticeForTab(tab.ID, fmt.Sprintf("model %q is no longer available; switched to %s", requestedModel, resolved))
			}
			model = resolved
		}
	}

	// Acquire a shared plugin host for this workspace root so MCP processes
	// are launched once per root, not once per tab. SharedHostKey is an a.mu-
	// guarded field (takeTabSharedHostKey reads it under the lock during
	// teardown), so publish it under the lock alongside the model. Capture the
	// tab-local runtime profile here too: bound methods (SetModeForTab,
	// SetGoalForTab, SetEffortForTab, ...) write these under a.mu, so the
	// off-lock boot.Build below must read a locked snapshot, not the live tab.
	rootKey := tabWorkspaceRoot
	if rootKey == "" {
		rootKey = "__global__" // stable key for global workspace tabs
	}
	a.mu.Lock()
	if a.tabBuildSupersededLocked(tab, buildGeneration) {
		a.mu.Unlock()
		return
	}
	tab.rebindEffortModel(cfg, model)
	tab.Label = model
	tab.SharedHostKey = rootKey
	buildEffort := cloneStringPtr(tab.effort)
	buildMode := tab.mode
	buildToolApprovalMode := tab.toolApprovalMode
	buildGoal := tab.goal
	buildSink := tab.sink
	a.saveTabsLocked()
	a.mu.Unlock()
	buildRuntime := (tabRuntimeSnapshot{
		mode:             buildMode,
		goal:             buildGoal,
		toolApprovalMode: buildToolApprovalMode,
	}).normalizedRuntime()
	// Capture the extension generation before the shared host is mutated by
	// boot.Build. A concurrent plugin delete/update/reauth bumps the counter;
	// if it moves before publication we abandon this controller rather than
	// resurrecting removed tools on the shared host.
	extensionGen := a.currentExtensionGeneration()
	sharedHost := a.acquireSharedHost(rootKey)
	sink := a.desktopControllerSink(buildSink, cfg.Notifications)
	booted := a.bootTabControllerWithModelFallback(buildCtx, tab, cfg, sharedHost, boot.Options{
		Model:                model,
		RequireKey:           false,
		StatsSource:          "desktop",
		TaskStore:            a.taskStore(),
		OnConfigLoadWarnings: a.configLoadWarningsHandler(),
		Sink:                 sink,
		WorkspaceRoot:        root,
		SessionDir:           sessionDir,
		SessionService:       buildSessionService,
		EffortOverride:       cloneStringPtr(buildEffort),
		SharedHost:           sharedHost, BrowserExecutor: a.browserExecutorForRuntime(tab.ID, buildSink),
		PinnedContextLoader:   pinnedContextLoader(root),
		BeforeInboxDispatch:   a.beforeInboxDispatch,
		OnSessionTitleChanged: a.onSessionTitleChanged,
	}, extensionGen, buildGeneration, tabSessionID, requestedModel)
	buildCtx, registration := booted.ctx, booted.registration
	defer func() { registration.rollback() }()
	ctrl, err := booted.controller, booted.err
	model, modelFallback := booted.model, booted.fallback
	if a.handleTabControllerBootError(tab, registration, rootKey, buildGeneration, appCtx, err) {
		return
	}
	defer finishUnpublishedTabController(ctrl, buildGeneration, &keepBuildContext)
	if a.tabBuildSuperseded(tab, buildGeneration) {
		registration.rollback()
		a.abandonSupersededBuild(ctrl, rootKey)
		return
	}
	if a.currentExtensionGeneration() != extensionGen {
		registration.rollback()
		a.abandonSupersededBuild(ctrl, rootKey)
		a.scheduleDeferredStartupBuild(tab.ID)
		return
	}

	configureControllerRuntime(ctrl, nil, buildRuntime)

	restoredRuntime := buildRuntime
	identity, usesExclusiveV3 := ctrl.(control.IdentityLifecycle)
	if usesExclusiveV3 && identity.UsesExclusiveSession() {
		bound, bindErr := a.bindTabCanonicalSessionTopic(
			buildCtx, identity, cfg, tabScope, tabWorkspaceRoot, tabSessionID, model, modelFallback, tabTopicID, tabSeedTitle,
		)
		if bindErr != nil {
			a.recordTabStartupFailure(tab, buildGeneration, appCtx, friendlySessionLoadError(bindErr))
			ctrl.Close()
			a.releaseSharedHost(rootKey)
			return
		}
		a.mu.Lock()
		if a.tabBuildSupersededLocked(tab, buildGeneration) {
			a.mu.Unlock()
			a.abandonSupersededBuild(ctrl, rootKey)
			return
		}
		bound.applyLocked(tab)
		a.mu.Unlock()
		// Local Desktop restores its canonical session choice from the Desktop
		// preset store. OpenSession publishes the session default, so restore the
		// selected preset after binding the target identity.
		applyTabToolApprovalModeToController(ctrl, buildRuntime.toolApprovalMode)
		tab.replaceTelemetry(loadTelemetryFor(sessionRoute(bound.ref.SessionID)), sessionRuntimeKey(remoteSessionIDRoutePrefix+bound.ref.SessionID))
	}

	// Lifecycle admission protects only the compare-and-publish boundary. Slow
	// config, history, lease, and extension work above remains cancellable and
	// cannot prevent shutdown from acquiring the write side.
	a.reportManualBuildStage(tab, buildGeneration, "publishing_controller")
	releasePublication, extensionsCurrent := a.lockTabControllerPublication(extensionGen, tabScope, tabWorkspaceRoot)
	if !extensionsCurrent {
		registration.rollback()
		a.abandonSupersededBuild(ctrl, rootKey)
		a.scheduleDeferredStartupBuild(tab.ID)
		return
	}
	defer releasePublication()
	if a.rejectStaleStartupModelSettings(tab, ctrl, buildGeneration, appCtx, func() {
		registration.rollback()
		a.abandonSupersededBuild(ctrl, rootKey)
	}) {
		return
	}
	a.mu.Lock()
	if a.tabBuildSupersededLocked(tab, buildGeneration) {
		a.mu.Unlock()
		a.abandonSupersededBuild(ctrl, rootKey)
		return
	}
	// Commit the scope while the final tab-generation check is still guarded.
	// It only takes the plugin Host leaf lock and cannot call back into App.
	if !registration.commit() {
		a.mu.Unlock()
		a.abandonSupersededBuild(ctrl, rootKey)
		a.scheduleDeferredStartupBuild(tab.ID)
		return
	}
	tab.Ctrl = ctrl
	tab.Label = ctrl.Label()
	applyNormalizedRuntimeToTabLocked(tab, restoredRuntime)
	tab.Ready = true
	clearTabStartupError(tab)
	a.bindSessionRuntimeKeyLocked(tab, tab.currentSessionIdentity())
	a.advanceSessionRuntimeEpochLocked(tab)
	keepBuildContext = true
	a.mu.Unlock()
	a.finishStartupPublication(tab, ctrl, appCtx)
}

// active tab helpers

// activeTab returns the currently active tab (nil when there are no tabs).
// Self-locking; safe to call from any goroutine without external lock.
func (a *App) activeTab() *WorkspaceTab {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.activeTabID == "" {
		return nil
	}
	return a.tabs[a.activeTabID]
}

// activeTabLocked is like activeTab but assumes the caller already holds a.mu
// (either RLock or Lock). Use this inside critical sections that already own
// the lock to avoid double-locking a write-lock holder.
func (a *App) activeTabLocked() *WorkspaceTab {
	if a.activeTabID == "" {
		return nil
	}
	return a.tabs[a.activeTabID]
}

// activeCtrl returns the controller of the active tab, or nil.
// Self-locking; safe to call from any goroutine without external lock.
func (a *App) activeCtrl() control.SessionAPI {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.activeCtrlLocked()
}

// activeCtrlLocked is like activeCtrl but assumes the caller already holds a.mu.
func (a *App) activeCtrlLocked() control.SessionAPI {
	t := a.activeTabLocked()
	if t == nil {
		return nil
	}
	return t.Ctrl
}

func (a *App) tabByID(tabID string) *WorkspaceTab {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tabByIDLocked(tabID)
}

func (a *App) tabByIDLocked(tabID string) *WorkspaceTab {
	if strings.TrimSpace(tabID) == "" {
		return a.activeTabLocked()
	}
	return a.tabs[tabID]
}

func (a *App) ctrlByTabID(tabID string) control.SessionAPI {
	a.mu.RLock()
	defer a.mu.RUnlock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		return nil
	}
	return tab.Ctrl
}

// autosave per tab

const maxTabSnapshotFailureRetries = 2

// autosaveWarnInterval rate-limits the user-facing autosave-failure notice
// per tab; slog keeps recording every failure regardless.
const autosaveWarnInterval = 5 * time.Minute

func tabSnapshotRetryDelay(failures int) time.Duration {
	switch {
	case failures <= 1:
		return 100 * time.Millisecond
	case failures == 2:
		return 250 * time.Millisecond
	default:
		return 500 * time.Millisecond
	}
}

func (a *App) scheduleTabSnapshot(tabID string) {
	a.mu.RLock()
	tab := a.tabByEventSinkIDLocked(tabID)
	a.mu.RUnlock()
	if tab == nil {
		return
	}
	tab.saveMu.Lock()
	defer tab.saveMu.Unlock()
	if tab.closing {
		// Tab is being torn down: don't start new snapshot work that could
		// race DeleteSession and resurrect a trashed session file (#4384).
		return
	}
	if tab.saving {
		tab.saveAgain = true
		return
	}
	tab.saving = true
	if tab.saveCond == nil {
		tab.saveCond = sync.NewCond(&tab.saveMu)
	}
	tab.saveFailures = 0
	go a.tabSnapshotLoop(tab)
}

// quiesceTabAutosave marks the tab as closing and blocks until any in-flight
// tabSnapshotLoop has finished its current (and final) write. After it returns,
// no background goroutine can call Snapshot on this tab's controller again, so
// a subsequent DeleteSession cannot race a late write. The closing flag also
// rejects completion callbacks that arrive after this join.
func (a *App) quiesceTabAutosave(tab *WorkspaceTab) {
	if tab == nil {
		return
	}
	tab.saveMu.Lock()
	if tab.saveCond == nil {
		tab.saveCond = sync.NewCond(&tab.saveMu)
	}
	tab.closing = true
	for tab.saving {
		tab.saveCond.Wait()
	}
	tab.saveMu.Unlock()
}

func (a *App) tabSnapshotLoop(tab *WorkspaceTab) {
	defer a.recoverToPending("tabSnapshotLoop")
	for {
		var snapshotErr error
		a.mu.RLock()
		ctrl := tab.Ctrl
		a.mu.RUnlock()
		if ctrl != nil {
			if err := a.snapshotTab(tab); err == nil {
				a.lifecycleCheckpoint("before-autosave-publish")
				a.emitProjectTreeChangedEvent()
			} else {
				snapshotErr = err
			}
		}
		tab.saveMu.Lock()
		if tab.saveCond == nil {
			tab.saveCond = sync.NewCond(&tab.saveMu)
		}
		if snapshotErr == nil {
			tab.saveFailures = 0
		} else {
			tab.saveFailures++
		}
		if tab.closing {
			// Tab is being torn down: stop without picking up saveAgain work.
			tab.saving = false
			tab.saveCond.Broadcast()
			tab.saveMu.Unlock()
			if snapshotErr != nil {
				slog.Warn("desktop: session autosave failed during teardown", "tab", tab.ID, "err", snapshotErr)
			}
			return
		}
		if tab.saveAgain {
			tab.saveAgain = false
			tab.saveMu.Unlock()
			if snapshotErr != nil {
				slog.Warn("desktop: session autosave failed; newer snapshot queued", "tab", tab.ID, "err", snapshotErr)
			}
			continue
		}
		if snapshotErr != nil && tab.saveFailures <= maxTabSnapshotFailureRetries {
			delay := tabSnapshotRetryDelay(tab.saveFailures)
			attempt := tab.saveFailures
			tab.saveMu.Unlock()
			// Retries are routine (transient AV/indexer holds); tell the user
			// only when the whole burst gives up, not once per attempt.
			slog.Warn("desktop: session autosave failed; retrying", "tab", tab.ID, "attempt", attempt, "err", snapshotErr)
			time.Sleep(delay)
			continue
		}
		exhausted := snapshotErr
		tab.saving = false
		tab.saveCond.Broadcast()
		tab.saveMu.Unlock()
		if exhausted != nil {
			a.reportTabSnapshotError(tab, "autosave", exhausted)
		}
		return
	}
}

func topicTitleFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	text = strings.Trim(text, " \t\r\n，。！？；：、,.!?;:\"'`“”‘’()（）[]【】")
	if text == "" {
		return ""
	}
	const maxRunes = 18
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = strings.TrimRightFunc(string(runes[:maxRunes]), unicode.IsPunct) + "…"
	}
	if isDefaultTopicTitle(text) {
		return ""
	}
	return text
}

// persistence: desktop-projects.json

const desktopProjectsFile = "desktop-projects.json"
const tabsFileName = "desktop-tabs.json"
const desktopGlobalOrderToken = "__global__"

var desktopProjectsFileMu sync.Mutex

func desktopConfigDir() string {
	return config.DeepzoraHomeDir()
}

func (a *App) saveTabsLocked() {
	dir, entries, activeID, version := a.saveTabsCollectLocked()
	a.saveTabsWrite(dir, entries, activeID, version)
}

// saveTabsCollectLocked gathers the tab-snapshot data under the caller's lock
// (it calls orderedTabIDsLocked which requires a.mu). Returns the config dir,
// the serializable entries, the active tab ID, and a monotonic snapshot version.
// The write can happen outside the lock to avoid blocking the UI with disk I/O.
func (a *App) saveTabsCollectLocked() (string, []desktopTabEntry, string, uint64) {
	dir := desktopConfigDir()
	var entries []desktopTabEntry
	for _, id := range a.orderedTabIDsLocked() {
		if tab := a.tabs[id]; tab != nil {
			if a.suppressTabStartupRestoreLocked(tab) {
				continue
			}
			entries = append(entries, persistedDesktopTabEntry(tab))
		}
	}
	a.tabsSaveVersion++
	return dir, entries, persistedActiveTabID(entries, a.activeTabID), a.tabsSaveVersion
}

func (a *App) orderedTabIDsLocked() []string {
	ordered, needsRepair := a.orderedTabIDsSnapshotLocked()
	if needsRepair {
		a.tabOrder = append([]string(nil), ordered...)
	}
	return ordered
}

func (a *App) orderedTabIDsSnapshotLocked() ([]string, bool) {
	seen := make(map[string]bool, len(a.tabs))
	ordered := make([]string, 0, len(a.tabs))
	for _, id := range a.tabOrder {
		if _, ok := a.tabs[id]; ok && !seen[id] {
			ordered = append(ordered, id)
			seen[id] = true
		}
	}
	var missing []string
	for id := range a.tabs {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	ordered = append(ordered, missing...)
	return ordered, len(ordered) != len(a.tabOrder) || len(missing) > 0
}

func loadTabsFile() desktopTabsFile {
	path := filepath.Join(desktopConfigDir(), tabsFileName)
	b, err := readFileUTF8(path)
	if err != nil {
		return desktopTabsFile{}
	}
	var f desktopTabsFile
	_ = json.Unmarshal(b, &f)
	return f
}

func saveProjectsFile(f desktopProjectFile) error {
	dir := desktopConfigDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f = normalizeProjectsFile(f)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, desktopProjectsFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return fileutil.ReplaceFile(tmp, path)
}

func updateProjectsFile(mutator func(*desktopProjectFile) (bool, error)) error {
	desktopProjectsFileMu.Lock()
	defer desktopProjectsFileMu.Unlock()
	return updateProjectsFileLocked(mutator)
}

func updateProjectsFileLocked(mutator func(*desktopProjectFile) (bool, error)) error {
	release, err := acquireDesktopProjectsFileLock()
	if err != nil {
		return err
	}
	defer release()
	return updateProjectsFileCrossProcessLocked(mutator)
}

func normalizeProjectRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

func sameProjectRoot(a, b string) bool {
	return newDesktopPathMatcher().sameProjectRoot(a, b)
}

func (m desktopPathMatcher) sameProjectRoot(a, b string) bool {
	return m.same(normalizeProjectRoot(a), normalizeProjectRoot(b))
}

func projectIndexByRoot(projects []desktopProject, root string) int {
	return newDesktopPathMatcher().projectIndexByRoot(projects, root)
}

func (m desktopPathMatcher) projectIndexByRoot(projects []desktopProject, root string) int {
	root = normalizeProjectRoot(root)
	if root == "" {
		return -1
	}
	for i, project := range projects {
		if m.sameProjectRoot(project.Root, root) {
			return i
		}
	}
	return -1
}

func projectRootInList(roots []string, root string) bool {
	return newDesktopPathMatcher().projectRootInList(roots, root)
}

func (m desktopPathMatcher) projectRootInList(roots []string, root string) bool {
	root = normalizeProjectRoot(root)
	if root == "" {
		return false
	}
	for _, candidate := range roots {
		if m.sameProjectRoot(candidate, root) {
			return true
		}
	}
	return false
}

func normalizeProjectsFile(f desktopProjectFile) desktopProjectFile {
	return newDesktopPathMatcher().normalizeProjectsFile(f)
}

func (m desktopPathMatcher) normalizeProjectsFile(f desktopProjectFile) desktopProjectFile {
	out := desktopProjectFile{
		GlobalTitle: strings.TrimSpace(f.GlobalTitle),
		GlobalColor: normalizeProjectColor(f.GlobalColor),
	}
	for _, p := range f.Projects {
		root := normalizeProjectRoot(p.Root)
		if root == "" {
			continue
		}
		p.Root = root
		p.Title = strings.TrimSpace(p.Title)
		p.Color = normalizeProjectColor(p.Color)
		if i := m.projectIndexByRoot(out.Projects, root); i >= 0 {
			if out.Projects[i].Title == "" && p.Title != "" {
				out.Projects[i].Title = p.Title
			}
			if out.Projects[i].Color == "" && p.Color != "" {
				out.Projects[i].Color = p.Color
			}
			continue
		}
		out.Projects = append(out.Projects, p)
	}
	for _, root := range uniqueStrings(f.PinnedProjects) {
		root = normalizeProjectRoot(root)
		if i := m.projectIndexByRoot(out.Projects, root); i >= 0 && !m.projectRootInList(out.PinnedProjects, out.Projects[i].Root) {
			out.PinnedProjects = append(out.PinnedProjects, out.Projects[i].Root)
		}
	}
	out.SidebarOrder = m.normalizeSidebarOrder(f.SidebarOrder, out.Projects)
	return out
}

func (m desktopPathMatcher) normalizeSidebarOrder(order []string, projects []desktopProject) []string {
	seenGlobal := false
	// Dedupe roots against a roots-only list: out also holds the global order
	// token, which must never be path-compared against project roots.
	var seenRoots []string
	out := make([]string, 0, len(order))
	for _, value := range order {
		value = strings.TrimSpace(value)
		if value == desktopGlobalOrderToken {
			if !seenGlobal {
				seenGlobal = true
				out = append(out, value)
			}
			continue
		}
		root := normalizeProjectRoot(value)
		i := m.projectIndexByRoot(projects, root)
		if i < 0 {
			continue
		}
		root = projects[i].Root
		if m.projectRootInList(seenRoots, root) {
			continue
		}
		seenRoots = append(seenRoots, root)
		out = append(out, root)
	}
	return out
}

func sameProjectOrder(a, b []desktopProject) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Root != b[i].Root {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func prependUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return uniqueStrings(values)
	}
	return uniqueStrings(append([]string{value}, values...))
}

func containsDesktopString(values []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	return slices.Contains(uniqueStrings(values), value)
}

func projectTreeOrderKey(node ProjectNode) string {
	switch node.Kind {
	case "global_folder":
		return desktopGlobalOrderToken
	case "project":
		return normalizeProjectRoot(node.Root)
	default:
		return ""
	}
}

func applyProjectTreeOrder(nodes []ProjectNode, order []string) []ProjectNode {
	if len(order) == 0 {
		return nodes
	}
	byKey := make(map[string]ProjectNode, len(nodes))
	for _, node := range nodes {
		key := projectTreeOrderKey(node)
		if key != "" {
			byKey[key] = node
		}
	}
	seen := make(map[string]bool, len(nodes))
	out := make([]ProjectNode, 0, len(nodes))
	for _, value := range order {
		key := strings.TrimSpace(value)
		if key != desktopGlobalOrderToken {
			key = normalizeProjectRoot(key)
		}
		if key == "" || seen[key] {
			continue
		}
		node, ok := byKey[key]
		if !ok {
			continue
		}
		seen[key] = true
		out = append(out, node)
	}
	for _, node := range nodes {
		key := projectTreeOrderKey(node)
		if key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		out = append(out, node)
	}
	return out
}

func applyPinnedProjectOrder(nodes []ProjectNode, pinnedRoots []string) []ProjectNode {
	pinnedRoots = uniqueStrings(pinnedRoots)
	if len(pinnedRoots) == 0 {
		return nodes
	}
	byRoot := make(map[string]ProjectNode, len(nodes))
	for _, node := range nodes {
		if node.Kind == "project" && node.Root != "" {
			byRoot[normalizeProjectRoot(node.Root)] = node
		}
	}
	seen := make(map[string]bool, len(pinnedRoots))
	out := make([]ProjectNode, 0, len(nodes))
	for _, root := range pinnedRoots {
		root = normalizeProjectRoot(root)
		node, ok := byRoot[root]
		if !ok || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, node)
	}
	for _, node := range nodes {
		if node.Kind == "project" && node.Root != "" && seen[normalizeProjectRoot(node.Root)] {
			continue
		}
		out = append(out, node)
	}
	return out
}

func projectDisplayName(p desktopProject) string {
	if title := strings.TrimSpace(p.Title); title != "" {
		return title
	}
	return workspaceName(p.Root)
}

func normalizeProjectColor(color string) string {
	switch strings.TrimSpace(strings.ToLower(color)) {
	case "red", "orange", "amber", "green", "teal", "blue", "purple", "pink":
		return strings.TrimSpace(strings.ToLower(color))
	default:
		return ""
	}
}

func projectColor(root string) string {
	root = normalizeProjectRoot(root)
	if root == "" {
		return globalProjectColor()
	}
	for _, p := range loadProjectsFile().Projects {
		if sameProjectRoot(p.Root, root) {
			return normalizeProjectColor(p.Color)
		}
	}
	return ""
}

func globalProjectColor() string {
	return normalizeProjectColor(loadProjectsFile().GlobalColor)
}

func globalProjectTitle() string {
	if title := strings.TrimSpace(loadProjectsFile().GlobalTitle); title != "" {
		return title
	}
	return "Global"
}

func addProject(root, title string) error {
	root = normalizeProjectRoot(root)
	if root == "" {
		return fmt.Errorf("project root is required")
	}
	title = strings.TrimSpace(title)
	return updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		for i, p := range f.Projects {
			if sameProjectRoot(p.Root, root) {
				changed := false
				if f.Projects[i].Root != root {
					f.Projects[i].Root = root
					changed = true
				}
				if title != "" && f.Projects[i].Title != title {
					f.Projects[i].Title = title
					changed = true
				}
				if !changed {
					return false, nil
				}
				return true, nil
			}
		}
		f.Projects = append(f.Projects, desktopProject{Root: root, Title: title})
		return true, nil
	})
}

func renameProject(root, title string) error {
	title = strings.TrimSpace(title)
	root = normalizeProjectRoot(root)
	return updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		if root == "" {
			if f.GlobalTitle == title {
				return false, nil
			}
			f.GlobalTitle = title
			return true, nil
		}
		for i, p := range f.Projects {
			if sameProjectRoot(p.Root, root) {
				if f.Projects[i].Root == root && f.Projects[i].Title == title {
					return false, nil
				}
				f.Projects[i].Root = root
				f.Projects[i].Title = title
				return true, nil
			}
		}
		f.Projects = append(f.Projects, desktopProject{Root: root, Title: title})
		return true, nil
	})
}

func setProjectColor(root, color string) error {
	root = normalizeProjectRoot(root)
	color = normalizeProjectColor(color)
	return updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		if root == "" {
			if f.GlobalColor == color {
				return false, nil
			}
			f.GlobalColor = color
			return true, nil
		}
		for i, p := range f.Projects {
			if sameProjectRoot(p.Root, root) {
				if f.Projects[i].Root == root && f.Projects[i].Color == color {
					return false, nil
				}
				f.Projects[i].Root = root
				f.Projects[i].Color = color
				return true, nil
			}
		}
		f.Projects = append(f.Projects, desktopProject{Root: root, Color: color})
		return true, nil
	})
}

func removeProject(root string) error {
	root = normalizeProjectRoot(root)
	return updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		projects := make([]desktopProject, 0, len(f.Projects))
		for _, p := range f.Projects {
			if !sameProjectRoot(p.Root, root) {
				projects = append(projects, p)
			}
		}
		if len(projects) == len(f.Projects) {
			return false, nil
		}
		f.Projects = projects
		return true, nil
	})
}

// topic helpers

const (
	defaultTopicTitle      = "新的会话"
	defaultTopicTitleEn    = "New session"
	topicTitleSourceAuto   = "auto"
	topicTitleSourceManual = "manual"
)

const (
	desktopLocaleUnknown int32 = iota
	desktopLocaleEn
	desktopLocaleZh
)

func (a *App) setDesktopLocale(locale string) {
	normalized := strings.ToLower(strings.TrimSpace(locale))
	switch {
	case strings.HasPrefix(normalized, "zh"):
		a.desktopLocale.Store(desktopLocaleZh)
	default:
		a.desktopLocale.Store(desktopLocaleEn)
	}
}

func (a *App) localizedDefaultTopicTitle() string {
	switch a.desktopLocale.Load() {
	case desktopLocaleZh:
		return defaultTopicTitle
	case desktopLocaleEn:
		return defaultTopicTitleEn
	default:
		return defaultTopicTitle
	}
}

func isDefaultTopicTitle(title string) bool {
	switch strings.TrimSpace(title) {
	case "", defaultTopicTitle, defaultTopicTitleEn:
		return true
	default:
		return false
	}
}

func (a *App) localizedTopicTitle(title, source string) string {
	if strings.TrimSpace(source) == topicTitleSourceAuto && isDefaultTopicTitle(title) {
		return a.localizedDefaultTopicTitle()
	}
	return title
}

func (a *App) forkTopicTitle(title string) string {
	base := strings.TrimSpace(title)
	if base == "" || isDefaultTopicTitle(base) {
		switch a.desktopLocale.Load() {
		case desktopLocaleEn:
			base = defaultTopicTitleEn
		default:
			base = defaultTopicTitle
		}
	}
	return sessiontitle.IncreaseFork(base)
}

func saveTelemetry(path string, snapshot tabTelemetrySnapshot) error {
	if snapshot.Version == 0 {
		snapshot.Version = 3
	}
	if snapshot.ReadFiles == nil {
		snapshot.ReadFiles = []readFileRecord{}
	}
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return fileutil.ReplaceFile(tmp, path)
}

func loadTelemetry(path string) tabTelemetrySnapshot {
	empty := tabTelemetrySnapshot{Version: 3, ReadFiles: []readFileRecord{}}
	b, err := readFileUTF8(path)
	if err != nil {
		return empty
	}
	var snapshot tabTelemetrySnapshot
	if json.Unmarshal(b, &snapshot) != nil || snapshot.Version != 3 {
		return empty
	}
	if snapshot.ReadFiles == nil {
		snapshot.ReadFiles = []readFileRecord{}
	}
	return snapshot
}

// project tree

func normalizeTopicStatus(status string) string {
	switch status {
	case topicStatusThinking, topicStatusStreaming, topicStatusWaitingConfirmation, topicStatusBackgroundJob, topicStatusPaused, topicStatusAwaitingDelivery, topicStatusError:
		return status
	default:
		return ""
	}
}

// TopicMeta describes a topic for the project tree.
type TopicMeta struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
}

// CreateTopic creates a new topic under a project workspace and returns its metadata.
func (a *App) CreateTopic(scope, workspaceRoot, title string) (TopicMeta, error) {
	scope = strings.TrimSpace(scope)
	if scope != "project" && scope != "global" {
		return TopicMeta{}, fmt.Errorf("unsupported topic scope %q", scope)
	}
	if scope == "global" {
		workspaceRoot = ""
	}
	if workspaceRoot != "" {
		if abs, err := filepath.Abs(workspaceRoot); err == nil {
			workspaceRoot = abs
		}
	}
	releaseAdmission, err := a.beginProjectRuntimeAdmission(scope, workspaceRoot)
	if err != nil {
		return TopicMeta{}, err
	}
	defer releaseAdmission()
	return a.createColdSessionTopic(scope, workspaceRoot, strings.TrimSpace(title))
}

// RenameProject updates the sidebar-only display title for a project folder.
// Empty title clears the override and falls back to the folder name.
func (a *App) RenameProject(workspaceRoot, title string) error {
	if err := renameProject(workspaceRoot, title); err != nil {
		return err
	}
	a.syncTabWorkspaceRootSpellings()
	a.emitProjectTreeMetadataChanged()
	return nil
}

// SetProjectColor updates the project-level accent color used by project topics
// in the sidebar and tabs. Empty color restores the default accent.
func (a *App) SetProjectColor(workspaceRoot, color string) error {
	if err := setProjectColor(workspaceRoot, color); err != nil {
		return err
	}
	a.syncTabWorkspaceRootSpellings()
	a.emitProjectTreeMetadataChanged()
	return nil
}

// SetProjectPinned controls whether a project folder is pinned above the rest of
// the desktop project tree.
func (a *App) SetProjectPinned(workspaceRoot string, pinned bool) error {
	root := normalizeProjectRoot(workspaceRoot)
	if root == "" {
		return fmt.Errorf("workspaceRoot is required")
	}
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		i := projectIndexByRoot(f.Projects, root)
		if i < 0 {
			return false, fmt.Errorf("project %q not found", root)
		}
		root = f.Projects[i].Root
		next := make([]string, 0, len(f.PinnedProjects))
		for _, pinnedRoot := range f.PinnedProjects {
			if !sameProjectRoot(pinnedRoot, root) {
				next = append(next, pinnedRoot)
			}
		}
		if pinned {
			next = prependUniqueString(next, root)
		}
		if sameStringList(next, f.PinnedProjects) {
			return false, nil
		}
		f.PinnedProjects = next
		return true, nil
	}); err != nil {
		return err
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

// ReorderProjects persists the user-defined order of project folders and,
// when present, the virtual Global sidebar section.
func (a *App) ReorderProjects(workspaceRoots []string) error {
	if err := updateProjectsFile(func(f *desktopProjectFile) (bool, error) {
		var seenProjects []string
		next := make([]desktopProject, 0, len(workspaceRoots))
		sidebarOrder := make([]string, 0, len(workspaceRoots))
		hasGlobalOrder := false
		for _, root := range workspaceRoots {
			root = strings.TrimSpace(root)
			if root == desktopGlobalOrderToken {
				if hasGlobalOrder {
					return false, fmt.Errorf("duplicate global section")
				}
				hasGlobalOrder = true
				sidebarOrder = append(sidebarOrder, root)
				continue
			}
			root = normalizeProjectRoot(root)
			i := projectIndexByRoot(f.Projects, root)
			if i < 0 {
				return false, fmt.Errorf("project %q not found", root)
			}
			project := f.Projects[i]
			if projectRootInList(seenProjects, project.Root) {
				return false, fmt.Errorf("duplicate project %q", root)
			}
			seenProjects = append(seenProjects, project.Root)
			next = append(next, project)
			sidebarOrder = append(sidebarOrder, project.Root)
		}
		if len(next) != len(f.Projects) {
			return false, fmt.Errorf("project order length mismatch")
		}
		changed := !sameProjectOrder(next, f.Projects)
		f.Projects = next
		if hasGlobalOrder {
			if !sameStringList(sidebarOrder, f.SidebarOrder) {
				changed = true
			}
			f.SidebarOrder = sidebarOrder
		} else {
			if len(f.SidebarOrder) > 0 {
				changed = true
			}
			f.SidebarOrder = nil
		}
		return changed, nil
	}); err != nil {
		return err
	}
	a.emitProjectTreeMetadataChanged()
	return nil
}

// RenameTopic updates a topic's display title.
func (a *App) RenameTopic(topicID, title string) error {
	title = strings.TrimSpace(title)
	a.topicTitleMutationMu.Lock()
	defer a.topicTitleMutationMu.Unlock()
	// Keep candidate protection inside the same mutation fence used by the
	// cleanup worker. Otherwise a rename can mark an archive-pending candidate
	// as protected while that worker still proceeds to remove its index.
	// Same-value manual renames remain durable evidence of use.
	if handled, err := a.updateCanonicalTopicPresentation(topicID, &title, nil); handled || err != nil {
		return err
	}
	return fmt.Errorf("topic %q not found", topicID)
}

func (a *App) emitProjectTreeChanged() {
	a.requestProjectTreeCatalogRefresh()
	a.emitProjectTreeChangedEvent()
}

func (a *App) requestProjectTreeCatalogRefresh() {
	if a.projectTreeCatalogRefreshHook != nil {
		a.projectTreeCatalogRefreshHook()
	}
	a.requestSessionCatalogMetadataSync()
}

// emitProjectTreeMetadataChanged refreshes ordering, titles, pins, and runtime
// status without walking session storage.
func (a *App) emitProjectTreeMetadataChanged() {
	a.requestSessionCatalogMetadataSync()
	a.emitProjectTreeChangedEvent()
}

// SetTopicPinned controls whether a topic is pinned to the top of its project
// or Global section in the desktop project tree.
func (a *App) SetTopicPinned(topicID string, pinned bool) error {
	if handled, err := a.updateCanonicalTopicPresentation(topicID, nil, &pinned); handled || err != nil {
		return err
	}
	return fmt.Errorf("topic %q not found", topicID)
}

// ListProjectTree builds the sidebar tree: project folders each containing
// their topics, plus a Global section.

func projectSessionNodeKey(scope, sessionPath string) string {
	sum := sha256.Sum256([]byte(sessionRuntimeKey(sessionPath)))
	return scope + "_session_" + hex.EncodeToString(sum[:8])
}

// ContextPanelInfo is the right-side panel's data for one tab.
type ContextPanelInfo struct {
	UsedTokens       int  `json:"usedTokens"`
	WindowTokens     int  `json:"windowTokens"`
	PromptTokens     int  `json:"promptTokens"`
	CompletionTokens int  `json:"completionTokens"`
	TotalTokens      int  `json:"totalTokens"`
	ReasoningTokens  int  `json:"reasoningTokens"`
	CacheHitTokens   int  `json:"cacheHitTokens"`
	CacheMissTokens  int  `json:"cacheMissTokens"`
	Estimated        bool `json:"estimated,omitempty"`
	// Session-cumulative token counts (from telemetry, atomic snapshot).
	// Separate from the per-turn fields above so existing consumers (status bar
	// turn tokens, donut chart) are unaffected.
	SessionCacheHitTokens   int                         `json:"sessionCacheHitTokens"`
	SessionCacheMissTokens  int                         `json:"sessionCacheMissTokens"`
	SessionCompletionTokens int                         `json:"sessionCompletionTokens"`
	SessionEstimated        bool                        `json:"sessionEstimated,omitempty"`
	RequestCount            int                         `json:"requestCount"`
	ElapsedMs               int64                       `json:"elapsedMs"`                     // finished turns only
	ActiveTurnStartedAt     int64                       `json:"activeTurnStartedAt,omitempty"` // unix ms; 0 when idle
	SessionCost             float64                     `json:"sessionCost"`
	SessionCurrency         string                      `json:"sessionCurrency,omitempty"`
	SessionCostComplete     bool                        `json:"sessionCostComplete,omitempty"`
	SessionCostEstimated    bool                        `json:"sessionCostEstimated,omitempty"`
	SessionBillingMode      string                      `json:"sessionBillingMode,omitempty"`
	SessionCostQuote        *billing.CostQuote          `json:"sessionCostQuote,omitempty"`
	Sources                 map[string]usageSourceStats `json:"sources,omitempty"`
	Mock                    bool                        `json:"mock,omitempty"`
	ReadFiles               []readFileRecord            `json:"readFiles"`
	ChangedFiles            []ChangedFileInfo           `json:"changedFiles"`
	ContextBudget           *ContextBudgetInfo          `json:"contextBudget,omitempty"`
}

type ChangedFileInfo struct {
	Path         string   `json:"path"`
	OldPath      string   `json:"oldPath,omitempty"`
	Sources      []string `json:"sources"`
	GitStatus    string   `json:"gitStatus,omitempty"`
	Turns        []int    `json:"turns"`
	LatestPrompt string   `json:"latestPrompt,omitempty"`
	LatestTime   int64    `json:"latestTime,omitempty"`
}

// ContextPanel returns the context usage, read files, and changed files for a
// specific tab.
func (a *App) ContextPanel(tabID string) ContextPanelInfo {
	if a.isRemoteTab(tabID) {
		used, window, ok := a.remoteContextSnapshot(tabID)
		if ok {
			return ContextPanelInfo{UsedTokens: used, WindowTokens: window, ReadFiles: []readFileRecord{}, ChangedFiles: []ChangedFileInfo{}}
		}
		return ContextPanelInfo{ReadFiles: []readFileRecord{}, ChangedFiles: []ChangedFileInfo{}}
	}
	read := a.captureContextRead(tabID)
	ctrl, telemetry := read.ctrl, read.telemetry
	if read.tab == nil {
		return ContextPanelInfo{ReadFiles: []readFileRecord{}, ChangedFiles: []ChangedFileInfo{}}
	}

	info := ContextPanelInfo{ReadFiles: []readFileRecord{}, ChangedFiles: []ChangedFileInfo{}}
	if ctrl != nil {
		_, window := ctrl.ContextSnapshot()
		info.WindowTokens = window
		// This panel breaks the last turn down into segments, so its total must
		// be that turn's usage and not the live-view measurement the status-bar
		// gauge reports — otherwise the segments stop summing to the total.
		if u := ctrl.LastUsage(); u != nil {
			info.UsedTokens = u.PromptTokens + u.CompletionTokens
		}
		if info.UsedTokens == 0 {
			if snap := telemetry; snap.Usage.LastUsedTokens > 0 {
				info.UsedTokens = snap.Usage.LastUsedTokens
			}
		}
		if u := ctrl.LastUsage(); u != nil {
			info.PromptTokens = u.PromptTokens
			info.CompletionTokens = u.CompletionTokens
			info.ReasoningTokens = u.ReasoningTokens
			info.CacheHitTokens = u.CacheHitTokens
			info.CacheMissTokens = u.CacheMissTokens
			info.Estimated = u.Estimated
		} else {
			// Executor rebuilt (session rebind): fall back to the telemetry-
			// persisted per-turn breakdown so the donut chart and type
			// breakdown show the last turn's composition instead of "other".
			snap := telemetry
			info.PromptTokens = snap.Usage.LastPromptTokens
			info.CompletionTokens = snap.Usage.LastCompletionTokens
			info.ReasoningTokens = snap.Usage.LastReasoningTokens
			info.CacheHitTokens = snap.Usage.LastCacheHitTokens
			info.CacheMissTokens = snap.Usage.LastCacheMissTokens
			info.Estimated = snap.Usage.LastEstimated
		}
	}

	if records := telemetry.ReadFiles; records != nil {
		info.ReadFiles = records
	}
	usage := telemetry.Usage
	info.TotalTokens = usage.TotalTokens
	info.RequestCount = usage.RequestCount
	info.ElapsedMs = read.runtime.completedMs
	info.ActiveTurnStartedAt = read.runtime.turnStartedAt
	info.SessionCost = usage.SessionCost
	info.SessionCurrency = usage.SessionCurrency
	info.SessionCostComplete = usage.SessionCostComplete
	info.SessionCostEstimated = true
	info.SessionCostQuote = usage.SessionCostQuote
	if usage.SessionCostQuote != nil {
		info.SessionBillingMode = usage.SessionCostQuote.BillingMode
		info.SessionCostEstimated = usage.SessionCostQuote.Estimated
		if !usage.SessionCostQuote.Complete {
			info.SessionCostComplete = false
		}
	}
	info.Sources = usage.Sources
	info.SessionCacheHitTokens = usage.CacheHitTokens
	info.SessionCacheMissTokens = usage.CacheMissTokens
	info.SessionCompletionTokens = usage.CompletionTokens
	info.SessionEstimated = usage.Estimated
	if ctrl != nil {
		if snap := ctrl.ContextMaintenanceSnapshot(); snap.ContextBudget != nil {
			info.ContextBudget = contextBudgetInfo(snap.ContextBudget)
		}
	}

	// Gather workspace changes for this tab's root.
	if ctrl != nil && read.workspaceRoot != "" {
		for _, meta := range ctrl.Checkpoints() {
			for _, path := range meta.Paths {
				info.ChangedFiles = append(info.ChangedFiles, ChangedFileInfo{
					Path:         path,
					Sources:      []string{"session"},
					Turns:        []int{meta.Turn},
					LatestPrompt: meta.Prompt,
					LatestTime:   meta.Time.UnixMilli(),
				})
			}
		}
	}

	if !read.current(a) {
		return ContextPanelInfo{ReadFiles: []readFileRecord{}, ChangedFiles: []ChangedFileInfo{}}
	}
	return info
}

// utility

func (a *App) newUniqueTabIDLocked() string {
	for {
		id := newTabID()
		if _, exists := a.tabs[id]; !exists {
			return id
		}
	}
}

func (a *App) restoredTabIDLocked(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return a.newUniqueTabIDLocked()
	}
	if _, exists := a.tabs[id]; exists {
		return a.newUniqueTabIDLocked()
	}
	return id
}

func normalizeTabMode(mode string) string {
	if mode == "plan" {
		return "plan"
	}
	return "normal"
}

func tabModeForPlan(plan bool) string {
	if plan {
		return "plan"
	}
	return "normal"
}

func tabModeHasPlan(mode string) bool { return normalizeTabMode(mode) == "plan" }

func currentTabMode(tab *WorkspaceTab) string {
	if tab == nil {
		return "normal"
	}
	if tab.Ctrl != nil {
		return tabModeForPlan(tab.Ctrl.PlanMode())
	}
	return normalizeTabMode(tab.mode)
}

func currentTabGoal(tab *WorkspaceTab) string {
	if tab == nil {
		return ""
	}
	if tab.Ctrl != nil {
		return tab.Ctrl.Goal()
	}
	return strings.TrimSpace(tab.goal)
}

func currentTabGoalStatus(tab *WorkspaceTab) string {
	if tab == nil {
		return control.GoalStatusStopped
	}
	if tab.Ctrl != nil {
		return tab.Ctrl.GoalStatus()
	}
	if strings.TrimSpace(tab.goal) != "" {
		return control.GoalStatusRunning
	}
	return control.GoalStatusStopped
}

func currentTabCollaborationMode(tab *WorkspaceTab) string {
	if tab == nil {
		return "normal"
	}
	if tabModeHasPlan(currentTabMode(tab)) {
		return "plan"
	}
	if strings.TrimSpace(currentTabGoal(tab)) != "" && currentTabGoalStatus(tab) == control.GoalStatusRunning {
		return "goal"
	}
	return "normal"
}

func currentTabToolApprovalMode(tab *WorkspaceTab) string {
	if tab == nil {
		return control.ToolApprovalWorkspaceWrite
	}
	if tab.Ctrl != nil {
		return tab.Ctrl.ToolApprovalMode()
	}
	return normalizeToolApprovalMode(tab.toolApprovalMode)
}

// Snapshot-based forms of the currentTabX helpers, for callers that already
// hold a consistent tabRuntimeSnapshot.

func (s tabRuntimeSnapshot) currentMode() string {
	if s.ctrl != nil {
		return tabModeForPlan(s.ctrl.PlanMode())
	}
	return normalizeTabMode(s.mode)
}

func (s tabRuntimeSnapshot) currentGoal() string {
	if s.ctrl != nil {
		return s.ctrl.Goal()
	}
	return strings.TrimSpace(s.goal)
}

func (s tabRuntimeSnapshot) currentGoalStatus() string {
	if s.ctrl != nil {
		return s.ctrl.GoalStatus()
	}
	if strings.TrimSpace(s.goal) != "" {
		return control.GoalStatusRunning
	}
	return control.GoalStatusStopped
}

func (s tabRuntimeSnapshot) collaborationMode() string {
	if tabModeHasPlan(s.currentMode()) {
		return "plan"
	}
	return "normal"
}

func (s tabRuntimeSnapshot) currentToolApprovalMode() string {
	if s.ctrl != nil {
		return s.ctrl.ToolApprovalMode()
	}
	return normalizeToolApprovalMode(s.toolApprovalMode)
}

// normalizedRuntime reads live Controller state only after the App snapshot has
// released a.mu. Rebuild callers hold turnStartMu while invoking it, so all
// three axes and the legacy Goal fallback describe one admitted runtime state.
func (s tabRuntimeSnapshot) normalizedRuntime() normalizedTabRuntime {
	plan := tabModeHasPlan(normalizeTabMode(s.mode))
	approvalMode := normalizeToolApprovalMode(s.toolApprovalMode)
	goal := strings.TrimSpace(s.goal)
	goalStatus := control.GoalStatusStopped
	if goal != "" {
		goalStatus = control.GoalStatusRunning
	}
	if s.ctrl != nil {
		plan = s.ctrl.PlanMode()
		approvalMode = normalizeToolApprovalMode(s.ctrl.ToolApprovalMode())
		goal = strings.TrimSpace(s.ctrl.Goal())
		goalStatus = s.ctrl.GoalStatus()
	}

	runtime := normalizedTabRuntime{
		collaborationMode: "normal",
		toolApprovalMode:  approvalMode,
	}
	switch {
	case plan:
		runtime.collaborationMode = "plan"
	case goal != "" && goalStatus == control.GoalStatusRunning:
		runtime.collaborationMode = "goal"
		runtime.goal = goal
	}
	return runtime
}

func (r normalizedTabRuntime) tabMode() string {
	return tabModeForPlan(r.collaborationMode == "plan")
}

func applyNormalizedRuntimeToTabLocked(tab *WorkspaceTab, runtime normalizedTabRuntime) {
	if tab == nil {
		return
	}
	tab.mode = runtime.tabMode()
	tab.toolApprovalMode = normalizeToolApprovalMode(runtime.toolApprovalMode)
	if runtime.collaborationMode == "goal" {
		tab.goal = strings.TrimSpace(runtime.goal)
	} else {
		tab.goal = ""
	}
}

func normalizeToolApprovalMode(mode string) string {
	return config.NormalizeToolApprovalMode(mode)
}

func persistedToolApprovalMode(mode string) string {
	return normalizeToolApprovalMode(mode)
}

// persistedTabMode stores the collaboration axis independently of permissions.
func persistedTabMode(mode string) string {
	switch normalizeTabMode(mode) {
	case "plan":
		return "plan"
	}
	return ""
}

func newTabID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UTC()
		return "tab_" + now.Format("20060102150405") + "_" + fmt.Sprintf("%09d", now.Nanosecond())
	}
	return "tab_" + hex.EncodeToString(b[:])
}

func newTopicID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UTC()
		return "topic_" + now.Format("20060102-150405") + "_" + fmt.Sprintf("%09d", now.Nanosecond())
	}
	return "topic_" + time.Now().UTC().Format("20060102-150405") + "_" + hex.EncodeToString(b[:])
}

func globalWorkspaceRoot() string {
	return filepath.Join(desktopConfigDir(), "global-workspace")
}

func ensureGlobalWorkspaceRoot() (string, error) {
	root := globalWorkspaceRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

func globalTabWorkspaceRoot() string {
	root, err := ensureGlobalWorkspaceRoot()
	if err != nil {
		return globalWorkspaceRoot()
	}
	return root
}

type tabSessionProfile struct {
	mode                   string
	toolApprovalMode, goal string
}

func defaultTabSessionProfile() tabSessionProfile {
	return tabSessionProfile{
		mode:             "normal",
		toolApprovalMode: control.ToolApprovalWorkspaceWrite,
	}
}

func applyTabSessionProfile(tab *WorkspaceTab, profile tabSessionProfile) {
	if tab == nil {
		return
	}
	tab.mode = normalizeTabMode(profile.mode)
	tab.toolApprovalMode = normalizeToolApprovalMode(profile.toolApprovalMode)

	tab.mode = tabModeForPlan(tabModeHasPlan(tab.mode))
	tab.goal = strings.TrimSpace(profile.goal)
}

func persistedTabGoal(tab *WorkspaceTab) string {
	goal := strings.TrimSpace(currentTabGoal(tab))
	if goal == "" || currentTabGoalStatus(tab) != control.GoalStatusRunning {
		return ""
	}
	return goal
}

func (a *App) rememberTabSessionPath(tab *WorkspaceTab, path string) {
	if tab == nil {
		return
	}
	locator := classifySessionLocator(path)
	if locator.kind == sessionLocatorInvalid || locator.kind == sessionLocatorEmpty {
		return
	}
	a.mu.Lock()
	if current := a.tabs[tab.ID]; current == tab {
		setTabSessionIdentity(tab, path)
		a.saveTabsLocked()
	} else {
		setTabSessionIdentity(tab, path)
	}
	a.mu.Unlock()
}
