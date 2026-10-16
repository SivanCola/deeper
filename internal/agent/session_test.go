package agent

import (
	"deepzora/internal/provider"
	"testing"
)

func TestNewSessionEmpty(t *testing.T) {
	s := NewSession("")
	if len(s.Messages) != 0 {
		t.Errorf("empty session should have 0 messages, got %d", len(s.Messages))
	}
}

func TestNewSessionWithSystem(t *testing.T) {
	s := NewSession("You are a helpful assistant.")
	if len(s.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(s.Messages))
	}
	if s.Messages[0].Role != provider.RoleSystem {
		t.Errorf("role = %q, want system", s.Messages[0].Role)
	}
	if s.Messages[0].Content != "You are a helpful assistant." {
		t.Errorf("content = %q", s.Messages[0].Content)
	}
}

func TestSessionAdd(t *testing.T) {
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "hi there"})
	if len(s.Messages) != 2 {
		t.Fatalf("want 2 messages, got %d", len(s.Messages))
	}
	if s.Messages[0].Role != provider.RoleUser {
		t.Errorf("first role = %q", s.Messages[0].Role)
	}
	if s.Messages[1].Role != provider.RoleAssistant {
		t.Errorf("second role = %q", s.Messages[1].Role)
	}
}

func TestSessionAddDecisionReceiptKeepsToolResultsAdjacent(t *testing.T) {
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "run the check"})
	s.Add(provider.Message{
		Role:      provider.RoleAssistant,
		ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "bash", Arguments: `{}`}},
	})
	receipt := &provider.DecisionReceipt{ID: "approval-1", Kind: "tool", Tool: "bash", Outcome: "allow_once"}

	s.AddDecisionReceipt(receipt)
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "call-1", Name: "bash", Content: "ok"})

	got := s.Snapshot()
	if len(got) != 3 {
		t.Fatalf("messages = %d, want the original three-message tool turn", len(got))
	}
	if len(got[1].DecisionReceipts) != 1 || got[1].DecisionReceipts[0] != receipt {
		t.Fatalf("assistant receipts = %+v, want approval receipt", got[1].DecisionReceipts)
	}
	if got[2].Role != provider.RoleTool || got[2].ToolCallID != "call-1" {
		t.Fatalf("tool result no longer follows assistant directly: %+v", got)
	}
	if s.RewriteVersion() == 0 {
		t.Fatal("attaching receipt to an existing message must advance its revision")
	}
}

func TestHasContentEmpty(t *testing.T) {
	s := NewSession("")
	if s.HasContent() {
		t.Error("empty session should not have content")
	}
}

func TestHasContentSystemOnly(t *testing.T) {
	s := NewSession("system prompt")
	if s.HasContent() {
		t.Error("system-only session should not have content")
	}
}

func TestHasContentWithUser(t *testing.T) {
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	if !s.HasContent() {
		t.Error("session with user message should have content")
	}
}

func TestHasContentWithAssistant(t *testing.T) {
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "response"})
	if !s.HasContent() {
		t.Error("session with assistant message should have content")
	}
}

func TestHasContentWithTool(t *testing.T) {
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleTool, Content: "result", ToolCallID: "tc1"})
	if !s.HasContent() {
		t.Error("session with tool message should have content")
	}
}

func TestHasSystemMessageWithSystem(t *testing.T) {
	s := NewSession("system prompt")
	if !s.HasSystemMessage() {
		t.Error("session with system message should report HasSystemMessage true")
	}
}

func TestHasSystemMessageWithoutSystem(t *testing.T) {
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	if s.HasSystemMessage() {
		t.Error("session without system message should report HasSystemMessage false")
	}
}

func TestHasSystemMessageAfterReplaceWithoutSystem(t *testing.T) {
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "kept"})
	// Replace with messages that have no system message — simulates a
	// compact/summarise path that failed to preserve the system prompt.
	s.Replace([]provider.Message{
		{Role: provider.RoleUser, Content: "replaced"},
	})
	if s.HasContent() {
		// HasContent returns true because the user message exists.
		if s.HasSystemMessage() {
			t.Error("session replaced without system message should report HasSystemMessage false")
		}
	} else {
		t.Error("session with user message should have content")
	}
}

func TestHasSystemMessageCompactedKeepsSystem(t *testing.T) {
	// This is the healthy path: compact preserves the system message at index 0.
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "answer"})
	s.Replace([]provider.Message{
		{Role: provider.RoleSystem, Content: "system"},
		{Role: provider.RoleUser, Content: "summary"},
	})
	if !s.HasSystemMessage() {
		t.Error("compacted session should still have system message at index 0")
	}
}

func TestCurrentSessionRoundTrip(t *testing.T) {

	s := NewSession("system prompt")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "world"})
	loaded := roundTripCurrentSession(t, s)
	if len(loaded.Messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "system prompt" {
		t.Errorf("system = %q", loaded.Messages[0].Content)
	}
	if loaded.Messages[1].Content != "hello" {
		t.Errorf("user = %q", loaded.Messages[1].Content)
	}
	if loaded.Messages[2].Content != "world" {
		t.Errorf("assistant = %q", loaded.Messages[2].Content)
	}
}

func TestUpdateToolCallPreviewPersistsAfterMidTurnSnapshot(t *testing.T) {
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "edit twice"})
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
		{ID: "c1", Name: "edit_file", Arguments: `{}`},
		{ID: "c2", Name: "edit_file", Arguments: `{}`},
	}})
	state := newCurrentPersistenceSession(t, s)
	before := s.RewriteVersion()

	refreshed := provider.ToolCall{ID: "c2", Diff: "@@ -1 +1 @@\n-ready\n+done\n", Added: 1, Removed: 1}
	if !s.UpdateToolCallPreview(refreshed) {
		t.Fatal("matching tool call was not updated")
	}
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "c1", Name: "edit_file", Content: "ready"})
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "c2", Name: "edit_file", Content: "done"})
	if s.RewriteVersion() <= before {
		t.Fatal("mutating a snapshotted assistant message must advance its revision")
	}
	for _, message := range s.Snapshot() {
		if err := state.handle.RecordSessionMessageUpsert(t.Context(), "metadata-update", message); err != nil {
			t.Fatal(err)
		}
	}
	state.reopen(t, state.ref)
	loaded := state.history(t)
	var got provider.ToolCall
	for _, msg := range loaded.Messages {
		for _, call := range msg.ToolCalls {
			if call.ID == "c2" {
				got = call
			}
		}
	}
	if got.Diff != refreshed.Diff || got.Added != 1 || got.Removed != 1 {
		t.Fatalf("persisted preview = %+v, want %+v", got, refreshed)
	}
}

func TestUpdateToolCallResolutionPersistsAfterMidTurnSnapshot(t *testing.T) {
	s := NewSession("system")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "use MCP"})
	s.Add(provider.Message{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{
		ID: "c1", Name: "use_capability",
		Arguments: `{"action":"call","capability_id":"mcp-tool:db/write"}`,
	}}})
	state := newCurrentPersistenceSession(t, s)
	before := s.RewriteVersion()

	readOnly := false
	resolved := provider.ToolCall{
		ID: "c1", ResolvedName: "mcp__db__write",
		CapabilityID: "mcp-tool:db/write", ResolvedReadOnly: &readOnly,
	}
	if !s.UpdateToolCallResolution(resolved) {
		t.Fatal("matching tool call resolution was not updated")
	}
	s.Add(provider.Message{Role: provider.RoleTool, ToolCallID: "c1", Name: "use_capability", Content: "done"})
	if s.RewriteVersion() <= before {
		t.Fatal("resolved metadata on a snapshotted assistant message must advance its revision")
	}
	for _, message := range s.Snapshot() {
		if err := state.handle.RecordSessionMessageUpsert(t.Context(), "metadata-update", message); err != nil {
			t.Fatal(err)
		}
	}
	state.reopen(t, state.ref)
	loaded := state.history(t)
	got := loaded.Messages[2].ToolCalls[0]
	if got.ResolvedReadOnly == nil || *got.ResolvedReadOnly ||
		got.ResolvedName != resolved.ResolvedName || got.CapabilityID != resolved.CapabilityID {
		t.Fatalf("persisted resolved metadata = %+v, want %+v", got, resolved)
	}
}

func TestCloneKeepsRevisionAndOwnsItsMessageSlice(t *testing.T) {
	original := NewSession("system")
	original.Rewrite([]provider.Message{{Role: provider.RoleUser, Content: "rewritten"}}, "test")
	clone := original.CloneWithMessages(original.Snapshot())
	if clone.RewriteVersion() != original.RewriteVersion() || clone.TranscriptVersion() != original.TranscriptVersion() {
		t.Fatal("clone lost its content revision")
	}
	clone.Messages[0].Content = "clone-only"
	if original.Snapshot()[0].Content != "rewritten" {
		t.Fatal("clone shares mutable message slice")
	}
}

func TestMessageRangeReturnsClampedCopy(t *testing.T) {
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "a"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "b"})
	s.Add(provider.Message{Role: provider.RoleUser, Content: "c"})

	got := s.MessageRange(1, 3)
	if len(got) != 2 || got[0].Content != "a" || got[1].Content != "b" {
		t.Fatalf("MessageRange(1,3) = %+v", got)
	}
	if got := s.MessageRange(-5, 99); len(got) != 4 {
		t.Fatalf("clamped MessageRange = %d messages, want 4", len(got))
	}
	if got := s.MessageRange(3, 3); len(got) != 0 {
		t.Fatalf("empty MessageRange = %d messages, want 0", len(got))
	}
	got[0].Content = "mutated"
	if s.Messages[1].Content != "a" {
		t.Fatal("MessageRange must return a copy")
	}
}
