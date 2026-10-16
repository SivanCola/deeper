package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/provider"
	"deepzora/internal/tool"
)

const restartMaintenanceWindow = 1_000_000

// largeRestartToolHistory builds a realistic long coding session from capped
// tool results. Reusing the same immutable string keeps the test allocation
// small while every message still contributes to the provider request size.
func largeRestartToolHistory(results int) []provider.Message {
	result := strings.Repeat("x", 32*1024)
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "long-running task"},
	}
	for i := range results {
		id := fmt.Sprintf("tool-%d", i)
		messages = append(messages,
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: "{}"}}},
			provider.Message{Role: provider.RoleTool, ToolCallID: id, Name: "read_file", Content: result},
		)
	}
	return append(messages,
		provider.Message{Role: provider.RoleUser, Content: "write the handover now"},
		provider.Message{Role: provider.RoleAssistant, Content: "working"},
	)
}

func newRestartMaintenanceAgent(state *currentPersistenceSession, prov provider.Provider, sink event.Sink) *Agent {
	// Explicit 0.80 matches the v1.22.0 user scenario and the plan's regression
	// fixture. New configs default to 0.80; explicit legacy ratios remain unchanged.
	return New(prov, tool.NewRegistry(), state.conversation(), Options{
		ContextWindow:       restartMaintenanceWindow,
		CompactRatio:        0.80,
		SessionCheckpointer: state.handle,
		WorkspaceID:         "workspace",
		ModelRef:            "provider/model",
	}, sink)
}

func appliedMaintenanceEvents(sink *recordSink) int {
	count := 0
	for _, got := range sink.kinds(event.ContextMaintenanceEvent) {
		if got.Maintenance != nil && got.Maintenance.Status == "applied" {
			count++
		}
	}
	return count
}

// 80 capped tool results put the request just above 60% of a one-million-token
// window. In v1.22.0 that crossed tool_result_snip_ratio, rewrote the prompt,
// and did so again after reopening. compact_ratio=80% must be the sole trigger.
func TestContextMaintenanceWaitsForCompactRatioAcrossRestart(t *testing.T) {
	messages := largeRestartToolHistory(80)
	state := newCurrentPersistenceSession(t, &Session{Messages: messages})

	firstSink := &recordSink{}
	first := newRestartMaintenanceAgent(state, nil, firstSink)
	before := first.ContextMaintenanceSnapshot()
	if before.ProjectedTokens < 600_000 || before.ProjectedTokens >= before.TriggerTokens {
		t.Fatalf("fixture prompt/trigger = %d/%d, want prompt in [600000, trigger)", before.ProjectedTokens, before.TriggerTokens)
	}
	if _, err := first.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatal(err)
	}
	if got := first.currentProjectionVersion(); got != 0 {
		t.Fatalf("60%% prompt installed projection version %d below compact_ratio", got)
	}
	if got := appliedMaintenanceEvents(firstSink); got != 0 {
		t.Fatalf("60%% prompt emitted %d applied maintenance events", got)
	}

	reopenedSink := &recordSink{}
	state.reopen(t, state.ref)
	reopened := newRestartMaintenanceAgent(state, nil, reopenedSink)
	if _, err := reopened.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatal(err)
	}
	if got := reopened.currentProjectionVersion(); got != 0 {
		t.Fatalf("reopened 60%% prompt installed projection version %d", got)
	}
	if got := appliedMaintenanceEvents(reopenedSink); got != 0 {
		t.Fatalf("reopened 60%% prompt emitted %d applied maintenance events", got)
	}
}

// Once the prompt really crosses 80%, one summary checkpoint is installed.
// Reopening restores the committed current workset and maintenance receipt
// without re-summarizing or replaying frontend events.
func TestContextMaintenanceRestoresAppliedProjectionWithoutReapplying(t *testing.T) {
	messages := largeRestartToolHistory(110)
	state := newCurrentPersistenceSession(t, &Session{Messages: messages})

	// A deterministic fake provider so the single summary transaction can land.
	firstSink := &recordSink{}
	first := newRestartMaintenanceAgent(state, &fakeProvider{reply: "structured resume briefing"}, firstSink)
	before := first.ContextMaintenanceSnapshot()
	if before.ProjectedTokens < before.TriggerTokens {
		t.Fatalf("fixture prompt/trigger = %d/%d, want prompt at or above trigger", before.ProjectedTokens, before.TriggerTokens)
	}
	if _, err := first.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatal(err)
	}
	if got := first.currentProjectionVersion(); got != 1 {
		t.Fatalf("first maintenance projection version = %d, want 1", got)
	}
	if got := appliedMaintenanceEvents(firstSink); got != 1 {
		t.Fatalf("first maintenance applied events = %d, want 1", got)
	}
	// Content-driven checkpoint should materially shrink the visible request.
	after := first.ContextMaintenanceSnapshot()
	if after.ProjectedTokens > after.HardInputCeiling/2 && after.ProjectedTokens > 300_000 {
		t.Fatalf("checkpoint projected tokens = %d, want content-driven low occupancy", after.ProjectedTokens)
	}

	reopenedSink := &recordSink{}
	state.reopen(t, state.ref)
	reopened := newRestartMaintenanceAgent(state, nil, reopenedSink)
	if got := reopened.currentProjectionVersion(); got != 1 {
		t.Fatalf("restored projection version = %d, want 1", got)
	}
	if got := reopened.ContextMaintenanceSnapshot().CheckpointState; got != "restored" {
		t.Fatalf("checkpoint state = %q, want restored", got)
	}
	if _, err := reopened.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatal(err)
	}
	if got := reopened.currentProjectionVersion(); got != 1 {
		t.Fatalf("reopen advanced unchanged projection to version %d", got)
	}
	if got := appliedMaintenanceEvents(reopenedSink); got != 0 {
		t.Fatalf("reopen reapplied maintenance %d times", got)
	}
}
