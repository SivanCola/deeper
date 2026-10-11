package planmode

import (
	"strings"
	"testing"
)

func TestDecideLeavesSafetyToPermissionsAndSandbox(t *testing.T) {
	p := Policy{}
	for _, call := range []Call{
		{Name: "read_file"},
		{Name: "write_file"},
		{Name: "bash"},
		{Name: "mcp__srv__query"},
		{Name: "mcp__srv__write"},
		{Name: "self_reported_writer", Safety: PlanSafetySafe},
	} {
		if got := p.Decide(call); got.Blocked {
			t.Errorf("ordinary call %q was phase-blocked: %s", call.Name, got.Message)
		}
	}
}

func TestDecideBlocksOnlyExplicitPhaseOptOut(t *testing.T) {
	got := (Policy{}).Decide(Call{Name: "complete_step", Safety: PlanSafetyUnsafe})
	if !got.Blocked || !strings.Contains(got.Message, "planning workflow") {
		t.Fatalf("complete_step decision = %+v", got)
	}

	got = (Policy{}).Decide(Call{Name: "custom_phase_tool", Safety: PlanSafetyUnsafe})
	if !got.Blocked || !strings.Contains(got.Message, "planning workflow") {
		t.Fatalf("custom phase opt-out decision = %+v", got)
	}
}
