package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// adoptBlankSessionAgentPreset gives a session that has not run a turn this
// controller's preset and system prompt before it is published, so opening a
// blank session is how a preset is switched.
func (c *Controller) adoptBlankSessionAgentPreset(ctx context.Context, runtime *session.Runtime) error {
	snapshot := runtime.Session().ExecutionSnapshot()
	projection := snapshot.Projection
	if err := c.checkAgentPresetLock(projection); err != nil || projection.AgentPreset == c.AgentPreset() {
		return err
	}
	events, err := c.agentPresetAdoptionEvents(projection.ModelMessages)
	if err != nil {
		return err
	}
	if _, err := runtime.Session().AppendBatch(ctx, fmt.Sprintf("agent-preset:%d", snapshot.EventSequence+1), events); err != nil {
		return err
	}
	_, err = runtime.Session().Flush(ctx)
	return err
}

func (c *Controller) agentPresetAdoptionEvents(model []provider.Message) ([]session.Event, error) {
	preset, err := agentPresetEvent(c.AgentPreset())
	if err != nil {
		return nil, err
	}
	events := []session.Event{preset}
	base := c.basePrompt()
	if len(model) == 0 || model[0].Role != provider.RoleSystem || model[0].Content == base {
		return events, nil
	}
	replaced := append([]provider.Message(nil), model...)
	replaced[0].Content = base
	payload, err := json.Marshal(map[string]any{"messages": replaced, "reason": "agent-preset"})
	if err != nil {
		return nil, err
	}
	return append(events, session.Event{Kind: "model/context-replace", Payload: payload}), nil
}

// RecordAgentPreset aligns the bound session with a rebuilt controller's
// preset. Rebuilds carry the system prompt themselves, so only the id is
// written, and only while the session is blank.
func (c *Controller) RecordAgentPreset(ctx context.Context) error {
	_, runtime, exclusive := c.v3Binding()
	if !exclusive || runtime == nil {
		return nil
	}
	projection := runtime.Session().ExecutionSnapshot().Projection
	if err := c.checkAgentPresetLock(projection); err != nil || projection.AgentPreset == c.AgentPreset() {
		return err
	}
	event, err := agentPresetEvent(c.AgentPreset())
	if err != nil {
		return err
	}
	commit, err := c.appendSessionBatch(ctx, runtime.Session(), session.Batch{
		OperationID: "agent-preset:" + agent.NewMessageID(),
		Events:      []session.Event{event},
	})
	if err != nil {
		return err
	}
	_, err = runtime.Session().FlushThrough(context.WithoutCancel(ctx), commit.LastSequence())
	return err
}

// appendFirstTurnAgentPreset commits the preset atomically with the turn that
// locks it, whichever host path bound the session.
func (c *Controller) appendFirstTurnAgentPreset(events []session.Event, projection session.Projection) ([]session.Event, error) {
	if projection.HasTurns() || projection.AgentPreset == c.AgentPreset() {
		return events, nil
	}
	event, err := agentPresetEvent(c.AgentPreset())
	return append(events, event), err
}
