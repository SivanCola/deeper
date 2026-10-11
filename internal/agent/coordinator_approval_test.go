package agent

import (
	"context"
	"strings"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

// TestCoordinatorPersistsDeniedPlanTurnToExecutorSession pins the denial
// bookkeeping: a plan the user declines must still land in the executor
// session (like the no-op path) so the turn survives save/reload, with a note
// telling the next executor turn that nothing ran, plus a user-facing notice.
func TestCoordinatorPersistsDeniedPlanTurnToExecutorSession(t *testing.T) {
	planner := &mockProvider{name: "planner", chunks: submitPlanChunk(`{"objective":"rewrite auth","requires_approval":true,"steps":[{"title":"rewrite auth"}]}`)}
	exec := &mockProvider{name: "executor", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "should not run"},
		{Type: provider.ChunkDone},
	}}
	sink := &recordSink{}
	executor := New(exec, tool.NewRegistry(), NewSession("exec-sys"), Options{}, sink)
	coord := NewCoordinatorWithPlannerPolicy(planner, NewSession("planner-sys"), nil, plannerRegistryWithSubmitPlan(), Options{}, executor, 0, sink, nil)
	gate := &coordinatorApprovalGate{allow: false}
	coord.SetPlannerPlanApprover(gate)

	if err := coord.Run(withNoClosedLoop(context.Background()), "rewrite auth"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gate.calls != 1 {
		t.Fatalf("approval gate calls = %d, want 1", gate.calls)
	}
	if len(exec.requests) != 0 {
		t.Fatal("executor must not run when the plan is denied")
	}
	msgs := executor.sess.conversation.Messages
	if len(msgs) < 2 {
		t.Fatalf("executor session messages = %d, want the denied turn persisted", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant || !strings.Contains(last.Content, plannerPlanNotApprovedNote) {
		t.Fatalf("last executor message = %q (%s), want plan with not-approved note", last.Content, last.Role)
	}
	prev := msgs[len(msgs)-2]
	if prev.Role != provider.RoleUser || !strings.Contains(prev.Content, "rewrite auth") {
		t.Fatalf("persisted user turn = %q (%s), want original input", prev.Content, prev.Role)
	}
	foundNotice := false
	for _, e := range sink.kinds(event.Notice) {
		if strings.Contains(e.Text, "not approved") {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatal("denied plan should emit a user-facing notice")
	}
}

// TestCoordinatorApprovalGateReadsOnlyTheField pins the negation side of the
// field-only contract: a plan that rules out an approval round in prose (and in
// its requires_approval field) hands off directly instead of raising a
// needless approval prompt.
func TestCoordinatorApprovalGateReadsOnlyTheField(t *testing.T) {
	planner := &mockProvider{name: "planner", chunks: submitPlanChunk(`{"objective":"修改 config","steps":[{"title":"修改 config.go，无需等待用户批准，直接执行修改"}]}`)}
	exec := &mockProvider{name: "executor", chunks: []provider.Chunk{
		{Type: provider.ChunkText, Text: "Done."},
		{Type: provider.ChunkDone},
	}}
	executor := New(exec, tool.NewRegistry(), NewSession("exec-sys"), Options{}, event.Discard)
	coord := NewCoordinatorWithPlannerPolicy(planner, NewSession("planner-sys"), nil, plannerRegistryWithSubmitPlan(), Options{}, executor, 0, event.Discard, nil)
	gate := &coordinatorApprovalGate{allow: false}
	coord.SetPlannerPlanApprover(gate)

	if err := coord.Run(withNoClosedLoop(context.Background()), "tweak config"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gate.calls != 0 {
		t.Fatalf("approval gate calls = %d, want 0 for requires_approval=false", gate.calls)
	}
	if len(exec.requests) == 0 {
		t.Fatal("executor should run directly for a plan that does not require approval")
	}
}
