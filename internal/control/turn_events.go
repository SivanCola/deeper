package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/evidence"
	"deepzora/internal/session"
	"deepzora/internal/sessioninbox"
	"deepzora/internal/transcript"
	"deepzora/internal/turnevent"
)

// turnEventSink persists lifecycle envelopes before frontend publication.
// Provider-facing transcript messages remain a separate artifact.
type turnEventSink struct {
	event.AuditForwarder
	innerMu sync.RWMutex
	inner   event.Sink
	stream  event.Sink
	c       *Controller
	publish atomic.Int32
}

type turnEventDurableSink struct{ owner *turnEventSink }

// clearProjectionLocked invalidates readers of a replaced transcript. The
// caller holds mu, including across the replacement's state updates.
func (s *turnEventState) clearProjectionLocked() {
	if s.projection != nil {
		s.projection.CloseFollowers()
	}
	s.projection = nil
	s.projectionErr = nil
}

// turnEventState has an independent lock so ledger I/O never holds c.mu.
type turnEventState struct {
	mu                  sync.RWMutex
	ledger              *turnevent.Ledger
	err                 error
	projection          *transcript.Projection
	projectionErr       error
	commitMu            sync.Mutex
	volatileTodos       []event.Todo
	volatileTodoWritten bool
	// pendingExecutionCommit is prepared by an unpublished hot-rebuild
	// candidate and consumed atomically with Runtime execution activation.
	// commitMu owns it and its queue reservation.
	pendingExecutionCommit *session.PreparedBatch
	pendingTermination     *TerminationPlan
	turnMessageIDs         map[string]bool
	finalizedTurn          string
	terminationBoundary    *terminationBoundary
	openStream             openStreamOutput
}

// projectVolatileTodo keeps the same event-derived projection for controllers
// that have not acquired a session path yet. It is a cache of successful
// lifecycle events, never a second writable todo state machine.
func (c *Controller) projectVolatileTodo(e event.Event) {
	if c == nil {
		return
	}
	c.turnEvents.mu.Lock()
	defer c.turnEvents.mu.Unlock()
	switch {
	case e.Kind == event.TurnStarted:
		c.turnEvents.volatileTodos = []event.Todo{}
		c.turnEvents.volatileTodoWritten = false
	case e.Kind == event.ToolResult && e.Tool.TodoWritten:
		c.turnEvents.volatileTodos = append([]event.Todo(nil), e.Tool.Todos...)
		c.turnEvents.volatileTodoWritten = true
	}
}

func (c *Controller) volatileTodoState() ([]event.Todo, bool) {
	if c == nil {
		return []event.Todo{}, false
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	return append([]event.Todo(nil), c.turnEvents.volatileTodos...), c.turnEvents.volatileTodoWritten
}

func newTurnEventSink(inner event.Sink, c *Controller) *turnEventSink {
	s := &turnEventSink{inner: inner, c: c}
	s.stream = event.Coalesce(&turnEventDurableSink{owner: s}, event.DefaultStreamDeltaWindow)
	s.AuditForwarder = event.AuditForwarder{Inner: s.stream}
	return s
}

func (s *turnEventSink) InboxChanged(snap sessioninbox.InboxSnapshot) {
	if s != nil {
		notifyInboxChanged(s.innerSnapshot(), snap)
	}
}

var _ event.OptionalSinkCapabilities = (*turnEventSink)(nil)
var _ event.CheckedSink = (*turnEventSink)(nil)
var _ event.OptionalSinkCapabilities = (*turnEventDurableSink)(nil)
var _ event.CheckedSink = (*turnEventDurableSink)(nil)

func (s *turnEventSink) Emit(e event.Event) {
	if s == nil {
		return
	}
	s.observe(e)
	if turnEventSynchronousBarrier(e.Kind) {
		if err := event.EmitChecked(s.stream, e); err != nil {
			s.fail(err)
		}
		return
	}
	s.stream.Emit(e)
}

// observe feeds every raw event to the ledger's routing and to the liveness
// tracker before ordering, so silence is measured from real emission time.
func (s *turnEventSink) observe(e event.Event) {
	if s.c == nil {
		return
	}
	if ledger := s.c.turnEventLedger(); ledger != nil {
		ledger.ObserveRawEvent(e)
	}
	s.c.turnEvents.openStream.observe(e)
	s.c.liveness.observe(e, time.Now())
}

func turnEventSynchronousBarrier(kind event.Kind) bool {
	switch kind {
	case event.ToolDispatch, event.ToolStarted, event.ToolResult, event.AskRequest, event.ApprovalRequest,
		event.MCPInteractionRequest, event.PromptAnswered, event.TurnStatusChanged,
		event.TurnStarted, event.TurnDone, event.SessionOperation:
		return true
	default:
		return false
	}
}

func (s *turnEventSink) EmitChecked(e event.Event) error {
	if s == nil {
		return nil
	}
	s.observe(e)
	var err error
	if s.publish.Load() > 0 && e.Kind == event.PromptAnswered {
		// A frontend may answer during prompt publication, so the coalescer cannot
		// wait on itself. Only that already-ordered PromptAnswered barrier may use
		// this re-entrant path; other checked events preserve coalescer ordering.
		err = (&turnEventDurableSink{owner: s}).EmitChecked(e)
	} else {
		err = event.EmitChecked(s.stream, e)
	}
	if err != nil {
		s.fail(err)
	}
	return err
}

func (s *turnEventSink) fail(err error) {
	if s != nil && s.c != nil && err != nil {
		s.c.failTurnEventLedger(err)
	}
}

func (s *turnEventSink) innerSnapshot() event.Sink {
	if s == nil {
		return nil
	}
	s.innerMu.RLock()
	defer s.innerMu.RUnlock()
	return s.inner
}

func (s *turnEventSink) setInner(inner event.Sink) {
	if s == nil {
		return
	}
	s.innerMu.Lock()
	s.inner = inner
	s.innerMu.Unlock()
}

func (s *turnEventSink) publishInner(e event.Event) {
	inner := s.innerSnapshot()
	if inner == nil {
		return
	}
	s.publish.Add(1)
	defer s.publish.Add(-1)
	inner.Emit(e)
}

// emitChecked persists before publish and returns durability failures to the
// admission boundary. It also suppresses the executor's duplicate TurnStarted
// because the controller has already committed that transition before the
// provider goroutine is launched.
func (s *turnEventSink) persistAndPublish(e event.Event) error {
	if s == nil || s.c == nil {
		return nil
	}
	if e.Kind == event.SessionOperation {
		if err := s.c.persistMaintenanceOperation(e); err != nil {
			return err
		}
		return s.publishOutsideTurn(s.c.turnEventLedger(), e)
	}
	if e.RecoveryCheckpoint {
		return s.c.CheckpointSession(context.Background(), agent.CheckpointBeforeTopTool)
	}
	if err := s.c.stampToolRecoveryEvent(e); err != nil {
		return err
	}
	ledger := s.c.turnEventLedger()
	if ledger == nil {
		s.c.projectVolatileTodo(e)
		s.c.refreshRuntimeState(e)
		s.publishInner(e)
		return nil
	}
	if staleTurnStatus(e, ledger) {
		return nil
	}
	// Outside-turn notices are not lifecycle records and must pass through after
	// bootstrap or a terminal event.
	if ledger.ActiveTurnID() == "" {
		return s.publishOutsideTurn(ledger, e)
	}
	if e.Kind == event.TurnStarted && ledger.CurrentStatus() == event.TurnInProgress {
		return nil
	}
	status := publicationTurnStatus(e, ledger)
	if e.WriteIntent {
		return nil
	}
	// No frontend callback runs while commitMu is held. Prompt publication
	// can synchronously reenter this sink to append PromptAnswered.
	stamped, envelope, ok, err := s.commitEnvelope(ledger, e, status)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := s.c.flushSubmissionStart(s.c.submissionAdmissionContext(), e.Kind); err != nil {
		return err
	}
	if e.Kind == event.TurnDone {
		if store := s.c.sessionEventStore(); store != nil {
			if _, err := store.Flush(context.Background()); err != nil {
				return err
			}
		}

	}
	if _, runtime, exclusive := s.c.v3Binding(); exclusive && runtime != nil {
		if err := runtime.PublishTranscriptFrame(envelope); err != nil {
			return err
		}
	}
	s.c.recordTurnLifecycle(stamped)
	s.c.refreshRuntimeState(stamped)
	s.publishInner(stamped)
	if e.Kind == event.TurnDone && !ledger.ProjectionAckRequired() {
		if err := ledger.AcknowledgeProjection(stamped.TurnID); err != nil {
			return err
		}
	}
	return nil
}

func lateBusinessEvent(kind event.Kind) bool {
	switch kind {
	case event.ToolDispatch, event.ToolStarted, event.ToolProgress, event.ToolResult,
		event.AskRequest, event.ApprovalRequest, event.MCPInteractionRequest,
		event.PromptAnswered, event.TurnStarted, event.TurnStatusChanged, event.TurnDone:
		return true
	default:
		return false
	}
}

func (s *turnEventSink) commitEnvelope(ledger *turnevent.Ledger, e event.Event, status event.TurnStatus) (event.Event, turnevent.Envelope, bool, error) {
	if e.Kind == event.TurnDone {
		s.c.snapshotMu.Lock()
		defer s.c.snapshotMu.Unlock()
	}
	s.c.turnEvents.commitMu.Lock()
	defer s.c.turnEvents.commitMu.Unlock()
	if s.c.discardLateTurnEvent(e) {
		slog.Info("controller: discarded late turn event", "kind", e.Kind, "turnId", e.TurnID)
		return e, turnevent.Envelope{}, false, nil
	}
	ctx := context.Background()
	if e.Kind == event.TurnDone {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, terminationFlushTimeout)
		defer cancel()
	}
	if e.Kind == event.Notice && e.Code == event.NoticeCodeMCPToolsList && e.MessageID == "" {
		if store := s.c.sessionEventStore(); store != nil {
			e.MessageID = fmt.Sprintf("notice:%s:%d", store.ID(), store.EventSequence()+1)
		}
	}
	if err := s.c.appendSessionEventLocked(ctx, e); err != nil {
		return e, turnevent.Envelope{}, false, err
	}
	if e.Kind == event.TurnDone {
		e.ReadCompletion = s.c.updateTurnLedgerTranscript(ledger)
	}
	stamped, envelope, ok, err := ledger.AppendEnvelope(e, status)
	if err != nil || !ok || stamped.Sequence == 0 {
		return stamped, envelope, ok, err
	}
	s.c.turnEvents.mu.RLock()
	projection := s.c.turnEvents.projection
	s.c.turnEvents.mu.RUnlock()
	_, _, exclusive := s.c.v3Binding()
	if projection != nil && !exclusive {
		if projectionErr := projection.Apply(envelope); projectionErr != nil {
			s.c.turnEvents.mu.Lock()
			s.c.turnEvents.projectionErr = projectionErr
			s.c.turnEvents.mu.Unlock()
		}
	}
	return stamped, envelope, true, nil
}

func (s *turnEventDurableSink) Emit(e event.Event) {
	_ = s.EmitChecked(e)
}

func (s *turnEventDurableSink) EmitChecked(e event.Event) error {
	if s == nil || s.owner == nil {
		return nil
	}
	err := s.owner.persistAndPublish(e)
	if err == nil {
		return nil
	}
	if classifyCommitError(err) == commitLifecycle {
		slog.Info("controller: lifecycle event commit", "err", err, "kind", e.Kind)
		return nil
	}
	if e.Kind == event.TurnDone && classifyCommitError(err) != commitOwnership {
		s.owner.c.disarmGoalLifecycle("persistence-error")
		s.owner.c.mu.Lock()
		s.owner.c.enterRecoveryLocked("terminal_commit_failed")
		s.owner.c.mu.Unlock()
		if _, runtime, exclusive := s.owner.c.v3Binding(); exclusive && runtime != nil {
			runtime.Transcript().PersistenceFailed()
		}
	}
	// Async stream callers cannot observe checked errors. Fail the Turn here so
	// a poisoned WAL immediately cancels provider, prompt, and process work.
	slog.Error("controller: append turn event ledger", "err", err, "kind", e.Kind)
	s.owner.fail(err)
	if e.Kind == event.TurnDone {
		// The durable terminal failed, so publish a sequence-free control-plane
		// failure only to release UI state. It is never treated as ledger truth.
		e.Err = errors.Join(e.Err, err)
		e.Status = event.TurnFailed
		if inner := s.owner.innerSnapshot(); inner != nil {
			inner.Emit(e)
		}
	}
	return err
}

func (s *turnEventDurableSink) inner() event.Sink {
	if s == nil || s.owner == nil {
		return nil
	}
	return s.owner.innerSnapshot()
}

func (s *turnEventDurableSink) RecordDelegationAudit(a evidence.DelegationAudit) {
	event.RecordDelegationAudit(s.inner(), a)
}
func (s *turnEventDurableSink) RecordReadinessAudit(a evidence.ReadinessAudit) {
	event.RecordReadinessAudit(s.inner(), a)
}
func (s *turnEventDurableSink) RecordAnchorSafetyAudit(a event.AnchorSafetyAudit) {
	event.RecordAnchorSafetyAudit(s.inner(), a)
}
func (s *turnEventDurableSink) RecordTurnCompletion() { event.RecordTurnCompletion(s.inner()) }
func (s *turnEventDurableSink) RecordContractShadow(a event.ContractShadowAudit) {
	event.RecordContractShadow(s.inner(), a)
}
func (s *turnEventDurableSink) RecordCompletionReport(a event.CompletionReportAudit) {
	event.RecordCompletionReport(s.inner(), a)
}
func (s *turnEventDurableSink) RecordMemoryRecall(a event.MemoryRecallAudit) {
	event.RecordMemoryRecall(s.inner(), a)
}
func (s *turnEventDurableSink) RecordDelegationAdmission(a event.DelegationAdmissionAudit) {
	event.RecordDelegationAdmission(s.inner(), a)
}
func (s *turnEventDurableSink) RecordOutcomeProgress(a evidence.OutcomeSample) {
	event.RecordOutcomeProgress(s.inner(), a)
}
func (s *turnEventDurableSink) RecordProtocolRecovery(a event.ProtocolRecoveryAudit) {
	event.RecordProtocolRecovery(s.inner(), a)
}
func (s *turnEventDurableSink) RecordWorkspaceMutation(a event.WorkspaceMutation) {
	event.RecordWorkspaceMutation(s.inner(), a)
}
func (s *turnEventDurableSink) RecordRunBudget(a event.RunBudgetSample) {
	event.RecordRunBudget(s.inner(), a)
}
func (s *turnEventDurableSink) RecordSubagentLifecycle(a event.SubagentLifecycleInfo) {
	event.RecordSubagentLifecycle(s.inner(), a)
}

func terminalTurnStatus(e event.Event) event.TurnStatus {
	if e.Recovery != nil && e.Recovery.State == "recovery_required" {
		return event.TurnRecoveryRequired
	}
	if e.Cancelled || errors.Is(e.Err, context.Canceled) {
		return event.TurnInterrupted
	}
	if e.Err != nil {
		return event.TurnFailed
	}
	return event.TurnCompleted
}

func (c *Controller) turnEventLedger() *turnevent.Ledger {
	if c == nil {
		return nil
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	return c.turnEvents.ledger
}

func (c *Controller) turnEventLedgerError() error {
	if c == nil {
		return nil
	}
	c.turnEvents.mu.RLock()
	defer c.turnEvents.mu.RUnlock()
	return c.turnEvents.err
}

func (c *Controller) applyTurnDoneProtocol(done event.Event, cancelRequested bool) event.Event {
	if cancelRequested {
		// Interruption is a terminal state, not a send failure; partial text is
		// already display-only by this point.
		done.Err = nil
	}
	return done
}

func (c *Controller) activeLedgerTurnID() string {
	ledger := c.turnEventLedger()
	if ledger == nil {
		return ""
	}
	return ledger.ActiveTurnID()
}

func (c *Controller) rebindTurnEvents() {
	if c == nil {
		return
	}
	defer c.refreshRuntimeState(event.Event{})
	id := ""
	if _, runtime, _ := c.v3Binding(); runtime != nil {
		id = runtime.Ref().SessionID
	}
	ledger := turnevent.NewMemory(id)
	var projection *transcript.Projection
	var projectionErr error
	if !c.sessionEngineEnabled() {
		projection, projectionErr = c.newMemoryTranscriptProjection(ledger)
	}
	c.turnEvents.mu.Lock()
	c.turnEvents.ledger, c.turnEvents.err = ledger, nil
	c.turnEvents.volatileTodos = []event.Todo{}
	c.turnEvents.volatileTodoWritten = false
	c.turnEvents.clearProjectionLocked()
	c.turnEvents.projection, c.turnEvents.projectionErr = projection, projectionErr
	c.turnEvents.mu.Unlock()

}

func classifyCommitError(err error) commitFailureKind {
	if err == nil {
		return commitOK
	}
	if errors.Is(err, errTerminationDurability) {
		return commitUnexpected
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return commitLifecycle
	}
	if errors.Is(err, session.ErrStaleActivity) || errors.Is(err, session.ErrOperationConflict) {
		return commitLifecycle
	}
	if errors.Is(err, session.ErrSessionNotRunning) || errors.Is(err, session.ErrStaleGeneration) ||
		errors.Is(err, session.ErrStaleExecution) || errors.Is(err, session.ErrReadOnly) || errors.Is(err, session.ErrRuntimeRetiring) {
		return commitOwnership
	}
	return commitUnexpected
}

type commitFailureKind int

const (
	commitOK commitFailureKind = iota
	commitLifecycle
	commitOwnership
	commitUnexpected
)

func (c *Controller) failTurnEventLedger(err error) {
	defer c.refreshRuntimeState(event.Event{})
	if c == nil || err == nil {
		return
	}
	switch classifyCommitError(err) {
	case commitLifecycle:
		slog.Info("controller: lifecycle commit result", "err", err)
		return
	case commitOwnership:
		c.turnEvents.mu.Lock()
		if c.turnEvents.err == nil {
			c.turnEvents.err = err
		}
		c.turnEvents.mu.Unlock()
		c.signalTurnCancel()
		c.promptOwner.CancelAll()
		c.approval.clearAll()
		return
	}
	c.turnEvents.mu.Lock()
	if c.turnEvents.err == nil {
		c.turnEvents.err = err
	}
	c.turnEvents.mu.Unlock()
	c.signalTurnCancel()
	c.promptOwner.CancelAll()
	c.approval.clearAll()
}

// staleTurnStatus reports a status stamped for a turn that has since reached
// its terminal event; cancelling is sticky, so it must not reach the next turn.
func staleTurnStatus(e event.Event, ledger *turnevent.Ledger) bool {
	return e.Kind == event.TurnStatusChanged && e.TurnID != "" && e.TurnID != ledger.ActiveTurnID()
}

// emitTurnStatus stamps the transition with the turn that requested it so the
// ledger can drop it if that turn already reached its terminal event.
func (c *Controller) emitTurnStatus(status event.TurnStatus, turnID string) {
	if c == nil || status == "" {
		return
	}
	c.sink.Emit(event.Event{Kind: event.TurnStatusChanged, Status: status, TurnID: turnID})
}

// emitTurnEventChecked reaches the lifecycle sink below the inbox observer so
// admission can fail closed on disk errors instead of starting an unledgered
// provider request. Lifecycle events do not participate in inbox notice logic.
func (c *Controller) emitTurnEventChecked(e event.Event) error {
	if c == nil {
		return nil
	}
	if e.ItemID != "" && e.TurnID == "" {
		if identity, ok := c.promptOwner.Identity(e.ItemID); ok {
			e.TurnID = identity.TurnID
			e.PromptKind = string(identity.Kind)
		}
	} else if e.ItemID != "" && e.PromptKind == "" {
		if identity, ok := c.promptOwner.Identity(e.ItemID); ok {
			e.PromptKind = string(identity.Kind)
		}
	}
	return event.EmitChecked(c.sink, e)
}

// SetTurnEventRoutingMetadata attaches desktop routing identity to lifecycle
// envelopes only. It never changes provider-visible prompts or tool schemas.
func (c *Controller) SetTurnEventRoutingMetadata(runtimeEpoch, submissionID string) {
	c.promptEpochMu.Lock()
	c.promptRuntimeEpoch = runtimeEpoch
	c.promptEpochMu.Unlock()
	if ledger := c.turnEventLedger(); ledger != nil {
		ledger.RequireProjectionAck(true)
		ledger.SetRoutingMetadata(runtimeEpoch, submissionID)
	}
	c.BindTranscriptRuntimeEpoch(runtimeEpoch)
}

func (c *Controller) DrainTurnEventMetrics() turnevent.MetricsSnapshot {
	ledger := c.turnEventLedger()
	if ledger == nil {
		return turnevent.MetricsSnapshot{}
	}
	return ledger.DrainMetrics()
}

func publicationTurnStatus(e event.Event, ledger *turnevent.Ledger) event.TurnStatus {
	status := e.Status
	if status == "" {
		status = ledger.CurrentStatus()
	}
	switch e.Kind {
	case event.TurnStarted:
		status = event.TurnInProgress
	case event.AskRequest, event.ApprovalRequest, event.MCPInteractionRequest:
		status = event.TurnWaitingUser
	case event.TurnDone:
		status = terminalTurnStatus(e)
	case event.TurnStatusChanged:
		// The emitter supplied the exact transition in e.Status.
	}
	return status
}
