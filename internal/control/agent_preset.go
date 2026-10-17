package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"deepzora/internal/session"
)

// ErrAgentPresetLocked refuses to run a session under a preset other than the
// one its history was produced with: recorded tool calls would no longer match
// the tools the model is offered.
var ErrAgentPresetLocked = errors.New("agent preset is locked once a session has run a turn")

// AgentPreset is the preset id this controller was assembled with; empty is
// the default agent. Changing it means building a new controller.
func (c *Controller) AgentPreset() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prompt.agentPreset
}

// AgentPresetLocked reads live lifecycle state without touching the filesystem.
func (c *Controller) AgentPresetLocked() bool {
	if c == nil {
		return false
	}
	if _, runtime, exclusive := c.v3Binding(); exclusive {
		return runtime != nil && runtime.Session().HasTurns()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startedOnce || c.turn > 0
}

func (c *Controller) checkAgentPresetMCPServer(server string) error {
	if !c.capabilityRuntime.AdmitsServer(server) {
		return fmt.Errorf("MCP server %q is excluded by agent preset %q", server, presetLabel(c.AgentPreset()))
	}
	return nil
}

func agentPresetEvent(id string) (session.Event, error) {
	payload, err := json.Marshal(map[string]string{"preset": id})
	return session.Event{Kind: session.AgentPresetKind, Payload: payload}, err
}

// checkAgentPresetLock is the publication guard every session path shares.
func (c *Controller) checkAgentPresetLock(projection session.Projection) error {
	recorded, mine := projection.AgentPreset, c.AgentPreset()
	if recorded == mine || !projection.HasTurns() {
		return nil
	}
	return fmt.Errorf("%w: session uses %q, this agent is %q", ErrAgentPresetLocked, presetLabel(recorded), presetLabel(mine))
}

func presetLabel(id string) string {
	if id == "" {
		return "default"
	}
	return id
}

// agentPresetAdoptionEvents refreshes custom personas on every attach and
// permits a blank session to adopt a different identity. Canonical history is
// untouched; default sessions retain their existing restore behavior.
func (c *Controller) agentPresetAdoptionEvents(projection session.Projection) ([]session.Event, []provider.Message, error) {
	if err := c.checkAgentPresetLock(projection); err != nil {
		return nil, nil, err
	}
	model := projection.ModelMessages
	var events []session.Event
	if projection.AgentPreset != c.AgentPreset() {
		preset, err := agentPresetEvent(c.AgentPreset())
		if err != nil {
			return nil, nil, err
		}
		events = append(events, preset)
	}
	if c.AgentPreset() == "" && len(events) == 0 {
		return nil, model, nil
	}
	replaced := c.agentPresetModelContext(model)
	if reflect.DeepEqual(model, replaced) {
		return events, model, nil
	}
	payload, err := json.Marshal(map[string]any{"messages": replaced, "reason": "agent-preset"})
	if err != nil {
		return nil, nil, err
	}
	return append(events, session.Event{Kind: "model/context-replace", Payload: payload}), replaced, nil
}

func (c *Controller) prepareAgentPresetPublication(ctx context.Context, runtime *session.Runtime, projection session.Projection, prepared *agent.Session) (*session.PreparedBatch, error) {
	events, model, err := c.agentPresetAdoptionEvents(projection)
	if err != nil || len(events) == 0 {
		return nil, err
	}
	batch, err := runtime.Session().PrepareBatchContext(ctx, "agent-preset:"+agent.NewMessageID(), session.Batch{TurnID: projection.TurnID, Events: events})
	if err != nil {
		return nil, err
	}
	prepared.Replace(model)
	return &batch, nil
}

// acceptAgentPresetPublication stages attach to an owned runtime on the same
// pending batch as a rebuild. Cold attach commits with its first execution bind.
func (c *Controller) acceptAgentPresetPublication(runtime *session.Runtime, pending **session.PreparedBatch) (uint64, error) {
	if *pending == nil {
		return 0, nil
	}
	c.turnEvents.commitMu.Lock()
	defer c.turnEvents.commitMu.Unlock()
	_, boundRuntime, _ := c.v3Binding()
	if boundRuntime == runtime && runtime.OwnsExecution(c.ExecutionGeneration()) {
		_, err := runtime.CommitPreparedForExecution(c.ExecutionGeneration(), **pending)
		return 0, err
	}
	if !runtime.ExecutionBound() {
		generation, _, err := runtime.ReplaceExecutionAndCommit(0, controllerExecution{c: c}, **pending)
		return generation, err
	}
	if previous := c.turnEvents.pendingExecutionCommit; previous != nil {
		previous.Release()
	}
	c.turnEvents.pendingExecutionCommit = *pending
	*pending = nil // activation or teardown now owns the reservation
	return 0, nil
}

// RecordAgentPreset stages identity with the assembled context, using the same
// activation boundary as a rebuild. Hosts normally get this through Rebuild.
func (c *Controller) RecordAgentPreset(ctx context.Context) error {
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return nil
	}
	projection := runtime.Session().ExecutionSnapshot().Projection
	if err := c.checkAgentPresetLock(projection); err != nil || projection.AgentPreset == c.AgentPreset() {
		return err
	}
	messages := c.agentPresetModelContext(projection.ModelMessages)
	return c.replaceSessionModelContext(ctx, messages, "agent-preset")
}

func (c *Controller) agentPresetModelContext(model []provider.Message) []provider.Message {
	replaced := append([]provider.Message(nil), model...)
	base := c.basePrompt()
	if len(replaced) > 0 && replaced[0].Role == provider.RoleSystem {
		replaced[0].Content = base
		return replaced
	}
	if base == "" {
		return replaced
	}
	return append(agent.NewSession(base).Snapshot(), replaced...)
}

// appendFirstTurnAgentPreset commits the preset atomically with the turn that
// locks it, whichever host path bound the session.
func (c *Controller) appendFirstTurnAgentPreset(events []session.Event, projection session.Projection) ([]session.Event, error) {
	if err := c.checkAgentPresetLock(projection); err != nil {
		return nil, err
	}
	if projection.HasTurns() || projection.AgentPreset == c.AgentPreset() {
		return events, nil
	}
	event, err := agentPresetEvent(c.AgentPreset())
	return append(events, event), err
}
