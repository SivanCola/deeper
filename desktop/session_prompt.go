package main

import (
	"fmt"
	"strings"

	"deepzora/internal/control"
)

// configureControllerRuntime applies the non-persisted runtime posture before
// Resume. Session grants are copied before a lease is acquired so a replacement
// is fully configured but cannot run against the session until ownership is
// established.
func configureControllerRuntime(ctrl, oldCtrl control.SessionAPI, runtime normalizedTabRuntime) {
	if ctrl == nil {
		return
	}
	ctrl.EnableInteractiveApproval()
	applyTabModeToController(ctrl, runtime.tabMode())
	applyTabToolApprovalModeToController(ctrl, runtime.toolApprovalMode)
	if next, ok := ctrl.(*control.Controller); ok {
		if prev, ok := oldCtrl.(*control.Controller); ok {
			next.RestoreSessionAuthorizations(prev.SessionAuthorizations())
		}
	}
}

func normalizeRestoredControllerRuntime(ctrl control.SessionAPI, requested normalizedTabRuntime) (normalizedTabRuntime, error) {
	if ctrl == nil {
		return normalizedTabRuntime{}, fmt.Errorf("replacement controller is nil")
	}
	plan := requested.collaborationMode == "plan"
	ctrl.SetPlanMode(plan)
	applyTabToolApprovalModeToController(ctrl, requested.toolApprovalMode)
	if plan && ctrl.GoalStatus() == control.GoalStatusRunning {
		// Explicit Plan wins over inconsistent legacy data. Clearing the running
		// Goal also prevents a stale scope from being executed after approval.
		if err := ctrl.SetGoalDurable(""); err != nil {
			return normalizedTabRuntime{}, fmt.Errorf("clear goal for Plan mode: %w", err)
		}
	}

	actual := requested
	actual.goal = ""
	actual.collaborationMode = "normal"
	switch {
	case ctrl.PlanMode():
		actual.collaborationMode = "plan"
	case ctrl.GoalStatus() == control.GoalStatusRunning && strings.TrimSpace(ctrl.Goal()) != "":
		actual.collaborationMode = "goal"
		actual.goal = strings.TrimSpace(ctrl.Goal())
	}
	actual.toolApprovalMode = normalizeToolApprovalMode(ctrl.ToolApprovalMode())
	if ctrl.PlanMode() != (actual.collaborationMode == "plan") {
		return normalizedTabRuntime{}, fmt.Errorf("replacement collaboration mode validation failed")
	}
	if actual.toolApprovalMode != normalizeToolApprovalMode(requested.toolApprovalMode) {
		return normalizedTabRuntime{}, fmt.Errorf("replacement tool approval mode = %q, want %q", actual.toolApprovalMode, requested.toolApprovalMode)
	}
	return actual, nil
}
