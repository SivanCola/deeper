package acp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/extension/uihub"
	"deepzora/internal/fileutil"
	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/jobs"
	"deepzora/internal/persistentshell"
	"deepzora/internal/plugin"
	"deepzora/internal/provider"
	"deepzora/internal/session"
	"deepzora/internal/sessioninbox"
	"deepzora/internal/sessiontemp"
	"deepzora/internal/tool/builtin"
)

// SessionParams is everything a Factory needs to assemble one ACP session's
// controller. Sink is owned by this package (an updateSink bound to the session
// id) and must be wired into the controller's event sink; the controller's
// interactive approval (see control.Controller.EnableInteractiveApproval) then
// routes "ask" decisions back through that sink as ApprovalRequest events, which
// the sink forwards to the client over session/request_permission.
//
// Cwd roots the session's file tools and bash (built via builtin.Workspace).
// Model, EffortOverride are optional session-local selectors
// from ACP config options. MCPServers are the MCP servers the client asked the
// agent to connect for this session. The path hooks keep service bookkeeping
// aligned; factories must wire both into the controller they build.
type SessionParams struct {
	SessionRuntime  *session.Runtime
	BackgroundScope *jobs.SessionBackgroundScope
	SessionTemp     *sessiontemp.Manager
	PersistentShell *persistentshell.Manager
	// MCPInteractions enables interactive MCP only after explicit client negotiation.
	MCPInteractions bool
	Cwd             string
	MCPServers      []plugin.Spec
	Sink            event.Sink
	Model           string
	EffortOverride  *string
	// FileOverlay and Terminal are non-nil when the client advertised the
	// matching capability at initialize: file tools then see unsaved editor
	// buffers, and foreground bash can run in a client-owned terminal.
	// Factories thread them into the controller's tool assembly.
	FileOverlay builtin.FileOverlay
	Terminal    builtin.TerminalRunner
}

// Factory builds the per-session controller. The composition root (the cli's
// `deepzora acp` command) implements it by reusing setup()'s assembly: a
// Provider for Model, a tool Registry rooted at Cwd via builtin.Workspace, a
// per-session MCP host from MCPServers, the event Sink, all wired into a
// control.Controller. The returned controller owns its own cleanup (Close stops
// MCP subprocesses), so the service calls ctrl.Close() on teardown.
type Factory interface {
	NewSession(ctx context.Context, p SessionParams) (*control.Controller, error)
}

// SessionConfigStateParams asks the Factory for normalized session config
// selectors. Empty Model use configured defaults. Nil
// EffortOverride means provider config wins; a non-nil empty string means
// provider default for this session.
type SessionConfigStateParams struct {
	Cwd            string
	Model          string
	EffortOverride *string
}

// SessionConfigState is the complete ACP-visible config state for a session.
type SessionConfigState struct {
	Model          string
	EffortOverride *string
	ConfigOptions  []SessionConfigOption
}

// SessionConfigStateProvider lets a Factory expose model and effort selectors
// without making the ACP transport depend on a concrete config backend.
type SessionConfigStateProvider interface {
	SessionConfigState(ctx context.Context, p SessionConfigStateParams) (SessionConfigState, error)
}

// SessionDirProvider lets a Factory expose the persistent session directory
// without forcing session/list to build a controller first.
type SessionDirProvider interface {
	SessionDir() string
}

// SessionRebuilder lets a Factory rebuild a session's controller via
// boot.Rebuild: the replacement is built with the same boot.Options NewSession
// would use, and the session state (history, approval grants, goal/recovery,
// lifecycle) migrates off old inside the boot layer. The caller keeps the
// swap/close ordering. Factories that do not implement it leave
// _deepzora.com/session/reloadExtensions reporting unavailable.
type SessionRebuilder interface {
	RebuildSession(ctx context.Context, p SessionParams, old *control.Controller) (*control.Controller, error)
}

// AgentInfo identifies this agent to clients in the initialize reply.
type AgentInfo struct {
	Name    string
	Version string
}

// Serve runs an ACP agent on r/w (stdin/stdout in production) until the input
// ends or ctx is cancelled. It owns the JSON-RPC connection and the session
// registry; the Factory supplies the kernel wiring. This is the single entry
// point the `deepzora acp` command calls.
//
// stdout is the JSON-RPC channel: callers must keep all other output (logs,
// diagnostics) off w and on stderr, or the wire corrupts.
func Serve(ctx context.Context, r io.Reader, w io.Writer, factory Factory, info AgentInfo) error {
	conn := NewConn(r, w)
	svc := &service{
		conn:     conn,
		factory:  factory,
		info:     info,
		sessions: make(map[string]*acpSession),
	}
	conn.Handle("initialize", svc.initialize)
	conn.Handle("authenticate", svc.authenticate)
	conn.Handle("session/new", svc.sessionNew)
	conn.Handle("session/load", svc.sessionLoad)
	conn.Handle("session/resume", svc.sessionResume)
	conn.Handle("session/prompt", svc.sessionPrompt)
	conn.Handle(sessionSteerMethod, svc.sessionSteer)
	conn.Handle(sessionInboxEnqueueMethod, svc.sessionInboxEnqueue)
	conn.Handle(sessionInboxListMethod, svc.sessionInboxList)
	conn.Handle(sessionInboxGetMethod, svc.sessionInboxGet)
	conn.Handle(sessionInboxUpdateMethod, svc.sessionInboxUpdate)
	conn.Handle(sessionInboxDeleteMethod, svc.sessionInboxDelete)
	conn.Handle(sessionInboxMoveMethod, svc.sessionInboxMove)
	conn.Handle(sessionInboxPauseMethod, svc.sessionInboxSetPaused)
	conn.Handle(sessionInboxRetryMethod, svc.sessionInboxRetry)
	conn.Handle(sessionInboxRefreshMethod, svc.sessionInboxRefresh)
	conn.Handle(sessionReloadExtensionsMethod, svc.sessionReloadExtensions)
	conn.Handle(sessionStatusMethod, svc.sessionStatus)
	conn.Handle("session/set_config_option", svc.sessionSetConfigOption)
	conn.Handle("session/set_mode", svc.sessionSetMode)
	conn.Handle("session/close", svc.sessionClose)
	conn.Handle("session/list", svc.sessionList)
	conn.Handle("session/delete", svc.sessionDelete)
	conn.HandleNotify("session/cancel", svc.sessionCancel)
	defer svc.closeAll()
	return conn.Serve(ctx)
}

// service holds the connection-wide ACP state: the factory, agent identity, and
// the live session registry.
type service struct {
	conn    *Conn
	factory Factory
	info    AgentInfo

	mu       sync.Mutex
	sessions map[string]*acpSession
	// clientCaps is what the client offered at initialize (fs proxy, host
	// terminals). Zero until initialize arrives; sessions opened later bind a
	// clientIO built from it.
	clientCaps ClientCapabilities
}

// afterResponse wraps a result with work that must run after the transport has
// successfully written that result. Session-opening notifications use this so a
// client can register the returned session before receiving its first update.
type afterResponse struct {
	result any
	after  func()
}

func (r afterResponse) Response() any { return r.result }

func (r afterResponse) AfterResponse() {
	if r.after != nil {
		r.after()
	}
}

func (s *service) setClientCapabilities(caps ClientCapabilities) {
	s.mu.Lock()
	s.clientCaps = caps
	s.mu.Unlock()
}

func (s *service) clientCapabilities() ClientCapabilities {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clientCaps
}

// extensionSurfaceSupported reports whether the connected client advertised
// deepzora.extensionSurface support in its initialize handshake.
func (s *service) extensionSurfaceSupported() bool {
	return clientExtensionSurfaceSupported(s.clientCapabilities())
}

// clientExtensionSurfaceSupported tolerantly parses the client's vendor
// capability block: _meta["deepzora.com"]["extensionSurface"]["supported"] must
// be an explicit true. Absent keys, wrong shapes, or a malformed block all
// mean unsupported — the sink then sends only the text fallback.
func clientExtensionSurfaceSupported(caps ClientCapabilities) bool {
	vendor, ok := caps.Meta["deepzora.com"].(map[string]any)
	if !ok {
		return false
	}
	capability, ok := vendor["extensionSurface"].(map[string]any)
	if !ok {
		return false
	}
	supported, _ := capability["supported"].(bool)
	return supported
}

// bindClientIO fills SessionParams' overlay/terminal fields from the client's
// declared capabilities. The nil checks keep absent capabilities as nil
// interface fields (a typed-nil *clientIO must never reach the interface).
func (s *service) bindClientIO(p *SessionParams, sessionID string) {
	p.MCPInteractions = clientMCPInteractionSupported(s.clientCapabilities())
	io := newClientIO(s.conn, sessionID, s.clientCapabilities())
	if !io.hasAny() {
		return
	}
	if fo := io.fileOverlay(); fo != nil {
		p.FileOverlay = fo
	}
	if tr := io.terminalRunner(); tr != nil {
		p.Terminal = tr
	}
}

// acpController is the slice of the controller's driving port the ACP transport
// drives: session lifecycle + persistence, turn execution, interactive approval,
// and the capability surface (commands/skills/MCP prompts). ACP never touches
// goals, checkpoints, or memory, so it depends on those sub-ports only — not the
// concrete *control.Controller.
type acpController interface {
	control.Lifecycle
	control.TurnControl
	RunTurnWithRaw(ctx context.Context, input, raw, invokedSkill string) error
	RunSkillWithName(input string) (sent, name string, found bool)
	control.Approvals
	control.Capabilities
	control.SessionPersistence
	// Goals backs ACP's normal/plan/goal collaboration-mode surface.
	control.Goals
}

// acpSession is one open session: its controller, the on-disk transcript path
// (empty when persistence is off), and the cancel func of the in-flight turn
// (nil when idle) so session/cancel can abort it.
type acpSession struct {
	id         string
	ctrl       acpController
	sink       *updateSink
	metaPath   string
	cwd        string
	mcpServers []plugin.Spec
	model      string
	// nil means use config; non-nil empty string means provider default.
	effortOverride   *string
	toolApprovalMode string
	// runtimeState is the effective planner/sandbox posture captured after CLI
	// hard overrides. status snapshots never reconstruct it from user config.
	runtimeState SessionRuntimeState
	status       *statusTelemetry
	// modeID is the ACP collaboration mode last reported to the client (normal |
	// plan | goal). Goal draft mode turns the next user prompt into the goal.
	// Both are guarded by mu; controller-side completion/plan exit is reconciled
	// after each turn through current_mode_update.
	modeID        string
	goalDraftMode bool
	// pendingConfig queues config deltas requested while a turn or rebuild is
	// in flight, holding at most one entry per axis: a later request replaces
	// only its own axis (last-write-wins per axis), so independent changes queued
	// back to back during one turn both survive to the drain.
	pendingConfig []sessionConfigDelta
	// pendingReload coalesces _deepzora.com/session/reloadExtensions requests
	// made while a turn or a rebuild is in flight; the finishTurn /
	// post-maintenance drains run it once the session is idle.
	pendingReload bool
	title         string
	createdAt     time.Time
	updatedAt     time.Time

	mu sync.Mutex
	// stateChangeMu serializes controller rebuilds with collaboration/approval
	// changes so a swap cannot overwrite a newer user selection.
	stateChangeMu sync.Mutex
	cancel        context.CancelFunc
	done          chan struct{}
	running       bool
	deleted       bool
	// maintenanceDone is non-nil while session-owned maintenance, such as an
	// idle config rebuild, is in flight outside mu.
	maintenanceDone chan struct{}
}

func (s *acpSession) begin(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	runCtx, cancel := context.WithCancel(ctx)
	// Prompt admission and config-axis changes share this lock. TryLock keeps
	// ACP admission non-blocking while closing the idle-check/use window in an
	// in-place role switch.
	if !s.stateChangeMu.TryLock() {
		cancel()
		return nil, nil, false
	}
	defer s.stateChangeMu.Unlock()
	s.mu.Lock()
	// A queued pendingConfig blocks new turns so a prompt never runs on the
	// outgoing config. The turn or maintenance that queued it applies it from
	// its defer, so no new turn is needed to drain the queue.
	if s.running || s.deleted || s.maintenanceDone != nil || len(s.pendingConfig) > 0 {
		s.mu.Unlock()
		cancel()
		return nil, nil, false
	}
	s.running = true
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	return runCtx, cancel, true
}

func (s *acpSession) finish() {
	s.mu.Lock()
	done := s.done
	s.running = false
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if done != nil {
		close(done)
	}
}

func (s *acpSession) abort() {
	s.mu.Lock()
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
	}
}

func (s *acpSession) abortAndWait() {
	s.mu.Lock()
	c := s.cancel
	done := s.done
	maintenanceDone := s.maintenanceDone
	s.mu.Unlock()
	if c != nil {
		c()
	}
	if done != nil {
		<-done
	}
	if maintenanceDone != nil {
		<-maintenanceDone
	}
}

func (s *acpSession) deleteAndWait() {
	s.mu.Lock()
	s.deleted = true
	c := s.cancel
	done := s.done
	maintenanceDone := s.maintenanceDone
	s.mu.Unlock()
	if c != nil {
		c()
	}
	if done != nil {
		<-done
	}
	if maintenanceDone != nil {
		<-maintenanceDone
	}
}

func (s *acpSession) finishMaintenance(done chan struct{}) {
	if done == nil {
		return
	}
	closeDone := false
	s.mu.Lock()
	if s.maintenanceDone == done {
		s.maintenanceDone = nil
		closeDone = true
	}
	s.mu.Unlock()
	if closeDone {
		close(done)
	}
}

// swapModeID records the mode reported to the client and returns the previous
// value, so callers can emit current_mode_update only on change.
func (s *acpSession) swapModeID(id string) (old string) {
	s.mu.Lock()
	old = s.modeID
	s.modeID = id
	s.mu.Unlock()
	return old
}

// currentModeID returns the mode last reported to the client.
func (s *acpSession) currentModeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modeID == "" {
		return sessionModeNormal
	}
	return s.modeID
}

func (s *acpSession) setGoalDraftMode(on bool) {
	s.mu.Lock()
	s.goalDraftMode = on
	s.mu.Unlock()
}

func (s *acpSession) takeGoalDraftMode() bool {
	s.mu.Lock()
	on := s.goalDraftMode
	s.goalDraftMode = false
	s.mu.Unlock()
	return on
}

func (s *acpSession) isGoalDraftMode() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.goalDraftMode
}

func (s *acpSession) setToolApprovalMode(mode string) {
	s.mu.Lock()
	s.toolApprovalMode = normalizeACPToolApprovalMode(mode)
	s.mu.Unlock()
}

func (s *acpSession) swapToolApprovalMode(mode string) (old string) {
	mode = normalizeACPToolApprovalMode(mode)
	s.mu.Lock()
	old = normalizeACPToolApprovalMode(s.toolApprovalMode)
	s.toolApprovalMode = mode
	s.mu.Unlock()
	return old
}

func (s *acpSession) saveMetaIfPresent() {
	s.mu.Lock()
	path := s.metaPath
	meta := s.metaLocked()
	s.mu.Unlock()
	if path != "" {
		_ = saveACPMeta(path, meta)
	}
}

// currentCtrl returns the session's controller under mu. rebuildSession swaps
// ctrl while holding mu, so any read of the field outside mu races with a
// concurrent config rebuild; always go through this accessor unless mu is
// already held.
func (s *acpSession) currentCtrl() acpController {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctrl
}

// initialize advertises the agent's capability set: persisted load plus ACP v1
// list/resume/close/delete lifecycle helpers, prompts carrying inline resource
// text (embeddedContext) but not image/audio, and stdio / Streamable HTTP MCP
// (no legacy sse).
func (s *service) initialize(_ context.Context, raw json.RawMessage) (any, error) {
	var p InitializeParams
	if len(raw) > 0 && json.Unmarshal(raw, &p) == nil {
		s.setClientCapabilities(p.ClientCapabilities)
	}
	return InitializeResult{
		ProtocolVersion: ProtocolVersion,
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			SessionCapabilities: SessionCapabilities{
				List:   &EmptyCapability{},
				Resume: &EmptyCapability{},
				Close:  &EmptyCapability{},
				Delete: &EmptyCapability{},
			},
			PromptCapabilities: PromptCapabilities{
				Image:           false,
				Audio:           false,
				EmbeddedContext: true,
			},
			MCPCapabilities: MCPCapabilities{HTTP: true, SSE: false},
			Meta: map[string]any{
				"deepzora.com": DeepzoraExtensionCapabilities{
					MCPInteraction: &MCPInteractionCapability{Supported: true, SchemaVersion: 1, Method: mcpInteractionMethod},
					SessionSteer:   &SessionSteerCapability{Method: sessionSteerMethod},
					SessionInbox: &SessionInboxCapability{
						SchemaVersion: sessionInboxSchemaVersion,
						Methods: map[string]string{
							"enqueue":   sessionInboxEnqueueMethod,
							"list":      sessionInboxListMethod,
							"get":       sessionInboxGetMethod,
							"update":    sessionInboxUpdateMethod,
							"delete":    sessionInboxDeleteMethod,
							"move":      sessionInboxMoveMethod,
							"setPaused": sessionInboxPauseMethod,
							"retry":     sessionInboxRetryMethod,
							"refresh":   sessionInboxRefreshMethod,
						},
					},
					SessionReloadExtensions: &SessionReloadExtensionsCapability{Method: sessionReloadExtensionsMethod},
					ExtensionSurface:        &ExtensionSurfaceCapability{Supported: true, SchemaVersion: deepzoraExtensionSurfaceSchemaVersion},
				},
				sessionStatusMethod:       DeepzoraSchemaCapability{SchemaVersion: deepzoraStatusSchemaVersion},
				sessionStatusUpdateMethod: DeepzoraSchemaCapability{SchemaVersion: deepzoraStatusSchemaVersion},
			},
		},
		AgentInfo:   Implementation{Name: s.info.Name, Version: s.info.Version},
		AuthMethods: []AuthMethod{deepzoraSetupAuthMethod()},
	}, nil
}

func deepzoraSetupAuthMethod() AuthMethod {
	return AuthMethod{
		ID:          "deepzora-setup",
		Name:        "Deepzora setup",
		Description: "Configure Deepzora providers and credentials in a terminal",
		Type:        "terminal",
		Args:        []string{"setup"},
	}
}

func (s *service) authenticate(_ context.Context, raw json.RawMessage) (any, error) {
	var p AuthenticateParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "authenticate: " + err.Error()}
	}
	if strings.TrimSpace(p.MethodID) != deepzoraSetupAuthMethod().ID {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "authenticate: unknown methodId " + p.MethodID}
	}
	return AuthenticateResult{}, nil
}

// sessionNew opens a session: it mints an id, builds the session's sink bound to
// that id, asks the Factory to assemble the controller, switches the controller
// to interactive approval (so tool gates surface as ApprovalRequest events the
// sink forwards), and registers it.
func (s *service) sessionNew(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionNewParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "session/new: " + err.Error()}
		}
	}
	cwd, err := s.resolveSessionCwd(p.Cwd, "")
	if err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/new: " + err.Error()}
	}
	mcpServers, err := mcpSpecs(p.MCPServers, cwd)
	if err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/new: " + err.Error()}
	}
	cfgState, err := s.sessionConfigState(ctx, SessionConfigStateParams{Cwd: cwd})
	if err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: "session/new: " + err.Error()}
	}
	cfgState = withToolApprovalConfig(cfgState, control.ToolApprovalWorkspaceWrite)
	runtimeState, err := s.sessionRuntimeState(ctx, SessionRuntimeStateParams{
		Cwd: cwd, Model: cfgState.Model,
	})
	if err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: "session/new: " + err.Error()}
	}

	id, err := newSessionID()
	if err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: "session/new: " + err.Error()}
	}

	sink := newUpdateSink(s.conn, id)
	sink.bindCwd(cwd)
	sink.bindExtensionSurface(s.extensionSurfaceSupported())
	sessionParams := SessionParams{
		Cwd:            cwd,
		MCPServers:     mcpServers,
		Sink:           sink,
		Model:          cfgState.Model,
		EffortOverride: cloneStringPtr(cfgState.EffortOverride),
	}
	s.bindSessionClients(id, &sessionParams)
	ctrl, err := s.factory.NewSession(ctx, sessionParams)
	if err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: "session/new: " + err.Error()}
	}
	ctrl.EnableInteractiveApproval()
	// The session metadata and advertised selector both start in workspace-write.
	// Apply the same preset to the controller before admitting the first turn so
	// a later same-value reconciliation cannot look like a permission change and
	// cancel work that was already admitted under the advertised boundary.
	ctrl.SetToolApprovalMode(control.ToolApprovalWorkspaceWrite)
	sink.bindControllerPrompts(ctrl, sessionParams.MCPInteractions)

	now := time.Now().UTC()
	sess := &acpSession{
		id:               id,
		ctrl:             ctrl,
		sink:             sink,
		cwd:              cwd,
		mcpServers:       clonePluginSpecs(mcpServers),
		model:            cfgState.Model,
		effortOverride:   cloneStringPtr(cfgState.EffortOverride),
		toolApprovalMode: control.ToolApprovalWorkspaceWrite,
		runtimeState:     runtimeState,
		status:           newStatusTelemetry(),
		modeID:           sessionModeNormal,
		createdAt:        now,
		updatedAt:        now,
	}
	s.bindStatusEvents(sess)
	if ctrl.SessionService() != nil {
		if _, err := ctrl.BindFreshSession(ctx, id); err != nil {
			ctrl.Close()
			return nil, &RPCError{Code: ErrInternal, Message: "session/new: " + err.Error()}
		}
		sess.metaPath = sessionMetadataPath(s.sessionDir(), id)
		if err := saveACPMeta(sess.metaPath, sess.meta()); err != nil {
			ctrl.Close()
			return nil, &RPCError{Code: ErrInternal, Message: "session/new: " + err.Error()}
		}
	} else if ctrl.SessionDir() != "" {
		ctrl.Close()
		return nil, &RPCError{Code: ErrInternal, Message: "session/new: session service is unavailable"}
	}

	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	// Fold in the live controller's extension catalog so plugin/... models
	// are discoverable from the very first session/new result.
	cfgState = enrichStateWithExtensionModels(cfgState, ctrl.ProviderCatalog())
	return afterResponse{
		result: SessionNewResult{
			SessionID:     id,
			Modes:         sessionModesState(sessionModeNormal),
			ConfigOptions: cfgState.ConfigOptions,
		},
		after: func() { s.sendAvailableCommands(sess) },
	}, nil
}

// Session modes exposed over ACP describe how the agent advances the task.
// Tool approval is an independent config option.
const (
	sessionModeNormal = "normal"
	sessionModePlan   = "plan"
	sessionModeGoal   = "goal"
)

func sessionModesState(current string) *SessionModeState {
	return &SessionModeState{
		CurrentModeID: current,
		AvailableModes: []SessionMode{
			{ID: sessionModeNormal, Name: "Normal", Description: "Work directly and pause when user input is required"},
			{ID: sessionModePlan, Name: "Plan", Description: "Research and propose a plan before making changes"},
			{ID: sessionModeGoal, Name: "Goal", Description: "Keep advancing the next prompt as a goal until complete or blocked"},
		},
	}
}

// sessionSetMode switches the session's operating mode and confirms it with a
// current_mode_update, per the ACP session-mode contract.
func (s *service) sessionSetMode(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionSetModeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/set_mode: " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/set_mode: unknown session " + p.SessionID}
	}
	sess.stateChangeMu.Lock()
	defer sess.stateChangeMu.Unlock()
	ctrl := sess.currentCtrl()
	nextMode, rpcErr := applyACPSessionMode(ctrl, p.ModeID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	// Entering Goal mode only arms a draft when no lifecycle exists. A restored,
	// blocked, paused, or disarmed Goal must retain its complete objective so the
	// user's next prompt can authorize recovery instead of replacing it.
	sess.setGoalDraftMode(selectedGoalDraftMode(nextMode, ctrl.Goal()))

	if sess.swapModeID(nextMode) != nextMode {
		sess.sink.send(currentModeUpdate{SessionUpdate: "current_mode_update", CurrentModeID: nextMode})
	}
	sess.saveMetaIfPresent()
	return SessionSetModeResult{}, nil
}

// emitModeDrift reports controller-side mode flips (plan mode auto-exits when
// a plan is approved, a config rebuild resets switches) as current_mode_update
// so the client's mode picker stays truthful.
func (s *service) emitModeDrift(sess *acpSession) {
	// Hold stateChangeMu across the controller read and the session-state swap:
	// a session/set_mode completing between them (it holds this lock) would
	// otherwise be read back as drift, roll the session's modeID and metadata
	// back to the pre-selection value, and make the next rebuild re-apply that
	// stale mode to the replacement controller.
	sess.stateChangeMu.Lock()
	defer sess.stateChangeMu.Unlock()
	ctrl := sess.currentCtrl()
	current := sessionModeNormal
	switch {
	case ctrl.PlanMode():
		current = sessionModePlan
	case ctrl.GoalStatus() == control.GoalStatusRunning || sess.isGoalDraftMode():
		current = sessionModeGoal
	}
	if sess.swapModeID(current) != current {
		sess.sink.send(currentModeUpdate{SessionUpdate: "current_mode_update", CurrentModeID: current})
		sess.saveMetaIfPresent()
	}
}

func (s *service) emitToolApprovalDrift(ctx context.Context, sess *acpSession) {
	// Same contract as emitModeDrift: serialize with switchSessionToolApproval
	// and rebuilds so a user selection landing between the controller read and
	// the swap below is never reverted.
	sess.stateChangeMu.Lock()
	defer sess.stateChangeMu.Unlock()
	current := normalizeACPToolApprovalMode(sess.currentCtrl().ToolApprovalMode())
	if sess.swapToolApprovalMode(current) == current {
		return
	}
	if cfgState, err := s.configStateForSession(ctx, sess); err == nil {
		sess.sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: cfgState.ConfigOptions})
	}
	sess.saveMetaIfPresent()
}

// sessionLoad resumes a previously-saved session by id: it builds a controller
// (rooted at the requested cwd), seeds it from the on-disk transcript, replays
// the conversation to the client as session/update notifications, and registers
// it for subsequent prompts. A session already live in this process is replayed
// from memory without rebuilding.
func (s *service) sessionLoad(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionLoadParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/load: " + err.Error()}
	}
	cfgState, err := s.openExistingSession(ctx, "session/load", p.SessionID, p.Cwd, p.MCPServers, true)
	if err != nil {
		return nil, err
	}
	return afterResponse{
		result: SessionLoadResult{Modes: s.sessionModesFor(p.SessionID), ConfigOptions: cfgState.ConfigOptions},
		after:  func() { s.sendSessionProjection(s.session(p.SessionID)) },
	}, nil
}

// sessionModesFor reports the modes state for a just-opened session. A live
// session keeps its current normal/plan/goal selection, so load/resume must not
// reset a reconnecting client's mode picker to normal.
func (s *service) sessionModesFor(id string) *SessionModeState {
	if sess := s.session(id); sess != nil {
		return sessionModesState(sess.currentModeID())
	}
	return sessionModesState(sessionModeNormal)
}

// sessionResume restores a previously-saved session without replaying its
// conversation history to the client.
func (s *service) sessionResume(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionResumeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/resume: " + err.Error()}
	}
	cfgState, err := s.openExistingSession(ctx, "session/resume", p.SessionID, p.Cwd, p.MCPServers, false)
	if err != nil {
		return nil, err
	}
	return afterResponse{
		result: SessionResumeResult{Modes: s.sessionModesFor(p.SessionID), ConfigOptions: cfgState.ConfigOptions},
		after:  func() { s.sendSessionProjection(s.session(p.SessionID)) },
	}, nil
}

// sendSessionProjection publishes current host-owned state after load/resume.
// History replay is presentation data and may contain legacy todo tool cards;
// the committed runtime snapshot is the only source of the current ACP plan.
func (s *service) sendSessionProjection(sess *acpSession) {
	if sess == nil {
		return
	}
	s.sendAvailableCommands(sess)
	reader, ok := sess.currentCtrl().(control.RuntimeStateReader)
	if !ok {
		return
	}
	snapshot := reader.RuntimeStateSnapshot()
	sess.sink.send(planUpdate{SessionUpdate: "plan", Entries: planEntriesFromTodos(snapshot.Todos)})
}

func (s *service) openExistingSession(ctx context.Context, method, id, cwdParam string, servers []MCPServerSpec, replay bool) (SessionConfigState, error) {
	if err := validateSessionID(method, id); err != nil {
		return SessionConfigState{}, err
	}
	cwd, err := s.resolveSessionCwd(cwdParam, id)
	if err != nil {
		return SessionConfigState{}, &RPCError{Code: ErrInvalidParams, Message: method + ": " + err.Error()}
	}
	mcpServers, err := mcpSpecs(servers, cwd)
	if err != nil {
		return SessionConfigState{}, &RPCError{Code: ErrInvalidParams, Message: method + ": " + err.Error()}
	}

	if sess := s.session(id); sess != nil {

		if replay {
			ctrl := sess.currentCtrl()
			replaySink := newUpdateSink(s.conn, id)
			replaySink.bindCwd(sess.cwd)
			replaySink.replay(ctrl.History())
		}
		cfgState, err := s.configStateForSession(ctx, sess)
		if err != nil {
			return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + err.Error()}
		}
		return cfgState, nil
	}

	var saved acpSessionMeta
	persistedPath := ""
	if dir := s.sessionDir(); dir != "" {
		persistedPath = sessionMetadataPath(dir, id)

		meta, _, metaErr := loadACPMeta(persistedPath)
		if metaErr != nil {
			return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + metaErr.Error()}
		}
		saved = meta
	}
	cfgParams := SessionConfigStateParams{
		Cwd:            cwd,
		Model:          saved.Model,
		EffortOverride: cloneStringPtr(saved.EffortOverride),
	}
	cfgState, err := s.sessionConfigState(ctx, cfgParams)
	if err != nil && (strings.TrimSpace(saved.Model) != "" || saved.EffortOverride != nil) {
		cfgState, err = s.sessionConfigState(ctx, SessionConfigStateParams{Cwd: cwd})
	}
	if err != nil {
		return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + err.Error()}
	}
	runtimeState, err := s.sessionRuntimeState(ctx, SessionRuntimeStateParams{
		Cwd: cwd, Model: cfgState.Model,
	})
	if err != nil {
		return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + err.Error()}
	}

	sink := newUpdateSink(s.conn, id)
	sink.bindCwd(cwd)
	sink.bindExtensionSurface(s.extensionSurfaceSupported())
	sessionParams := SessionParams{
		Cwd:            cwd,
		MCPServers:     mcpServers,
		Sink:           sink,
		Model:          cfgState.Model,
		EffortOverride: cloneStringPtr(cfgState.EffortOverride),
	}
	s.bindSessionClients(id, &sessionParams)
	ctrl, err := s.factory.NewSession(ctx, sessionParams)
	if err != nil {
		return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + err.Error()}
	}
	ctrl.EnableInteractiveApproval()
	sink.bindControllerPrompts(ctrl, sessionParams.MCPInteractions)

	path := persistedPath
	canonicalExists, statErr := canonicalSessionExists(ctx, ctrl, id)
	if statErr != nil {
		ctrl.Close()
		return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + statErr.Error()}
	}
	if canonicalExists {
		if err := openCanonicalSession(ctx, ctrl, id, method); err != nil {
			ctrl.Close()
			return SessionConfigState{}, err
		}
	} else {
		ctrl.Close()
		return SessionConfigState{}, &RPCError{Code: ErrInvalidParams, Message: method + ": unknown session " + id}
	}
	toolApprovalMode := normalizeACPToolApprovalMode(saved.ToolApprovalMode)
	if strings.TrimSpace(saved.ToolApprovalMode) == "" {
		toolApprovalMode = control.ToolApprovalWorkspaceWrite
	}
	ctrl.SetToolApprovalMode(toolApprovalMode)
	modeID, goalDraftMode := applyLoadedACPMode(ctrl, saved.CollaborationMode)

	meta := metadataForLoadedSession(path, id, cwd, ctrl.History())
	meta.Model = cfgState.Model
	meta.EffortOverride = cloneStringPtr(cfgState.EffortOverride)
	meta.ToolApprovalMode = toolApprovalMode
	meta.CollaborationMode = modeID
	cfgState = withToolApprovalConfig(cfgState, toolApprovalMode)
	sess := &acpSession{
		id:               id,
		ctrl:             ctrl,
		sink:             sink,
		metaPath:         path,
		cwd:              meta.Cwd,
		mcpServers:       clonePluginSpecs(mcpServers),
		model:            cfgState.Model,
		effortOverride:   cloneStringPtr(cfgState.EffortOverride),
		toolApprovalMode: toolApprovalMode,
		runtimeState:     runtimeState,
		status:           restoreStatusTelemetry(saved.Status),
		modeID:           modeID,
		goalDraftMode:    goalDraftMode,
		title:            meta.Title,
		createdAt:        meta.CreatedAt,
		updatedAt:        meta.UpdatedAt,
	}
	s.bindStatusEvents(sess)
	if path != "" {
		if err := saveACPMeta(path, sess.meta()); err != nil {
			ctrl.Close()
			return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: method + ": " + err.Error()}
		}
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	if replay {
		sink.replay(ctrl.History())
	}
	return enrichStateWithExtensionModels(cfgState, ctrl.ProviderCatalog()), nil
}

// sessionPrompt runs one turn. It flattens the prompt blocks to text and runs the
// session's controller synchronously under a per-turn cancelable context (so
// session/cancel can stop it), then reports why the turn ended. The controller
// streams the turn's events to the session's sink as it runs.
func (s *service) sessionPrompt(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionPromptParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/prompt: " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/prompt: unknown session " + p.SessionID}
	}
	text := FlattenPrompt(p.Prompt)
	rawText := text
	invokedSkill := ""
	if text == "" && p.Action != control.ProtocolRecoveryAction {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/prompt: empty prompt"}
	}
	protocolRecovery := p.Action == control.ProtocolRecoveryAction
	if protocolRecovery && p.RecoveryID == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/prompt: missing recoveryId"}
	}
	if p.Action != "" && !protocolRecovery {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/prompt: unsupported action " + p.Action}
	}
	if p.Action == "" {
		if id, guidance, ok := control.ParseProtocolRecoveryCommand(text); ok {
			protocolRecovery = true
			p.RecoveryID = id
			text = guidance
		} else {
			text, invokedSkill = s.resolveSlashPrompt(ctx, sess, text)
		}
	}

	runCtx, cancel, ok := sess.begin(ctx)
	if !ok {
		return nil, &RPCError{Code: ErrInvalidRequest, Message: "session/prompt: session already has an active prompt"}
	}
	defer func() {
		sess.sink.clearTurnContext()
		s.finishTurn(ctx, sess)
		cancel()
	}()
	statusStarted := false
	if rpcErr := prepareACPGoalPrompt(sess, text); rpcErr != nil {
		return nil, rpcErr
	}
	beginTurn := func() {
		if sess.status == nil {
			sess.status = newStatusTelemetry()
		}
		sess.status.beginTurn()
		s.publishStatus(sess, "phase")
		sess.sink.setTurnContext(runCtx)
		statusStarted = true
	}
	var runErr error
	if protocolRecovery {
		if runner, ok := sess.ctrl.(interface {
			RunProtocolRecoveryWithAdmission(context.Context, string, string, func()) error
		}); ok {
			runErr = runner.RunProtocolRecoveryWithAdmission(runCtx, p.RecoveryID, text, beginTurn)
		} else {
			return nil, &RPCError{Code: ErrInvalidRequest, Message: "protocol recovery is unsupported by this controller"}
		}
	} else {
		beginTurn()
		runErr = sess.ctrl.RunTurnWithRaw(runCtx, text, rawText, invokedSkill)
	}
	if errors.Is(runErr, agent.ErrProtocolRecoveryUnavailable) && !statusStarted {
		return nil, &RPCError{Code: ErrInvalidRequest, Message: "session/prompt: protocol recovery is unavailable or stale"}
	}

	runErr = drainACPInbox(runCtx, sess.ctrl, runErr)
	cancelled := runCtx.Err() != nil

	statusEvent := sess.status.finishTurn(
		runErr,
		cancelled,
		sess.currentCtrl().GoalStatus(),
		finalAssistantSummary(sess.currentCtrl()),
	)
	s.publishStatus(sess, statusEvent)
	// Persist after status finalization (best-effort) so reconnect recovers both
	// the transcript and the same sequence/usage/outcome snapshot.
	sess.persistAfterTurn(text)

	stop, warning, promptErr := promptStopReason(runErr, cancelled, p.SessionID)
	if promptErr != nil {
		return nil, promptErr
	}
	if warning != "" {
		// The TUI keeps completed work for deliberate run boundaries; mirror
		// that for ACP and tell clients how the successful turn ended.
		sess.sink.Emit(promptPauseNotice(runErr, warning))
	}
	return SessionPromptResult{StopReason: stop}, nil
}

// sessionSteer durably persists guidance then attempts mid-turn admission.
// Parameter/session errors remain RPC errors; busy rejection returns a
// disposition so clients can keep the durable follow-up.
func (s *service) sessionSteer(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionSteerParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: sessionSteerMethod + ": " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: sessionSteerMethod + ": unknown session " + p.SessionID}
	}
	text := FlattenPrompt(p.Prompt)
	if text == "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: sessionSteerMethod + ": empty prompt"}
	}
	api, err := s.sessionAPI(ctx, sess)
	if err != nil {
		return nil, err
	}
	if !api.Running() {
		return nil, &RPCError{Code: ErrInvalidRequest, Message: sessionSteerMethod + ": session has no active prompt"}
	}
	rec, err := api.TryEnqueueAndSteer(control.InboxRequest{
		Intent: sessioninbox.IntentSteer, Display: text, Raw: text, Submit: text, Source: "acp",
	})
	if err != nil {
		return nil, &RPCError{Code: ErrInvalidRequest, Message: sessionSteerMethod + ": " + err.Error()}
	}
	return SessionSteerResult{ItemID: rec.ItemID, Disposition: string(rec.Disposition)}, nil

}

// sessionReloadExtensions rebuilds a session's agent runtime in place —
// tools, skills, commands, hooks, MCP servers, and providers are re-discovered
// — while the session (transcript, approval grants, goal and recovery state)
// carries over via boot.Rebuild. It follows the same contract as a config
// switch: a turn or rebuild in flight coalesces exactly one queued reload,
// drained when the session goes idle; a failure keeps the old controller fully
// usable; the old controller's resources are released only after the swap.
func (s *service) sessionReloadExtensions(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionReloadExtensionsParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: sessionReloadExtensionsMethod + ": " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: sessionReloadExtensionsMethod + ": unknown session " + p.SessionID}
	}
	return s.reloadSessionExtensions(ctx, sess)
}

func (s *service) reloadSessionExtensions(ctx context.Context, sess *acpSession) (any, error) {
	rebuilder, ok := s.factory.(SessionRebuilder)
	if !ok {
		return nil, &RPCError{Code: ErrInvalidRequest, Message: sessionReloadExtensionsMethod + ": runtime reload is unavailable in this session"}
	}
	if !sess.stateChangeMu.TryLock() {
		// A config switch or reload is in maintenance: coalesce one reload
		// behind it; the maintenance owner's post-maintenance drain runs it
		// (mirrors the pendingConfig queue contract in rebuildSession).
		sess.mu.Lock()
		if sess.maintenanceDone != nil && !sess.deleted {
			sess.pendingReload = true
			sess.mu.Unlock()
			return SessionReloadExtensionsResult{Queued: true}, nil
		}
		sess.mu.Unlock()
		sess.stateChangeMu.Lock()
	}
	didMaintenance := false
	res, err := s.reloadSessionExtensionsLocked(ctx, sess, rebuilder, &didMaintenance)
	sess.stateChangeMu.Unlock()
	if didMaintenance {
		s.reportPendingSessionConfigError(ctx, sess, s.applyPendingSessionConfig(ctx, sess), "after maintenance")
		s.drainPendingReload(ctx, sess)
	}
	return res, err
}

// reloadSessionExtensionsLocked is reloadSessionExtensions' body; callers hold
// stateChangeMu. The busy/queue checks and the publish/close ordering mirror
// rebuildSessionLocked, but the build itself goes through the factory's
// boot.Rebuild path instead of NewSession + manual migration.
func (s *service) reloadSessionExtensionsLocked(ctx context.Context, sess *acpSession, rebuilder SessionRebuilder, didMaintenance *bool) (any, error) {
	sess.mu.Lock()
	if sess.deleted {
		sess.mu.Unlock()
		return nil, &RPCError{Code: ErrInvalidRequest, Message: sessionReloadExtensionsMethod + ": session is deleted"}
	}
	status := sess.ctrl.RuntimeStateSnapshot()
	if status.PendingPrompt {
		sess.mu.Unlock()
		return nil, sessionConfigActiveWorkError("answer pending prompts before reloading the runtime")
	}
	if !sess.running && !status.Running && status.BackgroundJobs > 0 {
		sess.mu.Unlock()
		return nil, sessionConfigActiveWorkError("stop background jobs before reloading the runtime")
	}
	if sess.running || status.Running || sess.maintenanceDone != nil {
		// Busy: coalesce exactly one reload; finishTurn (or the maintenance
		// owner's post-maintenance drain) runs it once the session is idle.
		sess.pendingReload = true
		sess.mu.Unlock()
		return SessionReloadExtensionsResult{Queued: true}, nil
	}
	// Claim the queued reload and raise maintenance in the same critical
	// section (mirrors rebuildSessionLocked): begin must never observe an
	// idle session between the two.
	sess.pendingReload = false
	cur := sess.ctrl
	sink := sess.sink
	mcpServers := clonePluginSpecs(sess.mcpServers)
	cwd := sess.cwd
	model := sess.model
	effortOverride := cloneStringPtr(sess.effortOverride)
	maintenanceDone := make(chan struct{})
	sess.maintenanceDone = maintenanceDone
	*didMaintenance = true
	sess.mu.Unlock()
	defer func() {
		sess.finishMaintenance(maintenanceDone)
	}()

	if err := cur.Snapshot(); err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: sessionReloadExtensionsMethod + ": snapshot before reload: " + err.Error()}
	}
	old, ok := cur.(*control.Controller)
	if !ok {
		return nil, &RPCError{Code: ErrInternal, Message: sessionReloadExtensionsMethod + ": session controller does not support rebuild"}
	}
	rebuildParams := SessionParams{
		Cwd:            cwd,
		MCPServers:     mcpServers,
		Sink:           sink,
		Model:          model,
		EffortOverride: effortOverride,
	}
	s.bindSessionClients(sess.id, &rebuildParams)
	newCtrl, err := rebuilder.RebuildSession(ctx, rebuildParams, old)
	if err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: sessionReloadExtensionsMethod + ": " + err.Error()}
	}
	newCtrl.EnableInteractiveApproval()
	// Config on disk may have changed the effective planner/sandbox posture;
	// recompute the status snapshot from the same resolved inputs.
	runtimeState, err := s.sessionRuntimeState(ctx, SessionRuntimeStateParams{
		Cwd: cwd, Model: model,
	})
	if err != nil {
		newCtrl.ReleaseResources()
		return nil, &RPCError{Code: ErrInternal, Message: sessionReloadExtensionsMethod + ": runtime state: " + err.Error()}
	}
	// Persist before publishing the replacement. If this fails, the outgoing
	// controller and transcript still agree and remain fully usable (mirrors
	// the config switch).
	if err := newCtrl.Snapshot(); err != nil {
		newCtrl.ReleaseResources()
		return nil, &RPCError{Code: ErrInternal, Message: sessionReloadExtensionsMethod + ": snapshot after reload: " + err.Error()}
	}

	sess.mu.Lock()
	if sess.deleted {
		sess.mu.Unlock()
		newCtrl.ReleaseResources()
		return nil, &RPCError{Code: ErrInvalidRequest, Message: sessionReloadExtensionsMethod + ": session is deleted"}
	}
	if sess.ctrl != cur {
		sess.mu.Unlock()
		newCtrl.ReleaseResources()
		return nil, sessionConfigActiveWorkError("session changed while reloading; retry")
	}
	oldCtrl, _ := cur.(*control.Controller)
	if err := control.ActivateControllerReplacement(oldCtrl, newCtrl); err != nil {
		sess.mu.Unlock()
		newCtrl.ReleaseResources()
		return nil, &RPCError{Code: ErrInternal, Message: sessionReloadExtensionsMethod + ": activate replacement: " + err.Error()}
	}
	sess.ctrl = newCtrl
	sess.runtimeState = runtimeState
	if sess.metaPath != "" {
		_ = saveACPMeta(sess.metaPath, sess.metaLocked())
	}
	sess.mu.Unlock()
	newCtrl.ActivateGoalDriverAfterRebuild()
	sink.bindControllerPrompts(newCtrl, rebuildParams.MCPInteractions)

	// Release the outgoing controller only after the swap published the
	// replacement. ReleaseResources (not Close): the session logically
	// continues, so SessionEnd hooks must not fire — mirrors the config
	// switch.
	cur.ReleaseResources()
	// Clients see refreshed plugin commands without waiting for the next turn.
	s.sendAvailableCommands(sess)
	return SessionReloadExtensionsResult{}, nil
}

// drainPendingReload runs the coalesced reloadExtensions request once the
// session is idle. Called from finishTurn and after a config switch's or a
// reload's own maintenance completes; callers must NOT hold stateChangeMu
// (the reload re-acquires it).
func (s *service) drainPendingReload(ctx context.Context, sess *acpSession) {
	if _, ok := s.factory.(SessionRebuilder); !ok {
		return
	}
	sess.mu.Lock()
	if !sess.pendingReload || sess.deleted || sess.running || sess.maintenanceDone != nil || len(sess.pendingConfig) > 0 {
		sess.mu.Unlock()
		return
	}
	sess.mu.Unlock()
	if _, err := s.reloadSessionExtensions(ctx, sess); err != nil {
		s.reportPendingSessionConfigError(ctx, sess, err, "after queued reload")
	}
}

// finishTurn reconciles controller-side drift and drains any config switch
// queued during the turn. Drift must be reconciled before finish() exposes
// the session as idle: a concurrent config switch races on sess.running, and
// if it wins that race while modeID/toolApprovalMode are still stale (a
// slash command or plan/goal completion changed them inside the turn), it
// rebuilds the replacement controller from the outgoing state instead of the
// one this turn actually ended in.
func (s *service) finishTurn(ctx context.Context, sess *acpSession) {
	s.emitModeDrift(sess)
	s.emitToolApprovalDrift(ctx, sess)
	sess.finish()
	s.reportPendingSessionConfigError(ctx, sess, s.applyPendingSessionConfig(ctx, sess), "after turn")
	// A reloadExtensions request queued during the turn runs now that the
	// session may be idle; the drain re-checks busy state.
	s.drainPendingReload(ctx, sess)
	// Re-check after a rebuild in case the replacement normalized state.
	s.emitModeDrift(sess)
	s.emitToolApprovalDrift(ctx, sess)
}

// sessionSetConfigOption applies ACP's generic session-level selectors for
// model, reasoning effort, work mode, and tool approval.
func (s *service) sessionSetConfigOption(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SetSessionConfigOptionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/set_config_option: " + err.Error()}
	}
	sess := s.session(p.SessionID)
	if sess == nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/set_config_option: unknown session " + p.SessionID}
	}
	cfgState, err := s.configStateForSession(ctx, sess)
	if err != nil {
		return nil, &RPCError{Code: ErrInternal, Message: "session/set_config_option: " + err.Error()}
	}
	option, ok := findConfigOption(cfgState.ConfigOptions, p.ConfigID)
	if !ok {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/set_config_option: unknown config option " + p.ConfigID}
	}
	if !configOptionHasValue(option, p.Value) {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/set_config_option: invalid value " + p.Value + " for " + option.ID}
	}

	var next SessionConfigState
	switch configOptionCategory(option) {
	case "model":
		next, err = s.switchSessionModel(ctx, sess, p.Value)
	case "thought_level":
		next, err = s.switchSessionEffort(ctx, sess, p.Value)
	case "tool_approval":
		next, err = s.switchSessionToolApproval(ctx, sess, p.Value)
	default:
		err = &RPCError{Code: ErrInvalidParams, Message: "session/set_config_option: unsupported config option " + option.ID}
	}
	if err != nil {
		return nil, err
	}
	return SetSessionConfigOptionResult{ConfigOptions: next.ConfigOptions}, nil
}

// resolveSessionConfigDeltas resolves deltas against the session's current
// config baseline. Calling this fresh at every apply — instead of reusing a
// snapshot taken when a change was first requested — is what keeps a queued
// delta for one axis from clobbering another axis that rebuilt in between.
func (s *service) resolveSessionConfigDeltas(ctx context.Context, sess *acpSession, deltas []sessionConfigDelta) (SessionConfigState, error) {
	params := sess.configStateParams()
	for _, delta := range deltas {
		delta.applyTo(&params)
	}
	cfgState, err := s.sessionConfigState(ctx, params)
	if err != nil {
		return SessionConfigState{}, err
	}
	return withToolApprovalConfig(cfgState, sess.currentToolApprovalMode()), nil
}

func (s *service) switchSessionModel(ctx context.Context, sess *acpSession, modelID string) (SessionConfigState, error) {
	deltas := []sessionConfigDelta{{axis: "model", model: modelID}}
	return s.switchSessionConfig(ctx, sess, deltas)
}

func (s *service) switchSessionEffort(ctx context.Context, sess *acpSession, effort string) (SessionConfigState, error) {
	level := strings.TrimSpace(effort)
	if level == "auto" {
		level = ""
	}
	deltas := []sessionConfigDelta{{axis: "thought_level", effortOverride: &level}}
	return s.switchSessionConfig(ctx, sess, deltas)
}

func (s *service) switchSessionConfig(ctx context.Context, sess *acpSession, deltas []sessionConfigDelta) (SessionConfigState, error) {
	resolve := func() (SessionConfigState, error) {
		cfgState, err := s.resolveSessionConfigDeltas(ctx, sess, deltas)
		if err != nil {
			return SessionConfigState{}, &RPCError{Code: ErrInvalidParams, Message: "session/set_config_option: " + err.Error()}
		}
		if len(deltas) == 1 && deltas[0].axis == "model" && cfgState.Model == "" {
			return SessionConfigState{}, &RPCError{Code: ErrInvalidRequest, Message: "session/set_config_option: model switching is unavailable in this session"}
		}
		return cfgState, nil
	}

	if !sess.stateChangeMu.TryLock() {
		// Preserve the non-blocking queue contract while a rebuild is already in
		// maintenance. Resolve once for validation and the immediate client update;
		// the drain resolves the queued deltas again against live state.
		cfgState, err := resolve()
		if err != nil {
			return SessionConfigState{}, err
		}
		sess.mu.Lock()
		if sess.maintenanceDone != nil && !sess.deleted {
			for _, delta := range deltas {
				sess.pendingConfig = mergePendingConfig(sess.pendingConfig, delta)
			}
			sess.mu.Unlock()
			sess.sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: cfgState.ConfigOptions})
			return cfgState, nil
		}
		sess.mu.Unlock()
		sess.stateChangeMu.Lock()
	}

	// Always resolve inside the serialization domain. Even a successful TryLock
	// can follow a concurrent rebuild that completed after this request began.
	cfgState, err := resolve()
	if err != nil {
		sess.stateChangeMu.Unlock()
		return SessionConfigState{}, err
	}
	didMaintenance := false
	err = s.rebuildSessionLocked(ctx, sess, cfgState, deltas, &didMaintenance)
	sess.stateChangeMu.Unlock()
	if didMaintenance {
		pendingErr := s.applyPendingSessionConfig(ctx, sess)
		s.reportPendingSessionConfigError(ctx, sess, pendingErr, "after maintenance")
		// A reloadExtensions request queued behind this maintenance runs next.
		s.drainPendingReload(ctx, sess)
		// The pending drain completes before this request returns. Refresh the RPC
		// result so an older response cannot overwrite the newer config_option_update
		// with the pre-drain full snapshot on the client.
		if current, stateErr := s.configStateForSession(ctx, sess); stateErr == nil {
			cfgState = current
		}
	}
	if err != nil {
		return SessionConfigState{}, err
	}
	return cfgState, nil
}

func (s *service) switchSessionToolApproval(ctx context.Context, sess *acpSession, mode string) (SessionConfigState, error) {
	sess.stateChangeMu.Lock()
	defer sess.stateChangeMu.Unlock()
	mode = normalizeACPToolApprovalMode(mode)
	ctrl := sess.currentCtrl()
	ctrl.SetToolApprovalMode(mode)
	sess.setToolApprovalMode(mode)
	sess.saveMetaIfPresent()
	cfgState, err := s.configStateForSession(ctx, sess)
	if err != nil {
		return SessionConfigState{}, &RPCError{Code: ErrInternal, Message: "session/set_config_option: " + err.Error()}
	}
	sess.sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: cfgState.ConfigOptions})
	return cfgState, nil
}

func (s *service) rebuildSession(ctx context.Context, sess *acpSession, cfgState SessionConfigState, deltas []sessionConfigDelta) error {
	if !sess.stateChangeMu.TryLock() {
		// Preserve the existing queue contract: a config change arriving during
		// a controller build returns immediately and is applied after that
		// build. The queue keeps one delta per axis (last-write-wins within an
		// axis), so changes queued for different axes never clobber each other.
		// Collaboration/approval changes do not use this queue; they wait for
		// the swap and then update the replacement controller.
		sess.mu.Lock()
		if sess.maintenanceDone != nil && !sess.deleted {
			for _, delta := range deltas {
				sess.pendingConfig = mergePendingConfig(sess.pendingConfig, delta)
			}
			sess.mu.Unlock()
			sess.sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: cfgState.ConfigOptions})
			return nil
		}
		sess.mu.Unlock()
		sess.stateChangeMu.Lock()
	}
	didMaintenance := false
	err := s.rebuildSessionLocked(ctx, sess, cfgState, deltas, &didMaintenance)
	sess.stateChangeMu.Unlock()
	if didMaintenance {
		pendingErr := s.applyPendingSessionConfig(ctx, sess)
		s.reportPendingSessionConfigError(ctx, sess, pendingErr, "after maintenance")
		// A reloadExtensions request queued behind this maintenance runs next.
		s.drainPendingReload(ctx, sess)
	}
	return err
}

func (s *service) rebuildSessionLocked(ctx context.Context, sess *acpSession, cfgState SessionConfigState, deltas []sessionConfigDelta, didMaintenance *bool) (retErr error) {
	sess.mu.Lock()
	if sess.deleted {
		sess.mu.Unlock()
		return &RPCError{Code: ErrInvalidRequest, Message: "session config: session is deleted"}
	}
	status := sess.ctrl.RuntimeStateSnapshot()
	if status.PendingPrompt {
		sess.mu.Unlock()
		return sessionConfigActiveWorkError("answer pending prompts before switching config")
	}
	modelOnly := modelOnlyConfigDeltas(deltas)
	if !sess.running && !status.Running && status.BackgroundJobs > 0 && configBackgroundBlocked(sess.ctrl, modelOnly) {
		sess.mu.Unlock()
		return sessionConfigActiveWorkError("stop background jobs before switching config")
	}
	if sess.running || status.Running || sess.maintenanceDone != nil {
		for _, delta := range deltas {
			sess.pendingConfig = mergePendingConfig(sess.pendingConfig, delta)
		}
		sess.mu.Unlock()
		sess.sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: cfgState.ConfigOptions})
		return nil
	}
	// Claim this rebuild's axes from the queue in the same critical section
	// that raises maintenanceDone below: begin must never observe an idle
	// session between the two. Axes queued by other requests stay queued and
	// are drained by the post-maintenance apply.
	sess.pendingConfig = removePendingAxes(sess.pendingConfig, deltas)

	cur := sess.ctrl
	sink := sess.sink
	mcpServers := clonePluginSpecs(sess.mcpServers)
	cwd := sess.cwd
	modeID := normalizeACPCollaborationMode(sess.modeID)
	goalDraftMode := sess.goalDraftMode
	toolApprovalMode := normalizeACPToolApprovalMode(sess.toolApprovalMode)
	maintenanceDone := make(chan struct{})
	sess.maintenanceDone = maintenanceDone
	*didMaintenance = true
	sess.mu.Unlock()
	defer func() {
		sess.finishMaintenance(maintenanceDone)
	}()

	if err := cur.Snapshot(); err != nil {
		return &RPCError{Code: ErrInternal, Message: "session config: snapshot before switch: " + err.Error()}
	}
	carriedGoal := ""
	if cur.GoalStatus() == control.GoalStatusRunning {
		carriedGoal = cur.Goal()
	}

	rebuildParams := SessionParams{
		Cwd:            cwd,
		MCPServers:     mcpServers,
		Sink:           sink,
		Model:          cfgState.Model,
		EffortOverride: cloneStringPtr(cfgState.EffortOverride),
	}
	s.bindSessionClients(sess.id, &rebuildParams)
	newCtrl, err := s.buildConfigReplacement(ctx, cur, rebuildParams, modelOnly)
	if err != nil {
		return &RPCError{Code: ErrInternal, Message: "session config: " + err.Error()}
	}
	newCtrl.EnableInteractiveApproval()
	runtimeState, err := s.sessionRuntimeState(ctx, SessionRuntimeStateParams{
		Cwd: cwd, Model: cfgState.Model,
	})
	if err != nil {
		newCtrl.ReleaseResources()
		return &RPCError{Code: ErrInternal, Message: "session config: runtime state: " + err.Error()}
	}
	// Re-apply all three independent session axes. A controller rebuild must not
	// turn Plan into tool approval, drop a running Goal, or reset Ask/Auto/Yolo.
	newCtrl.SetToolApprovalMode(toolApprovalMode)
	switch modeID {
	case sessionModePlan:
		newCtrl.SetPlanMode(true)
	case sessionModeGoal:
		newCtrl.SetPlanMode(false)
		if carriedGoal != "" {
			newCtrl.SetGoal(carriedGoal)
		}
	default:
		newCtrl.SetPlanMode(false)
	}
	// InheritLifecycleFrom wires two concrete controllers' turn/hook state; it's a
	// construction concern, not part of the driving port. cur is always the
	// *control.Controller the factory built for this session, so this is safe.
	if rpcErr := inheritACPControllerLifecycle(newCtrl, cur); rpcErr != nil {
		newCtrl.ReleaseResources()
		return rpcErr
	}
	// Stage the selected model against the retained Runtime. Publication owns
	// the commit; a rejected candidate cannot rewrite the live model context.
	if _, _, bound := newCtrl.SessionBinding(); bound {
		if err := newCtrl.AdoptRebuiltModelContext(newCtrl.History()); err != nil {
			newCtrl.ReleaseResources()
			return &RPCError{Code: ErrInternal, Message: "session config: prepare current model context: " + err.Error()}
		}
	}
	if err := newCtrl.Snapshot(); err != nil {
		newCtrl.ReleaseResources()
		return &RPCError{Code: ErrInternal, Message: "session config: snapshot after switch: " + err.Error()}
	}

	sess.mu.Lock()
	if sess.deleted {
		sess.mu.Unlock()
		newCtrl.ReleaseResources()
		return &RPCError{Code: ErrInvalidRequest, Message: "session config: session is deleted"}
	}
	if sess.ctrl != cur {
		sess.mu.Unlock()
		newCtrl.ReleaseResources()
		return sessionConfigActiveWorkError("session changed while switching config; retry")
	}
	oldCtrl, _ := cur.(*control.Controller)
	if err := control.ActivateControllerReplacement(oldCtrl, newCtrl); err != nil {
		sess.mu.Unlock()
		newCtrl.ReleaseResources()
		return &RPCError{Code: ErrInternal, Message: "session config: activate replacement: " + err.Error()}
	}
	sess.ctrl = newCtrl
	sess.model = cfgState.Model
	sess.effortOverride = cloneStringPtr(cfgState.EffortOverride)
	sess.toolApprovalMode = toolApprovalMode
	sess.runtimeState = runtimeState
	sess.modeID = modeID
	sess.goalDraftMode = goalDraftMode
	if sess.metaPath != "" {
		_ = saveACPMeta(sess.metaPath, sess.metaLocked())
	}
	sess.mu.Unlock()
	newCtrl.ActivateGoalDriverAfterRebuild()
	sink.bindControllerPrompts(newCtrl, rebuildParams.MCPInteractions)

	cur.ReleaseResources()
	s.sendAvailableCommands(sess)
	sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: cfgState.ConfigOptions})
	return nil
}

func (s *service) applyPendingSessionConfig(ctx context.Context, sess *acpSession) error {
	var firstErr error
	for {
		if s.session(sess.id) != sess {
			return firstErr
		}
		// Claim the queue in the same serialization domain as explicit config
		// switches. Without this lock, a newer same-axis request can rebuild after
		// the clone below but before this apply starts, then the stale cloned delta
		// queues behind it and wins last instead of preserving request order.
		sess.stateChangeMu.Lock()
		didMaintenance := false
		sess.mu.Lock()
		if sess.deleted || len(sess.pendingConfig) == 0 {
			sess.mu.Unlock()
			sess.stateChangeMu.Unlock()
			return firstErr
		}
		deltas := clonePendingConfig(sess.pendingConfig)
		// Keep pendingConfig set while rebuilding: begin refuses new turns until
		// rebuildSession claims it together with raising maintenanceDone, so no
		// promptable instant is visible in between.
		sess.mu.Unlock()

		// Re-resolve against the session's current state rather than reusing
		// whatever baseline existed when each delta queued: another axis may have
		// finished rebuilding in the meantime, and replaying its old value here
		// would silently roll it back. All queued axes resolve into one state so a
		// single rebuild applies them together.
		cfgState, err := s.resolveSessionConfigDeltas(ctx, sess, deltas)
		if err != nil {
			sess.mu.Lock()
			if !sess.deleted && !sess.running && sess.maintenanceDone == nil {
				sess.pendingConfig = removePendingAxes(sess.pendingConfig, deltas)
			}
			sess.mu.Unlock()
			sess.stateChangeMu.Unlock()
			if firstErr != nil {
				s.reportPendingSessionConfigError(ctx, sess, err, "after failed maintenance")
				return firstErr
			}
			return err
		}

		err = s.rebuildSessionLocked(ctx, sess, cfgState, deltas, &didMaintenance)
		if err != nil && !didMaintenance {
			// Once this attempt failed nothing in flight is left to retry the
			// claimed axes, and begin refuses new turns while any are queued — drop
			// them so the session stays promptable. Once maintenance started, those
			// axes were already removed; anything queued now is a newer request and
			// must survive this failure.
			sess.mu.Lock()
			if !sess.deleted && !sess.running && sess.maintenanceDone == nil {
				sess.pendingConfig = removePendingAxes(sess.pendingConfig, deltas)
			}
			sess.mu.Unlock()
		}
		sess.stateChangeMu.Unlock()

		if err != nil {
			if firstErr == nil {
				firstErr = err
			} else {
				s.reportPendingSessionConfigError(ctx, sess, err, "after failed maintenance")
			}
			if !didMaintenance {
				return firstErr
			}
		}
		if !didMaintenance {
			return firstErr
		}
		// Requests can queue while NewSession/Snapshot runs. Iterate even when this
		// rebuild failed so their already-successful RPCs cannot leave the session
		// blocked. A loop keeps sustained config traffic from growing the call stack.
	}
}

func (s *service) reportPendingSessionConfigError(ctx context.Context, sess *acpSession, err error, when string) {
	if err == nil || sess == nil || sess.sink == nil {
		return
	}
	sess.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "session config switch failed " + when + ": " + err.Error()})
	// A queued request already announced its desired config to the client. Every
	// apply failure leaves the outgoing controller/config active, so always send
	// the live state back; otherwise snapshot/build/resolve failures leave the
	// picker claiming a switch that never happened.
	if current, stateErr := s.configStateForSession(ctx, sess); stateErr == nil {
		sess.sink.send(configOptionUpdate{SessionUpdate: "config_option_update", ConfigOptions: current.ConfigOptions})
	}
}

type activeSessionConfigWorkError struct {
	*RPCError
}

func (e *activeSessionConfigWorkError) Unwrap() error {
	return e.RPCError
}

func sessionConfigActiveWorkError(message string) error {
	return &activeSessionConfigWorkError{
		RPCError: &RPCError{Code: ErrInvalidRequest, Message: "session config: " + message},
	}
}

// sessionClose releases an active session. Unknown sessions are accepted as a
// no-op because closing is an idempotent resource cleanup request.
func (s *service) sessionClose(_ context.Context, raw json.RawMessage) (any, error) {
	var p SessionCloseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/close: " + err.Error()}
	}
	if err := validateSessionID("session/close", p.SessionID); err != nil {
		return nil, err
	}
	if sess := s.takeSession(p.SessionID); sess != nil {
		sess.abortAndWait()
		sess.ctrl.Close()
	}
	return SessionCloseResult{}, nil
}

// sessionList returns ACP sessions known to this process or persisted as ACP
// sidecars. It deliberately ignores ordinary CLI timestamp sessions.
func (s *service) sessionList(_ context.Context, raw json.RawMessage) (any, error) {
	var p SessionListParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &RPCError{Code: ErrInvalidParams, Message: "session/list: " + err.Error()}
		}
	}
	filterCwd := strings.TrimSpace(p.Cwd)
	if filterCwd != "" && !filepath.IsAbs(filterCwd) {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/list: cwd must be an absolute path"}
	}
	if strings.TrimSpace(p.Cursor) != "" {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/list: unsupported cursor"}
	}

	byID := map[string]SessionInfo{}
	if dir := s.sessionDir(); dir != "" {
		metas, err := listACPMetas(dir)
		if err != nil {
			return nil, &RPCError{Code: ErrInternal, Message: "session/list: " + err.Error()}
		}
		for _, meta := range metas {
			info := meta.info(nil)
			if sessionInfoMatchesCwd(info, filterCwd) {
				byID[info.SessionID] = info
			}
		}
	}
	for _, sess := range s.liveSessions() {
		info := sess.info()
		if sessionInfoMatchesCwd(info, filterCwd) {
			byID[info.SessionID] = info
		}
	}

	sessions := make([]SessionInfo, 0, len(byID))
	for _, info := range byID {
		sessions = append(sessions, info)
	}
	sort.Slice(sessions, func(i, j int) bool {
		ti := parseSessionUpdatedAt(sessions[i].UpdatedAt)
		tj := parseSessionUpdatedAt(sessions[j].UpdatedAt)
		if ti.Equal(tj) {
			return sessions[i].SessionID < sessions[j].SessionID
		}
		return ti.After(tj)
	})
	return SessionListResult{Sessions: sessions}, nil
}

// sessionDelete deletes the current session identity and its ACP metadata.
func (s *service) sessionDelete(ctx context.Context, raw json.RawMessage) (any, error) {
	var p SessionDeleteParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "session/delete: " + err.Error()}
	}
	if err := validateSessionID("session/delete", p.SessionID); err != nil {
		return nil, err
	}
	sess := s.takeSession(p.SessionID)
	var ctrl *control.Controller
	if sess != nil {
		sess.deleteAndWait()
		ctrl, _ = sess.currentCtrl().(*control.Controller)
	} else if s.sessionDir() != "" {
		cwd, err := s.resolveSessionCwd("", p.SessionID)
		if err != nil {
			return nil, &RPCError{Code: ErrInternal, Message: "session/delete: " + err.Error()}
		}
		ctrl, err = s.factory.NewSession(ctx, SessionParams{Cwd: cwd, Sink: event.Discard})
		if err != nil {
			return nil, &RPCError{Code: ErrInternal, Message: "session/delete: " + err.Error()}
		}
	}
	if ctrl != nil {
		ctrl.Close()
		if owner := ctrl.SessionService(); owner != nil {
			err := owner.Delete(ctx, session.SessionRef{HostID: owner.HostID(), SessionID: p.SessionID})
			if err != nil && !errors.Is(err, session.ErrSessionNotFound) && !os.IsNotExist(err) {
				return nil, &RPCError{Code: ErrInternal, Message: "session/delete: " + err.Error()}
			}
		}
	}
	if path := sessionMetadataPath(s.sessionDir(), p.SessionID); path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, &RPCError{Code: ErrInternal, Message: "session/delete: " + err.Error()}
		}
	}
	return SessionDeleteResult{}, nil
}

// sessionCancel aborts a session's in-flight turn, if any. It is a notification:
// no reply, and an unknown session is silently ignored.
func (s *service) sessionCancel(_ context.Context, raw json.RawMessage) {
	var p SessionCancelParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if sess := s.session(p.SessionID); sess != nil {
		sess.abort()
	}
}

func (s *service) session(id string) *acpSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *service) takeSession(id string) *acpSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	delete(s.sessions, id)
	return sess
}

func (s *service) liveSessions() []*acpSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*acpSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	return out
}

func (s *service) sessionDir() string {
	if p, ok := s.factory.(SessionDirProvider); ok {
		if dir := strings.TrimSpace(p.SessionDir()); dir != "" {
			return dir
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if dir := sess.currentCtrl().SessionDir(); dir != "" {
			return dir
		}
	}
	return ""
}

func (s *service) sessionConfigState(ctx context.Context, p SessionConfigStateParams) (SessionConfigState, error) {
	if provider, ok := s.factory.(SessionConfigStateProvider); ok {
		state, err := provider.SessionConfigState(ctx, p)
		if err != nil {
			return SessionConfigState{}, err
		}
		return state, nil
	}
	return SessionConfigState{}, nil
}

func (s *service) configStateForSession(ctx context.Context, sess *acpSession) (SessionConfigState, error) {
	state, err := s.sessionConfigState(ctx, sess.configStateParams())
	if err != nil {
		return SessionConfigState{}, err
	}
	// Fold in the live controller's extension catalog so plugin/... models
	// are discoverable on every config-state read, not only when current.
	state = enrichStateWithExtensionModels(state, sess.currentCtrl().ProviderCatalog())
	state = withToolApprovalConfig(state, sess.currentToolApprovalMode())
	return state, nil
}

func (s *acpSession) configStateParams() SessionConfigStateParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionConfigStateParams{
		Cwd:            s.cwd,
		Model:          s.model,
		EffortOverride: cloneStringPtr(s.effortOverride),
	}
}

func (s *acpSession) currentToolApprovalMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return normalizeACPToolApprovalMode(s.toolApprovalMode)
}

func normalizeACPToolApprovalMode(mode string) string {
	return config.NormalizeToolApprovalMode(mode)
}

func normalizeACPCollaborationMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case sessionModePlan:
		return sessionModePlan
	case sessionModeGoal:
		return sessionModeGoal
	default:
		return sessionModeNormal
	}
}

func withToolApprovalConfig(state SessionConfigState, mode string) SessionConfigState {
	mode = normalizeACPToolApprovalMode(mode)
	option := SessionConfigOption{
		ID:           "tool_approval",
		Name:         "Permissions",
		Category:     "tool_approval",
		Type:         "select",
		CurrentValue: mode,
		Options: []SessionConfigSelectOption{
			{Value: control.ToolApprovalReadOnly, Name: "Read only", Description: "Read files; ask before writes and external side effects"},
			{Value: control.ToolApprovalWorkspaceWrite, Name: "Workspace access", Description: "Write inside the workspace and private session temp directory"},
			{Value: control.ToolApprovalDangerFullAccess, Name: "Full access", Description: "Skip ordinary prompts while explicit deny rules remain active"},
		},
	}
	for i := range state.ConfigOptions {
		if normalizeConfigID(state.ConfigOptions[i].ID) == option.ID {
			state.ConfigOptions[i] = option
			return state
		}
	}
	state.ConfigOptions = append(state.ConfigOptions, option)
	return state
}

func findConfigOption(options []SessionConfigOption, id string) (SessionConfigOption, bool) {
	id = normalizeConfigID(id)
	for _, opt := range options {
		if normalizeConfigID(opt.ID) == id {
			return opt, true
		}
	}
	return SessionConfigOption{}, false
}

func normalizeConfigID(id string) string { return strings.TrimSpace(id) }

func configOptionHasValue(option SessionConfigOption, value string) bool {
	for _, opt := range option.Options {
		if opt.Value == value {
			return true
		}
	}
	return false
}

func configOptionCategory(option SessionConfigOption) string {
	if option.Category != "" {
		return option.Category
	}
	switch normalizeConfigID(option.ID) {
	case "model":
		return "model"
	case "effort":
		return "thought_level"
	case "tool_approval":
		return "tool_approval"
	default:
		return ""
	}
}

func cloneStringPtr(p *string) *string {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

func clonePluginSpecs(in []plugin.Spec) []plugin.Spec {
	if len(in) == 0 {
		return nil
	}
	out := make([]plugin.Spec, len(in))
	copy(out, in)
	return out
}

func (s *service) resolveSessionCwd(cwd, sessionID string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		if !filepath.IsAbs(cwd) {
			return "", fmt.Errorf("cwd must be an absolute path")
		}
		return filepath.Clean(cwd), nil
	}
	if sessionID != "" {
		if meta, ok := s.loadMeta(sessionID); ok && meta.Cwd != "" {
			if !filepath.IsAbs(meta.Cwd) {
				return "", fmt.Errorf("stored cwd must be an absolute path")
			}
			return filepath.Clean(meta.Cwd), nil
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return wd, nil
}

func (s *service) loadMeta(id string) (acpSessionMeta, bool) {
	dir := s.sessionDir()
	if dir == "" {
		return acpSessionMeta{}, false
	}
	meta, ok, err := loadACPMeta(sessionMetadataPath(dir, id))
	if err != nil {
		return acpSessionMeta{}, false
	}
	return meta, ok
}

// closeAll tears down every open session (aborting any in-flight turn and
// stopping its MCP subprocesses) when the connection ends.
func (s *service) closeAll() {
	s.mu.Lock()
	sessions := s.sessions
	s.sessions = make(map[string]*acpSession)
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.abortAndWait()
		sess.currentCtrl().Close()
	}
}

func (s *acpSession) persistAfterTurn(prompt string) {
	s.mu.Lock()
	if s.deleted {
		s.mu.Unlock()
		return
	}
	ctrl := s.ctrl
	s.mu.Unlock()

	_ = ctrl.Snapshot()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleted || s.ctrl != ctrl {
		return
	}
	if s.title == "" {
		s.title = previewTitle(prompt)
	}
	s.updatedAt = time.Now().UTC()
	if s.createdAt.IsZero() {
		s.createdAt = s.updatedAt
	}
	if s.metaPath != "" {
		_ = saveACPMeta(s.metaPath, s.metaLocked())
	}
}

func (s *acpSession) meta() acpSessionMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metaLocked()
}

func (s *acpSession) metaLocked() acpSessionMeta {
	return acpSessionMeta{
		SessionID:         s.id,
		Cwd:               s.cwd,
		Model:             s.model,
		EffortOverride:    cloneStringPtr(s.effortOverride),
		ToolApprovalMode:  normalizeACPToolApprovalMode(s.toolApprovalMode),
		CollaborationMode: normalizeACPCollaborationMode(s.modeID),
		Title:             s.title,
		CreatedAt:         s.createdAt,
		UpdatedAt:         s.updatedAt,
		Status:            s.status.persisted(),
	}
}

func (s *acpSession) info() SessionInfo {
	meta := s.meta()
	ctrl := s.currentCtrl()
	extra := map[string]any{}
	if n := len(ctrl.History()); n > 0 {
		extra["messageCount"] = n
	}
	if len(extra) == 0 {
		extra = nil
	}
	return meta.info(extra)
}

func (s *service) sendAvailableCommands(sess *acpSession) {
	if sess == nil {
		return
	}
	ctrl := sess.currentCtrl()
	if ctrl == nil {
		return
	}
	cmds := availableCommandsFor(ctrl)
	if len(cmds) == 0 {
		return
	}
	sess.sink.send(availableCommandsUpdate{
		SessionUpdate:     "available_commands_update",
		AvailableCommands: cmds,
	})
}

func availableCommandsFor(ctrl acpController) []AvailableCommand {
	if ctrl == nil {
		return nil
	}
	byName := map[string]AvailableCommand{}
	for _, cmd := range ctrl.Commands() {

		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			continue
		}
		desc := strings.TrimSpace(cmd.Description)
		if desc == "" {
			desc = "Run the " + name + " command"
		}
		ac := AvailableCommand{Name: name, Description: desc}
		if hint := strings.TrimSpace(cmd.ArgHint); hint != "" {
			ac.Input = &AvailableCommandInput{Hint: hint}
		}
		byName[name] = ac
	}
	for _, sk := range ctrl.SlashSkills() {
		name := strings.TrimSpace(sk.SlashName())
		if name == "" {
			continue
		}
		if _, exists := byName[name]; exists {
			continue
		}
		desc := strings.TrimSpace(sk.Description)
		if desc == "" {
			desc = "Run the " + name + " skill"
		}
		byName[name] = AvailableCommand{
			Name:        name,
			Description: desc,
			Input:       &AvailableCommandInput{Hint: "instructions"},
		}
	}
	if host := ctrl.Host(); host != nil {
		for _, prompt := range host.Prompts() {
			name := strings.TrimSpace(prompt.Name)
			if name == "" {
				continue
			}
			desc := strings.TrimSpace(prompt.Description)
			if desc == "" {
				desc = "Run the " + name + " MCP prompt"
			}
			ac := AvailableCommand{Name: name, Description: desc}
			if len(prompt.Args) > 0 {
				ac.Input = &AvailableCommandInput{Hint: "arguments"}
			}
			byName[name] = ac
		}
	}
	// Extension actions surface as "<plugin>:<action>" commands so ACP clients
	// can discover them in the slash menu alongside commands/skills/prompts.
	for _, action := range ctrl.ExtensionActions() {
		name := strings.TrimPrefix(strings.TrimSpace(action.Slash), "/")
		if name == "" {
			continue
		}
		if _, exists := byName[name]; exists {
			continue
		}
		desc := strings.TrimSpace(action.Label)
		if desc == "" {
			desc = "Run the " + name + " extension action"
		}
		byName[name] = AvailableCommand{
			Name:        name,
			Description: desc,
			Input:       &AvailableCommandInput{Hint: "arguments"},
		}
	}
	out := make([]AvailableCommand, 0, len(byName))
	for _, cmd := range byName {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *service) resolveSlashPrompt(ctx context.Context, sess *acpSession, text string) (string, string) {
	line := strings.TrimSpace(text)
	if sess == nil || !strings.HasPrefix(line, "/") {
		return text, ""
	}
	ctrl := sess.currentCtrl()
	if ctrl == nil {
		return text, ""
	}
	if sent, ok := ctrl.CustomCommand(line); ok {
		return sent, ""
	}
	if sent, name, ok := ctrl.RunSkillWithName(line); ok {
		return sent, name
	}
	if sent, ok, err := ctrl.MCPPrompt(ctx, line); err == nil && ok {
		return sent, ""
	}
	if sent, ok := invokeExtensionAction(ctx, ctrl, line); ok {
		return sent, ""
	}
	return text, ""
}

// invokeExtensionAction resolves a "/<plugin>:<action> args…" line against the
// handshake-declared extension actions and invokes it — the last resolution
// step in resolveSlashPrompt, after custom commands, skills, and MCP prompts.
// The extension's result message becomes the prompt text. A parse miss, an
// undeclared action, an invocation error, or an empty result all leave the
// line untouched (ok=false), matching how unknown slash commands fall through.
func invokeExtensionAction(ctx context.Context, ctrl acpController, line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false
	}
	pluginID, actionID, ok := uihub.ParseSlashName(fields[0])
	if !ok {
		return "", false
	}
	declared := false
	for _, action := range ctrl.ExtensionActions() {
		if action.PluginID == pluginID && action.ActionID == actionID {
			declared = true
			break
		}
	}
	if !declared {
		return "", false
	}
	message, err := ctrl.InvokeExtensionAction(ctx, fields[0], control.ParseExtensionActionArgs(fields[1:]))
	if err != nil || strings.TrimSpace(message) == "" {
		return "", false
	}
	return message, true
}

type acpSessionMeta struct {
	SchemaVersion     int                       `json:"schemaVersion"`
	SessionID         string                    `json:"sessionId"`
	Cwd               string                    `json:"cwd"`
	Model             string                    `json:"model,omitempty"`
	EffortOverride    *string                   `json:"effortOverride,omitempty"`
	ToolApprovalMode  string                    `json:"toolApprovalMode,omitempty"`
	CollaborationMode string                    `json:"collaborationMode,omitempty"`
	Title             string                    `json:"title,omitempty"`
	CreatedAt         time.Time                 `json:"createdAt"`
	UpdatedAt         time.Time                 `json:"updatedAt"`
	Status            *persistedStatusTelemetry `json:"status,omitempty"`
}

func (m acpSessionMeta) info(extra map[string]any) SessionInfo {
	updatedAt := ""
	if !m.UpdatedAt.IsZero() {
		updatedAt = m.UpdatedAt.Format(time.RFC3339Nano)
	}
	return SessionInfo{
		SessionID: m.SessionID,
		Cwd:       m.Cwd,
		Title:     m.Title,
		UpdatedAt: updatedAt,
		Meta:      extra,
	}
}

func metadataForLoadedSession(path, id, cwd string, history []provider.Message) acpSessionMeta {
	now := time.Now().UTC()
	meta, ok, err := loadACPMeta(path)
	if err != nil || !ok {
		meta = acpSessionMeta{
			SessionID: id,
			Cwd:       cwd,
			Title:     titleFromHistory(history),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if info, statErr := os.Stat(path); statErr == nil {
			meta.CreatedAt = info.ModTime().UTC()
			meta.UpdatedAt = info.ModTime().UTC()
		}
	}
	if meta.SessionID == "" {
		meta.SessionID = id
	}
	if cwd != "" {
		meta.Cwd = cwd
	}
	if meta.Title == "" {
		meta.Title = titleFromHistory(history)
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	if meta.UpdatedAt.IsZero() {
		meta.UpdatedAt = meta.CreatedAt
	}
	return meta
}

func loadACPMeta(sessionPath string) (acpSessionMeta, bool, error) {
	path := sessionPath
	if path == "" {
		return acpSessionMeta{}, false, nil
	}
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		if os.IsNotExist(err) {
			return acpSessionMeta{}, false, nil
		}
		return acpSessionMeta{}, false, err
	}
	var meta acpSessionMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return acpSessionMeta{}, false, fmt.Errorf("decode ACP session metadata %s: %w", path, err)
	}
	if meta.SchemaVersion != 1 {
		return acpSessionMeta{}, false, fmt.Errorf("unsupported ACP metadata version %d", meta.SchemaVersion)
	}
	return meta, true, nil
}

func saveACPMeta(sessionPath string, meta acpSessionMeta) error {
	path := sessionPath
	if path == "" {
		return nil
	}
	meta.SchemaVersion = 1
	now := time.Now().UTC()
	if meta.SessionID == "" {
		meta.SessionID = sessionIDFromTranscript(sessionPath)
	}
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	if meta.UpdatedAt.IsZero() {
		meta.UpdatedAt = meta.CreatedAt
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".acp-session.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, path)
}

func listACPMetas(dir string) ([]acpSessionMeta, error) {
	root := filepath.Join(dir, "acp")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []acpSessionMeta{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !isSafeSessionID(id) {
			continue
		}
		meta, ok, err := loadACPMeta(filepath.Join(root, entry.Name()))
		if err != nil || !ok || meta.SessionID != id || meta.Cwd == "" {
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

func sessionMetadataPath(dir, id string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "acp", id+".json")
}

func sessionIDFromTranscript(path string) string {
	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

func sessionInfoMatchesCwd(info SessionInfo, filter string) bool {
	if filter == "" {
		return true
	}
	return filepath.Clean(info.Cwd) == filepath.Clean(filter)
}

func titleFromHistory(history []provider.Message) string {
	for _, m := range history {
		if agent.IsUserAuthoredTurnMessage(m) {
			if title := previewTitle(m.Content); title != "" {
				return title
			}
		}
	}
	return ""
}

func previewTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= 80 {
		return text
	}
	runes := []rune(text)
	return string(runes[:77]) + "..."
}

func validateSessionID(method, id string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return &RPCError{Code: ErrInvalidParams, Message: method + ": missing sessionId"}
	}
	if trimmed != id || trimmed == "." || trimmed == ".." || !isSafeSessionID(trimmed) {
		return &RPCError{Code: ErrInvalidParams, Message: method + ": invalid sessionId"}
	}
	return nil
}

func isSafeSessionID(id string) bool {
	for _, r := range id {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func parseSessionUpdatedAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// mcpSpecs converts ACP MCP server declarations to plugin.Spec.
func mcpSpecs(in []MCPServerSpec, cwd string) ([]plugin.Spec, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]plugin.Spec, 0, len(in))
	for _, m := range in {
		typ := strings.ToLower(strings.TrimSpace(m.Type))
		if typ == "" {
			typ = "stdio"
		}
		if strings.TrimSpace(m.Name) == "" {
			return nil, fmt.Errorf("MCP server name is required")
		}
		switch typ {
		case "stdio":
			if strings.TrimSpace(m.Command) == "" {
				return nil, fmt.Errorf("MCP server %q command is required", m.Name)
			}
		case "http", "streamable-http", "streamable_http", "sse":
			if strings.TrimSpace(m.URL) == "" {
				return nil, fmt.Errorf("MCP server %q url is required", m.Name)
			}
			if typ != "sse" {
				typ = "http"
			}
		default:
			return nil, fmt.Errorf("MCP server %q uses unsupported transport %q", m.Name, m.Type)
		}
		out = append(out, plugin.Spec{
			Name:          strings.TrimSpace(m.Name),
			Type:          typ,
			Command:       strings.TrimSpace(m.Command),
			Args:          append([]string(nil), m.Args...),
			Env:           mapString(m.Env),
			URL:           strings.TrimSpace(m.URL),
			Headers:       mapString(m.Headers),
			Dir:           cwd,
			WorkspaceRoot: cwd,
		})
	}
	return out, nil
}

func mapString(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

// newSessionID returns a random RFC 4122 v4 UUID string used to address a session.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
