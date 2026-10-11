package control

import (
	"context"
	"deepzora/internal/agent"
	"testing"
)

func TestFactDrivenPlannerIsExplicitOnly(t *testing.T) {
	if (DecidePlannerRoute(context.Background(), "解释 OAuth token").Route != agent.PlannerRouteExecutorOnly) || (DecidePlannerRoute(context.Background(), "复杂重构认证迁移").Route != agent.PlannerRouteExecutorOnly) {
		t.Fatal("ordinary wording must stay executor-only")
	}
	if !(DecidePlannerRoute(context.Background(), "先规划再执行认证迁移").Route != agent.PlannerRouteExecutorOnly) {
		t.Fatal("explicit plan-then-execute must call planner")
	}
}

func TestFactDrivenConstraintsIgnoreComplexityWords(t *testing.T) {
	// ParseConstraints lives in runtimepolicy; this covers the controller path
	// that must not auto-plan on complexity words.
	if DecidePlannerRoute(context.Background(), "复杂重构认证迁移 OAuth token database").Route != agent.PlannerRouteExecutorOnly {
		t.Fatal("complexity words must not invoke the planner")
	}
}
