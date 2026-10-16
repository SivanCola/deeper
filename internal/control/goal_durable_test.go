package control

import (
	"deepzora/internal/agent"
	"deepzora/internal/event"
	"testing"
)

func TestCurrentGoalWriteFailurePreservesGoalAndComposerProfile(t *testing.T) {
	for _, profile := range []bool{false, true} {
		t.Run(map[bool]string{false: "goal", true: "profile"}[profile], func(t *testing.T) {
			exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
			c := newCurrentOwnedTestController(t, Options{Executor: exec, Sink: event.Discard})
			if err := c.SetGoalDurable("keep the goal"); err != nil {
				t.Fatal(err)
			}
			c.SetPlanMode(true)
			c.SetToolApprovalMode(ToolApprovalWorkspaceWrite)
			want := c.GoalRuntime()
			if err := c.sessionEventStore().Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			var err error
			if profile {
				_, err = c.ApplyComposerProfile(false, ToolApprovalDangerFullAccess, "replace goal")
			} else {
				err = c.SetGoalDurable("replace goal")
			}
			if err == nil {
				t.Fatal("closed current storage accepted goal update")
			}
			if got := c.GoalRuntime(); got != want {
				t.Fatalf("failed update changed goal: got=%+v want=%+v", got, want)
			}
			if !c.PlanMode() || c.ToolApprovalMode() != ToolApprovalWorkspaceWrite {
				t.Fatal("failed goal update changed composer profile")
			}
		})
	}
}

func TestCurrentComposerProfilePreservesMatchingPausedGoal(t *testing.T) {
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	c := newCurrentOwnedTestController(t, Options{Executor: exec, Sink: event.Discard})
	if err := c.SetGoalDurable("keep the goal"); err != nil {
		t.Fatal(err)
	}
	if !c.PauseGoal() {
		t.Fatal("goal did not pause")
	}
	want := c.GoalRuntime()
	if _, err := c.ApplyComposerProfile(false, ToolApprovalDangerFullAccess, "keep the goal"); err != nil {
		t.Fatal(err)
	}
	if got := c.GoalRuntime(); got != want {
		t.Fatalf("matching profile reset paused goal: got=%+v want=%+v", got, want)
	}
	if c.ToolApprovalMode() != ToolApprovalDangerFullAccess {
		t.Fatal("profile did not change permission mode")
	}
}
