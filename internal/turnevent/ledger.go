// Package turnevent owns the local lifecycle ledger for a session. The ledger
// is a projection/recovery artifact only and never contributes to model input.
package turnevent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"deepzora/internal/event"
	"deepzora/internal/eventwire"
)

const (
	schemaVersion = 2

	terminalSummaryLimit       = 16
	replayMaxEvents            = 512
	replaySoftBytes      int64 = 2 << 20
)

var ErrTurnLedgerUnavailable = errors.New("turn event ledger unavailable")

// Envelope is one durable runtime event. Dynamic routing fields stay local and
// are never injected into prompts or provider requests.
type Envelope struct {
	SchemaVersion      int              `json:"schemaVersion"`
	SessionID          string           `json:"sessionId"`
	TurnID             string           `json:"turnId"`
	Sequence           uint64           `json:"seq"`
	ItemID             string           `json:"itemId,omitempty"`
	AttemptID          string           `json:"attemptId,omitempty"`
	RuntimeEpoch       string           `json:"runtimeEpoch,omitempty"`
	SubmissionID       string           `json:"submissionId,omitempty"`
	Source             string           `json:"source,omitempty"`
	Kind               string           `json:"kind"`
	Status             event.TurnStatus `json:"status"`
	TranscriptRevision int64            `json:"transcriptRevision,omitempty"`
	TranscriptDigest   string           `json:"transcriptDigest,omitempty"`
	RewriteEpoch       uint64           `json:"rewriteEpoch,omitempty"`
	CreatedAt          int64            `json:"createdAt"`
	Event              eventwire.Event  `json:"event"`
}

// TerminalSummary is the bounded, content-free history kept by checkpoints.
type TerminalSummary struct {
	TurnID             string           `json:"turnId"`
	TerminalSequence   uint64           `json:"terminalSeq"`
	Status             event.TurnStatus `json:"status"`
	Outcome            string           `json:"outcome,omitempty"`
	RuntimeEpoch       string           `json:"runtimeEpoch,omitempty"`
	SubmissionID       string           `json:"submissionId,omitempty"`
	StartedAt          int64            `json:"startedAt,omitempty"`
	FinishedAt         int64            `json:"finishedAt,omitempty"`
	DurationMs         int64            `json:"durationMs,omitempty"`
	TranscriptRevision int64            `json:"transcriptRevision,omitempty"`
	TranscriptDigest   string           `json:"transcriptDigest,omitempty"`
}

// ReplayView is a bounded page plus the retained-history contract a frontend
// needs to distinguish an ordinary sequence gap from checkpoint compaction.
type ReplayView struct {
	Events             []Envelope `json:"events"`
	FloorSequence      uint64     `json:"floorSeq"`
	LatestSequence     uint64     `json:"latestSeq"`
	NextAfterSequence  uint64     `json:"nextAfterSeq"`
	HasMore            bool       `json:"hasMore"`
	ResetRequired      bool       `json:"resetRequired"`
	TranscriptRevision int64      `json:"transcriptRevision,omitempty"`
	TranscriptDigest   string     `json:"transcriptDigest,omitempty"`
	RuntimeEpoch       string     `json:"runtimeEpoch,omitempty"`
}

// PendingProjection is an unacknowledged terminal turn.
type PendingProjection struct {
	TurnID string
	Status event.TurnStatus
	Events []Envelope
}

type routingMetadata struct {
	runtimeEpoch string
	submissionID string
}

// MetricsSnapshot contains counters only; no event content, ids or paths leave
// the ledger through this surface.
// Ledger serializes sequence allocation and projection acknowledgement within
// one session actor lane.
type Ledger struct {
	mu        sync.Mutex
	sessionID string

	nextSeq uint64
	ledgerTurnState
	routing     routingMetadata
	nextRouting routingMetadata
	transcript  transcriptSnapshot
	todos       []event.Todo
	todoWritten bool
	recovery    *event.RecoveryStatus

	submissionTurns            map[string]string
	records                    []Envelope
	summaries                  []TerminalSummary
	projectionAcks             map[string]uint64
	compactedThrough           uint64
	projectionCommittedThrough uint64

	requireProjectionAck bool

	metrics MetricsSnapshot
}

type ledgerTurnState struct {
	turnStartSeq uint64
	turnStarted  int64
	active       string
	status       event.TurnStatus
	terminal     bool
}

// NewMemory allocates turn identities and orders lifecycle envelopes within
// one controller. The host Session Runtime owns durable business facts.
func NewMemory(sessionID string) *Ledger {
	return &Ledger{
		sessionID:       sessionID,
		nextSeq:         1,
		submissionTurns: make(map[string]string),
		projectionAcks:  make(map[string]uint64),
	}
}

func (l *Ledger) Begin() (string, error) {
	if l == nil {
		return "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != "" && !l.terminal {
		return "", fmt.Errorf("turn %s is still active", l.active)
	}
	id, err := newTurnID()
	if err != nil {
		return "", err
	}
	l.active, l.status, l.terminal = id, event.TurnQueued, false
	l.turnStartSeq, l.turnStarted = l.nextSeq, time.Now().UnixMilli()
	l.routing = l.nextRouting
	l.nextRouting = routingMetadata{runtimeEpoch: l.routing.runtimeEpoch}
	if l.routing.submissionID != "" {
		l.submissionTurns[l.routing.submissionID] = id
	}
	l.transcript = transcriptSnapshot{}
	return id, nil
}

func (l *Ledger) SetRoutingMetadata(runtimeEpoch, submissionID string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.nextRouting = routingMetadata{runtimeEpoch: runtimeEpoch, submissionID: submissionID}
	l.mu.Unlock()
}

// SetRuntimeEpoch binds a newly installed runtime without changing a queued
// submission identity. Active turns retain the routing captured by Begin.
func (l *Ledger) SetRuntimeEpoch(runtimeEpoch string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextRouting.runtimeEpoch = runtimeEpoch
}

func (l *Ledger) SetSubmissionID(submissionID string) {
	l.mu.Lock()
	l.nextRouting.submissionID = submissionID
	l.mu.Unlock()
}

func (l *Ledger) RequireProjectionAck(required bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.requireProjectionAck = required
	l.mu.Unlock()
}

func (l *Ledger) ProjectionAckRequired() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requireProjectionAck
}

func (l *Ledger) TurnIDForSubmission(submissionID string) string {
	if l == nil || submissionID == "" {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.submissionTurns[submissionID]
}

// ObserveRawEvent counts provider stream pressure before the coalescer. It
// intentionally records no content or routing identity.
func (l *Ledger) ObserveRawEvent(e event.Event) {
	if l == nil || (e.Kind != event.Text && e.Kind != event.Reasoning) {
		return
	}
	l.mu.Lock()
	l.metrics.RawEvents++
	l.mu.Unlock()
}

// ActiveTurnID returns the current unfinished turn.
func (l *Ledger) ActiveTurnID() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		return ""
	}
	return l.active
}

func (l *Ledger) CurrentStatus() event.TurnStatus {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

func (l *Ledger) ProjectionCursor() (latest, replayAfter uint64) {
	if l == nil {
		return 0, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	latest = l.latestLocked()
	replayAfter = latest
	if l.active != "" && !l.terminal && l.turnStartSeq > 0 {
		replayAfter = l.turnStartSeq - 1
	}
	return latest, replayAfter
}

func (l *Ledger) Append(e event.Event, status event.TurnStatus) (event.Event, bool, error) {
	if l == nil {
		return e, true, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.appendLocked(e, status)
}

// AppendEnvelope returns the exact committed envelope under the append lock.
// Consumers can project it before publication without reconstructing routing
// from a later read (which may already belong to the next submission).
func (l *Ledger) AppendEnvelope(e event.Event, status event.TurnStatus) (event.Event, Envelope, bool, error) {
	if l == nil {
		return e, Envelope{}, false, errors.New("turn event ledger is unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stamped, ok, err := l.appendLocked(e, status)
	if err != nil || !ok || stamped.Sequence == 0 {
		return stamped, Envelope{}, ok, err
	}
	if n := len(l.records); n > 0 && l.records[n-1].Sequence == stamped.Sequence {
		return stamped, l.records[n-1], true, nil
	}
	// In-memory sessions deliberately have no WAL records, but use the same
	// projection protocol and routing captured by this lock.
	kind, _ := eventwire.KindName(stamped.Kind)
	return stamped, Envelope{SchemaVersion: schemaVersion, SessionID: l.sessionID,
		TurnID: stamped.TurnID, Sequence: stamped.Sequence, ItemID: stamped.ItemID,
		AttemptID: stamped.AttemptID, RuntimeEpoch: l.routing.runtimeEpoch,
		SubmissionID: l.routing.submissionID, Source: stamped.Source, Kind: kind,
		Status: stamped.Status, CreatedAt: time.Now().UnixMilli(), Event: eventwire.ToWire(stamped),
		TranscriptDigest: l.transcript.digest, TranscriptRevision: l.transcript.revision,
		RewriteEpoch: l.transcript.rewriteEpoch}, true, nil
}

func (l *Ledger) appendLocked(e event.Event, status event.TurnStatus) (event.Event, bool, error) {
	started := time.Now()
	defer func() { l.metrics.AppendLatencyBuckets[latencyBucket(time.Since(started))]++ }()
	if l.active == "" {
		return e, true, nil
	}
	if l.terminal {
		return e, false, nil
	}
	if status == "" {
		status = l.status
	}
	next, err := nextTurnStatus(l.status, status)
	if err != nil {
		return e, false, err
	}
	status = next
	e.TurnID, e.Sequence, e.Status = l.active, l.nextSeq, status
	e.SessionID, e.RuntimeEpoch, e.SubmissionID = l.sessionID, l.routing.runtimeEpoch, l.routing.submissionID
	w := eventwire.ToWire(e)
	attemptID := e.AttemptID
	if e.Kind == event.StreamAttempt {
		attemptID = e.StreamAttempt.ID
	} else if e.Tool.AttemptID != "" {
		attemptID = e.Tool.AttemptID
	}
	kind, _ := eventwire.KindName(e.Kind)
	rec := Envelope{
		SchemaVersion: schemaVersion, SessionID: l.sessionID, TurnID: e.TurnID,
		Sequence: e.Sequence, ItemID: e.ItemID, AttemptID: attemptID,
		RuntimeEpoch: l.routing.runtimeEpoch, SubmissionID: l.routing.submissionID,
		Source: e.Source, Kind: kind, Status: status, CreatedAt: time.Now().UnixMilli(), Event: w,
		TranscriptRevision: l.transcript.revision, TranscriptDigest: l.transcript.digest,
		RewriteEpoch: l.transcript.rewriteEpoch,
	}
	l.records = append(l.records, rec)
	if e.Kind == event.Text || e.Kind == event.Reasoning {
		l.metrics.StreamRecords++
	}
	l.nextSeq++
	l.status = status
	if status == event.TurnRecoveryRequired && e.Recovery != nil {
		l.recovery = cloneRecoveryStatus(e.Recovery)
	}
	if e.Kind == event.TurnStarted {
		l.todos = nil
		l.todoWritten = false
		l.recovery = nil
	} else if e.Kind == event.ToolResult && e.Tool.TodoWritten {
		l.todos = append([]event.Todo(nil), e.Tool.Todos...)
		l.todoWritten = true
	}
	if status.Terminal() {
		l.terminal = true
		l.addSummaryLocked(rec, e.Outcome)
	}
	return e, true, nil
}

// AcknowledgeProjection records terminal display consumption.
func (l *Ledger) AcknowledgeProjection(turnID string) error {
	if l == nil || turnID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := l.terminalSequenceLocked(turnID)
	if seq == 0 {
		return fmt.Errorf("turn %s has no terminal event", turnID)
	}
	l.projectionAcks[turnID] = seq
	return nil
}

func (l *Ledger) terminalSequenceLocked(turnID string) uint64 {
	for _, rec := range slices.Backward(l.records) {
		if rec.TurnID == turnID && rec.Status.Terminal() {
			return rec.Sequence
		}
	}
	for _, summary := range slices.Backward(l.summaries) {
		if summary.TurnID == turnID {
			return summary.TerminalSequence
		}
	}
	return 0
}

func (l *Ledger) addSummaryLocked(rec Envelope, outcome string) {
	l.summaries = appendTerminalSummary(l.summaries, terminalSummaryFor(rec, outcome, l.turnStarted))
}

// Replay returns a bounded page of retained lifecycle envelopes.
func (l *Ledger) Replay(after uint64) (ReplayView, error) {
	started := time.Now()
	view := ReplayView{Events: []Envelope{}}
	if l == nil {
		return view, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	latest := l.latestLocked()
	floor := l.compactedThrough + 1
	if len(l.records) > 0 {
		floor = l.records[0].Sequence
	}
	view.FloorSequence = floor
	view.LatestSequence = latest
	view.ResetRequired = after < l.compactedThrough || after > latest
	if view.ResetRequired {
		l.metrics.ReplayResets++
	}
	view.TranscriptRevision = l.transcript.revision
	view.TranscriptDigest = l.transcript.digest
	view.RuntimeEpoch = l.routing.runtimeEpoch
	effective := after
	if effective < l.compactedThrough || effective > latest {
		effective = l.compactedThrough
	}
	view.NextAfterSequence = effective
	var pageBytes int64
	for _, rec := range l.records {
		if rec.Sequence <= effective {
			continue
		}
		encoded, _ := json.Marshal(rec)
		size := int64(len(encoded))
		if len(view.Events) >= replayMaxEvents || (len(view.Events) > 0 && pageBytes+size > replaySoftBytes) {
			break
		}
		view.Events = append(view.Events, rec)
		pageBytes += size
		view.NextAfterSequence = rec.Sequence
	}
	view.HasMore = view.NextAfterSequence < latest
	l.metrics.ReplayEvents += uint64(len(view.Events))
	l.metrics.ReplayBytes += uint64(pageBytes)
	l.metrics.ReplayLatencyBuckets[latencyBucket(time.Since(started))]++
	return view, nil
}

// EventsAfter returns retained lifecycle envelopes after a sequence.
func (l *Ledger) EventsAfter(after uint64) ([]Envelope, error) {
	if l == nil {
		return []Envelope{}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Envelope, 0)
	for _, rec := range l.records {
		if rec.Sequence > after {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (l *Ledger) pendingProjectionsLocked() []PendingProjection {
	byTurn := make(map[string][]Envelope)
	order := make([]string, 0)
	seen := make(map[string]bool)
	for _, rec := range l.records {
		if rec.TurnID == "" {
			continue
		}
		if !seen[rec.TurnID] {
			seen[rec.TurnID] = true
			order = append(order, rec.TurnID)
		}
		byTurn[rec.TurnID] = append(byTurn[rec.TurnID], rec)
	}
	out := make([]PendingProjection, 0)
	for _, turnID := range order {
		records := byTurn[turnID]
		if len(records) == 0 {
			continue
		}
		terminal := records[len(records)-1]
		if !terminal.Status.Terminal() || terminal.Sequence <= l.projectionCommittedThrough || l.projectionAcks[turnID] >= terminal.Sequence {
			continue
		}
		out = append(out, PendingProjection{TurnID: turnID, Status: terminal.Status, Events: append([]Envelope(nil), records...)})
	}
	return out
}

func (l *Ledger) latestLocked() uint64 {
	if l.nextSeq == 0 {
		return 0
	}
	return l.nextSeq - 1
}

func appendTerminalSummary(in []TerminalSummary, summary TerminalSummary) []TerminalSummary {
	for i := range in {
		if in[i].TurnID == summary.TurnID {
			in[i] = summary
			return in
		}
	}
	in = append(in, summary)
	if len(in) > terminalSummaryLimit {
		in = append([]TerminalSummary(nil), in[len(in)-terminalSummaryLimit:]...)
	}
	return in
}

func nextTurnStatus(current, requested event.TurnStatus) (event.TurnStatus, error) {
	if current == "" || current == requested {
		return requested, nil
	}
	if current.Terminal() {
		return requested, fmt.Errorf("turn is already terminal (%s)", current)
	}
	if current == event.TurnCancelling && !requested.Terminal() {
		return event.TurnCancelling, nil
	}
	valid := false
	switch current {
	case event.TurnQueued:
		valid = requested == event.TurnInProgress || requested == event.TurnWaitingUser || requested == event.TurnCancelling || requested.Terminal()
	case event.TurnInProgress:
		valid = requested == event.TurnWaitingUser || requested == event.TurnCancelling || requested.Terminal()
	case event.TurnWaitingUser:
		valid = requested == event.TurnInProgress || requested == event.TurnCancelling || requested.Terminal()
	case event.TurnCancelling:
		valid = requested.Terminal()
	}
	if !valid {
		return requested, fmt.Errorf("invalid turn status transition %s -> %s", current, requested)
	}
	return requested, nil
}

func newTurnID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "turn_" + hex.EncodeToString(raw[:]), nil
}
