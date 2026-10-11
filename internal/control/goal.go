package control

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"deepzora/internal/evidence"
	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/store"
)

const (

	// Bound the persisted novelty window. Signatures are compact hashes, and
	// retaining the most recent window is enough to stop short repeat cycles
	// without allowing an unbounded Goal sidecar.
	maxGoalProgressEvidence = 512
)

// Stop causes distinguish a spend pause from a manual pause.
const (
	stopCauseBudgetSpend = "budget_spend"
	stopCauseManual      = "manual"
)

// goalMachine owns the active goal FSM and its persistence. It is a strict
// leaf: methods take only machine locks and never call back into Controller.
// advance() takes already-gathered inputs so no disk/executor work holds mu.
type goalMachine struct {
	// mu guards the FSM fields below; every critical section under it is short
	// and non-blocking (no disk I/O, no executor calls).
	mu                 sync.Mutex
	goal               string
	status             string
	scopeID            string
	deliveryCheckpoint evidence.DeliveryCheckpoint
	block              string
	continuationEpoch  uint64

	tokenBudget int // configured ceiling for an unattended loop; 0 = unbounded

	// Runtime statistics and optional user-selected spend state, persisted
	// across turns and restarts. turnsUsed and noProgressTurns are observational;
	// tokensLimit is non-zero only when the user configured a Goal token budget.
	turnsUsed              int
	tokensUsed             int
	requestsUsed           int
	workDurationMs         int64
	tokensLimit            int // ceiling of the current user-selected spend slice
	noProgressTurns        int
	lastContinuationReason string
	launch                 goalLaunchState
	goalActivationState
	lastEvaluatorReason string
	stopCause           string
	budgetExtensions    int // number of user-authorized spend extensions
	progressEvidence    []string
	// stateExtra preserves fields written by a newer peer during read/modify/
	// write cycles. Known current fields always win on serialization.
	stateExtra map[string]json.RawMessage

	// statePath is the persisted goal-state sidecar; empty disables persistence.
	statePath string
	// writeMu serializes goal-state disk writes so concurrent saves don't
	// interleave or land out of order. Taken OFF mu by writeState.
	writeMu sync.Mutex
}

// goalState is the current persisted goal projection. Todo state belongs to
// the current turn and is not part of a goal record.
type goalState struct {
	SchemaVersion          int                         `json:"schemaVersion"`
	Goal                   string                      `json:"goal,omitempty"`
	Status                 string                      `json:"status,omitempty"`
	ScopeID                string                      `json:"scopeID,omitempty"`
	DeliveryCheckpoint     evidence.DeliveryCheckpoint `json:"deliveryCheckpoint,omitempty"`
	Block                  string                      `json:"block,omitempty"`
	TurnsUsed              int                         `json:"turnsUsed,omitempty"`
	TokensUsed             int                         `json:"tokensUsed,omitempty"`
	RequestsUsed           int                         `json:"requestsUsed,omitempty"`
	WorkDurationMs         int64                       `json:"workDurationMs,omitempty"`
	TokensLimit            int                         `json:"tokensLimit,omitempty"`
	NoProgressTurns        int                         `json:"noProgressTurns,omitempty"`
	LastContinuationReason string                      `json:"lastContinuationReason,omitempty"`
	LastEvaluatorReason    string                      `json:"lastEvaluatorReason,omitempty"`
	StopCause              string                      `json:"stopCause,omitempty"`
	BudgetExtensions       int                         `json:"budgetExtensions,omitempty"`
	ProgressEvidence       []string                    `json:"progressEvidence,omitempty"`
}

// goalStatePath derives a session's persisted goal-state sidecar.
func goalStatePath(sessionPath string) string {
	return store.SessionGoalState(sessionPath)
}

func (g *goalMachine) setStatePath(path string) {
	g.mu.Lock()
	g.statePath = path
	g.mu.Unlock()
}

// snapshot returns the fields Compose injects into outgoing turns.
func (g *goalMachine) snapshot() (goal, status string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disarmed && g.status == GoalStatusRunning {
		return g.goal, GoalStatusStopped
	}
	return g.goal, g.status
}

func (g *goalMachine) goalText() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.goal
}

// continuationToken captures the Goal lifecycle that owns an outgoing turn.
// The matching assistant output may advance the FSM only while this epoch is
// still current.
func (g *goalMachine) continuationToken() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.continuationEpoch
}

func (g *goalMachine) deliveryScope() (id, task string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disarmed || strings.TrimSpace(g.goal) == "" || g.status != GoalStatusRunning {
		return "", "", false
	}
	if g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	return g.scopeID, g.goal, true
}

func newGoalScopeID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return fmt.Sprintf("goal-%x", raw[:])
	}
	return fmt.Sprintf("goal-fallback-%d-%d", os.Getpid(), time.Now().UnixNano())
}

// active reports whether a goal is currently running.
func (g *goalMachine) active() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.disarmed && strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning
}

// statusForDisplay maps the empty zero status to "stopped" for frontends.
func (g *goalMachine) statusForDisplay() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.status == "" || g.disarmed && g.status == GoalStatusRunning {
		return GoalStatusStopped
	}
	return g.status
}

// set installs a session-scoped goal (or clears it when goal is empty), resets
// the per-goal runtime counters, and returns the state to persist. ok is
// false (no persistence) when the goal is unchanged or no state path is
// configured.
func (g *goalMachine) set(goal string) (string, []byte, bool) {
	goal = strings.TrimSpace(goal)
	g.mu.Lock()
	defer g.mu.Unlock()
	if goal != "" && g.goal == goal && g.status == GoalStatusRunning {
		if !g.disarmed {
			return "", nil, false
		}
		// Explicitly starting the restored objective preserves its identity and
		// actual accumulated usage, rather than creating a fresh budget.
		g.disarmed = false
		g.continuationEpoch++
		return g.buildStateLocked()
	}
	g.installGoalLocked(goal)
	return g.buildStateLocked()
}

func (g *goalMachine) installGoalLocked(goal string) {
	g.disarmed = false
	g.continuationEpoch++
	g.turnsUsed, g.tokensUsed, g.requestsUsed, g.noProgressTurns = 0, 0, 0, 0
	g.workDurationMs = 0
	g.block = ""
	g.lastContinuationReason, g.lastEvaluatorReason = "", ""
	g.stopCause = ""
	g.budgetExtensions = 0
	g.progressEvidence = nil
	if goal == "" {
		g.goal, g.status = "", GoalStatusStopped
		g.scopeID = ""
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{}
	} else {
		g.goal, g.status = goal, GoalStatusRunning
		g.scopeID = newGoalScopeID()
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{ScopeID: g.scopeID}
		g.tokensLimit = g.tokenBudget
	}

}

// stop transitions a running goal to the given terminal status and clears the
// transient runtime bookkeeping. stopCause is cleared: a host stop is not a
// safe pause.
func (g *goalMachine) stop(status string) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.continuationEpoch++
	if strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning {
		g.status = status
	}
	g.stopCause = ""
	g.noProgressTurns = 0
	return g.buildStateLocked()
}

// pauseFor transitions a running goal to a safe pause: status blocked plus a
// stop cause, keeping every runtime counter for a later resume.
func (g *goalMachine) pauseFor(stopCause, reason string) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.continuationEpoch++
	if strings.TrimSpace(g.goal) != "" && g.status == GoalStatusRunning {
		g.status = GoalStatusBlocked
	}
	g.stopCause = stopCause
	if reason != "" {
		g.block = reason
	}
	return g.buildStateLocked()
}

// resume re-enters a recoverable blocked/stopped goal without resetting scope
// or runtime history. Continuous Goals never extend a numeric quota.
func (g *goalMachine) resume() (path string, data []byte, persist, resumed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if strings.TrimSpace(g.goal) == "" || g.status == GoalStatusComplete {
		return "", nil, false, false
	}
	// A user-selected spend pause grants one fresh configured slice. Usage
	// remains cumulative; only the absolute threshold moves forward.
	spentBudget := g.stopCause == stopCauseBudgetSpend
	g.continuationEpoch++
	g.status = GoalStatusRunning
	g.disarmed = false
	g.block = ""
	g.stopCause = ""
	g.noProgressTurns = 0
	g.budgetExtensions = 0
	g.grantSpendSliceLocked(spentBudget)
	if g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	path, data, persist = g.buildStateLocked()
	return path, data, persist, true
}

func (g *goalMachine) setDeliveryCheckpoint(checkpoint evidence.DeliveryCheckpoint) (string, []byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scopeID == "" || checkpoint.ScopeID != g.scopeID {
		return "", nil, false
	}
	g.deliveryCheckpoint = checkpoint
	return g.buildStateLocked()
}

func (g *goalMachine) deliveryState() evidence.DeliveryCheckpoint {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliveryCheckpoint
}

// buildStateLocked marshals the current goal state for persistence. The caller
// holds mu; this only reads in-memory state, never touching disk. Returns ok=false
// when persistence is disabled (no state path). The matching writeState does the
// disk write OFF mu so the per-turn save can't stall a status poll.
func (g *goalMachine) buildStateLocked() (path string, data []byte, ok bool) {
	if g.statePath == "" {
		return "", nil, false
	}
	b, ok := g.marshalStateLocked()
	return g.statePath, b, ok
}

func (g *goalMachine) eventState() ([]byte, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.marshalStateLocked()
}

func (g *goalMachine) marshalStateLocked() ([]byte, bool) {
	state := goalState{
		SchemaVersion:          1,
		Goal:                   g.goal,
		Status:                 g.status,
		ScopeID:                g.scopeID,
		DeliveryCheckpoint:     g.deliveryCheckpoint,
		Block:                  g.block,
		TurnsUsed:              g.turnsUsed,
		TokensUsed:             g.tokensUsed,
		RequestsUsed:           g.requestsUsed,
		WorkDurationMs:         g.workDurationMs,
		TokensLimit:            g.tokensLimit,
		NoProgressTurns:        g.noProgressTurns,
		LastContinuationReason: g.lastContinuationReason,
		LastEvaluatorReason:    g.lastEvaluatorReason,
		StopCause:              g.stopCause,
		BudgetExtensions:       g.budgetExtensions,
		ProgressEvidence:       append([]string(nil), g.progressEvidence...),
	}
	b, err := marshalGoalState(state, g.stateExtra)
	if err != nil {
		slog.Warn("controller: marshal goal state", "err", err)
		return nil, false
	}
	return b, true
}

// writeState preserves the existing best-effort behavior for background Goal
// progress. Callers that need transactional persistence use writeStateErr.
func (g *goalMachine) writeState(path string, data []byte) {
	if err := g.writeStateErr(path, data); err != nil {
		slog.Warn("controller: write goal state", "err", err)
	}
}

func (g *goalMachine) restoreFromState(sessionPath string) {
	if strings.TrimSpace(sessionPath) == "" {
		return
	}
	if g.statePath == "" {
		g.setStatePath(goalStatePath(sessionPath))
	}
	raw, err := fileencoding.ReadFileUTF8(goalStatePath(sessionPath))
	if err != nil {
		return
	}
	state, decodeErr := decodeGoalState(raw)
	if decodeErr != nil {
		return
	}
	g.restoreDecodedState(raw, state)
}

// restoreGoalEvent installs a persisted v3 goal projection without reviving
// an execution loop. Goal events retain objective, status and budgets, while
// Todo remains a separate current-turn projection.
func (g *goalMachine) restoreGoalEvent(raw []byte) error {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		raw = []byte(`{"schemaVersion":1}`)
	}
	var discriminator map[string]json.RawMessage
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return err
	}
	if _, lifecycle := discriminator["version"]; lifecycle {
		if _, err := goalLifecycleFromProjection(raw); err != nil {
			return err
		}
		// The current human-directed lifecycle owns its own projection. It does
		// not activate the autonomous execution machine during a cold restore.
		g.restoreDecodedState(nil, goalState{SchemaVersion: 1})
		return nil
	}
	state, err := decodeGoalState(raw)
	if err != nil {
		return err
	}
	g.restoreDecodedState(raw, state)
	return nil
}

func (g *goalMachine) restoreDecodedState(raw []byte, state goalState) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stateExtra = goalStateUnknownFields(raw)
	g.goal = strings.TrimSpace(state.Goal)
	g.disarmed = true
	g.status = state.Status
	if g.status == "" {
		g.status = GoalStatusStopped
	}
	g.scopeID = strings.TrimSpace(state.ScopeID)
	if g.scopeID == "" {
		g.scopeID = strings.TrimSpace(state.DeliveryCheckpoint.ScopeID)
	}
	if g.goal != "" && g.scopeID == "" {
		g.scopeID = newGoalScopeID()
	}
	g.deliveryCheckpoint = state.DeliveryCheckpoint
	if g.scopeID == "" {
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{}
	} else if g.deliveryCheckpoint.ScopeID == "" {
		g.deliveryCheckpoint.ScopeID = g.scopeID
	} else if g.deliveryCheckpoint.ScopeID != g.scopeID {
		g.deliveryCheckpoint = evidence.DeliveryCheckpoint{ScopeID: g.scopeID}
	}
	g.block = state.Block
	g.stopCause = state.StopCause
	g.budgetExtensions = state.BudgetExtensions
	g.progressEvidence, _ = mergeGoalProgressEvidence(nil, state.ProgressEvidence)
	g.lastContinuationReason = state.LastContinuationReason
	g.lastEvaluatorReason = state.LastEvaluatorReason
	// Restore the current usage counters.
	g.turnsUsed = state.TurnsUsed

	g.tokensUsed = state.TokensUsed
	g.requestsUsed = state.RequestsUsed
	g.workDurationMs = state.WorkDurationMs
	g.noProgressTurns = state.NoProgressTurns
	g.tokensLimit = state.TokensLimit
	g.continuationEpoch++
}

// clipGoalReason bounds a recorded reason for storage and display.
func clipGoalReason(reason string) string {
	reason = strings.TrimSpace(reason)
	const max = 400
	if r := []rune(reason); len(r) > max {
		return string(r[:max]) + "..."
	}
	return reason
}

// ShortGoalForNotice collapses whitespace and truncates a goal for one-line UI.
func ShortGoalForNotice(goal string) string {
	goal = strings.Join(strings.Fields(goal), " ")
	runes := []rune(goal)
	const max = 160
	if len(runes) <= max {
		return goal
	}
	return string(runes[:max]) + "..."
}

// persistGoalState writes a freshly built goal state to disk, off c.mu. The
// executor guard preserves the original behavior of skipping persistence when
// no executor is attached.
func (c *Controller) persistGoalState(path string, data []byte, ok bool) {
	if !ok && c.sessionEngineEnabled() {
		data, ok = c.goals.eventState()
	}
	if !ok || c.executor == nil {
		return
	}
	if err := c.appendDomainState("goal/state", data, "goal-update"); err != nil {
		slog.Warn("controller: append goal state event", "err", err)
		c.failTurnEventLedger(err)
		return
	}
	c.goals.writeState(path, data)
}

func (c *Controller) persistGoalStateAtEpoch(epoch uint64) (bool, error) {
	applied, err := c.goals.writeStateAtEpoch(epoch)
	if err != nil {
		slog.Warn("controller: write goal state", "err", err)
	}
	return applied, err
}
