package control

import (
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/event"
	goaldomain "deepzora/internal/goal"
)

func TestCurrentRuntimeRestoresBlockedGoalWithoutConversation(t *testing.T) {
	old, service, runtime := exclusiveTestController(t, event.Discard)
	if err := old.SetGoalDurable("preserve the zero-turn goal"); err != nil {
		t.Fatal(err)
	}
	view, err := old.goalLifecycleView()
	if err != nil || view == nil {
		t.Fatalf("goal was not created: %v", err)
	}
	if _, err := old.applyHostGoalMutation(t.Context(), "block", func(machine *goaldomain.Machine) (*goaldomain.View, error) {
		blocked, err := machine.Block(view.Ref(), goaldomain.BlockReason{Code: "needs-input", Message: "waiting for input"}, true, 0)
		return &blocked, err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Session().Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	old.ReleaseResources()
	<-old.Closed()
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	replacement := newOwnedTestController(t, Options{
		Executor: exec, Sink: event.Discard,
		SessionService: service, SessionRuntime: runtime, ExclusiveSession: true,
	})
	if got := replacement.Goal(); got != "preserve the zero-turn goal" {
		t.Fatalf("restored goal = %q", got)
	}
	if got := replacement.GoalStatus(); got != GoalStatusBlocked {
		t.Fatalf("restored goal status = %q, want blocked", got)
	}
	if ref, ok := replacement.SessionRef(); !ok || ref != runtime.Ref() {
		t.Fatal("goal restoration changed the current session identity")
	}
}
