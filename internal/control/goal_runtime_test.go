package control

import (
	"context"
	"encoding/json"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/testutil"
	"deepzora/internal/tool"
)

// goalRuntimeController wires a controller with a scripted reporting model.

func goalRuntimeController(t *testing.T, prov provider.Provider) (*Controller, *agent.Agent, <-chan event.Event) {
	t.Helper()
	return goalRuntimeControllerWithTokenBudget(t, prov, 0)
}

func goalRuntimeControllerWithTokenBudget(t *testing.T, prov provider.Provider, tokens int) (*Controller, *agent.Agent, <-chan event.Event) {
	t.Helper()
	ag := agent.New(prov, goalRegistry(), agent.NewSession(""), agent.Options{}, event.Discard)
	events := make(chan event.Event, 8)
	c := newOwnedTestController(t, Options{
		Runner:          ag,
		Executor:        ag,
		GoalTokenBudget: tokens,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone || e.Kind == event.Notice {
				events <- e
			}
		}),
	})
	return c, ag, events
}

// TestGoalUsageTotalTokensFallback checks the prompt+completion fallback when
// TotalTokens is missing (never double-counting cache hit/miss).
func TestGoalUsageTotalTokensFallback(t *testing.T) {
	u := &provider.Usage{PromptTokens: 100, CompletionTokens: 20, CacheHitTokens: 90}
	if got := usageTotalTokens(u); got != 120 {
		t.Fatalf("fallback = %d, want 120 (prompt+completion, no cache double count)", got)
	}
	u.TotalTokens = 200
	if got := usageTotalTokens(u); got != 200 {
		t.Fatalf("TotalTokens preferred = %d, want 200", got)
	}
}

// TestGoalPauseResumeCommands covers the /goal pause and /goal resume CLI
// surface plus the runtime view.
func TestGoalPauseResumeCommands(t *testing.T) {
	cmd, ok := ParseGoalCommand("/goal pause")
	if !ok || cmd.Action != GoalCommandPause {
		t.Fatalf("ParseGoalCommand(/goal pause) = %+v", cmd)
	}
	cmd, ok = ParseGoalCommand("/goal resume")
	if !ok || cmd.Action != GoalCommandResume {
		t.Fatalf("ParseGoalCommand(/goal resume) = %+v", cmd)
	}
	cmd, ok = ParseGoalCommand("/goal")
	if !ok || cmd.Action != GoalCommandStatus {
		t.Fatalf("ParseGoalCommand(/goal) = %+v", cmd)
	}

	c := newOwnedTestController(t, Options{Sink: event.Discard})
	if c.PauseGoal() {
		t.Fatal("PauseGoal without a goal must return false")
	}
	c.SetGoal("long-running research")
	if !c.PauseGoal() {
		t.Fatal("PauseGoal on a running goal must return true")
	}
	if got := c.GoalStatus(); got != GoalStatusBlocked {
		t.Fatalf("GoalStatus() = %q, want blocked", got)
	}
	if rt := c.GoalRuntime(); rt.StopCause != stopCauseManual {
		t.Fatalf("StopCause = %q, want manual", rt.StopCause)
	}
	// The goal text and budget survive the pause.
	if got := c.Goal(); got != "long-running research" {
		t.Fatalf("Goal() = %q, want preserved", got)
	}
	if !c.ResumeGoal() {
		t.Fatal("ResumeGoal on a manually paused goal must return true")
	}
	if got := c.GoalStatus(); got != GoalStatusRunning {
		t.Fatalf("GoalStatus() after resume = %q, want running", got)
	}
	if rt := c.GoalRuntime(); rt.StopCause != "" {
		t.Fatalf("StopCause after resume = %q, want cleared", rt.StopCause)
	}
}

// TestGoalRuntimeViewPopulatesFromController covers the runtime view surface
// the CLI and desktop read.
func TestGoalRuntimeViewPopulatesFromController(t *testing.T) {
	c := newOwnedTestController(t, Options{Sink: event.Discard})
	c.SetGoal("finish the migration")
	rt := c.GoalRuntime()
	if rt.TurnsUsed != 0 {
		t.Fatalf("runtime view = %+v, want continuous defaults", rt)
	}
	if rt.TokensLimit != 0 {
		t.Fatalf("TokensLimit = %d, want 0 (no hard token limit)", rt.TokensLimit)
	}
}

// minimalFakeTool is a no-op tool for delivery-flow tests.
type minimalFakeTool struct {
	name     string
	readOnly bool
}

func (f minimalFakeTool) Name() string            { return f.name }
func (f minimalFakeTool) Description() string     { return "" }
func (f minimalFakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f minimalFakeTool) ReadOnly() bool          { return f.readOnly }
func (f minimalFakeTool) Execute(context.Context, json.RawMessage) (string, error) {
	return f.name + " done", nil
}

// TestOrdinaryTurnDoesNotCreateRecoveryCard covers a historical
// delivery value on an ordinary, non-Goal turn.
func TestOrdinaryTurnDoesNotCreateRecoveryCard(t *testing.T) {
	todoWrite, _ := tool.LookupBuiltin("todo_write")
	reg := tool.NewRegistry()
	reg.Add(todoWrite)
	reg.Add(minimalFakeTool{name: "write_file"})
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		{testutil.ToolCallChunk("w1", "write_file", `{"path":"main.go"}`), {Type: provider.ChunkDone}},
		{testutil.ToolCallChunk("t0", "todo_write", `{"todos":[{"content":"Ship main","status":"in_progress"}]}`), {Type: provider.ChunkDone}},
		textTurn("premature final"),
		textTurn("must not be consumed by a hidden readiness retry"),
	}}
	// "implement main" is an unanchored mutation. The retired delivery value
	// must not turn its evidence gap into a current pause.
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)
	done := make(chan event.Event, 1)
	c := newOwnedTestController(t, Options{
		Runner:   ag,
		Executor: ag,
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.TurnDone {
				done <- e
			}
		}),
	})

	c.Submit("implement main")
	<-done

	if prov.call != 3 {
		t.Fatalf("provider calls = %d, want 3 (work + todo + final answer)", prov.call)
	}
	if got := c.GoalStatus(); got != GoalStatusStopped {
		t.Fatalf("GoalStatus() = %q, want stopped (no goal involved)", got)
	}
}
