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

// Maintenance is generation-scoped: below compact_ratio nothing happens;
// these tool results fit the prune limit, so the threshold installs one summary.
// Appends before the next threshold do not re-pay.
func TestIssue7935Maintains206ToolResultsOnceAndOnlyProcessesNewTail(t *testing.T) {
	const staleResults = 40
	big := strings.Repeat("line\n", 200)
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "first task"},
	}
	for i := range staleResults {
		id := fmt.Sprintf("old-%d", i)
		messages = append(messages,
			provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: id, Name: "read_file", Arguments: "{}"}}},
			provider.Message{Role: provider.RoleTool, ToolCallID: id, Name: "read_file", Content: big},
		)
	}
	messages = append(messages,
		provider.Message{Role: provider.RoleUser, Content: "recent question"},
		provider.Message{Role: provider.RoleAssistant, Content: "recent answer"},
	)
	sess := &Session{Messages: messages}
	sink := &recordSink{}
	a := New(&fakeProvider{reply: "structured history digest"}, tool.NewRegistry(), sess, Options{
		ContextWindow: 8_000,
		CompactRatio:  0.80,
		ArchiveDir:    t.TempDir(),
	}, sink)

	// Below the fold trigger nothing may be rewritten.
	prepareAfterProviderUsage(t, a, context.Background(), &provider.Usage{PromptTokens: 5_000})
	if got := a.currentProjectionVersion(); got != 0 {
		t.Fatalf("history was rewritten below the fold trigger: projection version %d", got)
	}

	// These under-limit tool results need a summary to relieve pressure.
	prepareAfterProviderUsage(t, a, context.Background(), &provider.Usage{PromptTokens: 7_000})
	firstVersion := a.currentProjectionVersion()
	if firstVersion != 1 {
		t.Fatalf("first maintenance version = %d, want 1", firstVersion)
	}
	if got := countToolResultsContaining(a.modelVisibleMessages(), toolPruneMarker); got != 0 {
		t.Fatalf("automatic prune markers installed = %d, want 0", got)
	}
	status := a.ContextMaintenanceSnapshot()
	if status.LastReceipt == nil || status.LastReceipt.Action != "summary" || status.LastReceipt.Status != "applied" {
		t.Fatalf("maintenance snapshot receipt = %+v", status.LastReceipt)
	}
	for i, msg := range sess.Snapshot() {
		if msg.Role == provider.RoleTool && msg.Content != big {
			t.Fatalf("canonical tool result %d was rewritten", i)
		}
	}

	// Appends that stay under the next threshold must not re-summarize.
	sess.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "new", Name: "read_file", Arguments: "{}"}}})
	sess.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "new", Name: "read_file", Content: "small"})
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "continue"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	if _, err := a.contextManager().Prepare(context.Background(), ContextPreparePolicy{Trigger: CompactionTriggerPressure}); err != nil {
		t.Fatal(err)
	}
	if got := a.currentProjectionVersion(); got != firstVersion {
		t.Fatalf("below-threshold append advanced version to %d, want %d", got, firstVersion)
	}

	var applied int
	for _, got := range sink.kinds(event.ContextMaintenanceEvent) {
		if got.Maintenance != nil && got.Maintenance.Status == "applied" && got.Maintenance.Action == "summary" {
			applied++
		}
	}
	if applied != 1 {
		t.Fatalf("summary applied events = %d, want exactly 1", applied)
	}
}
