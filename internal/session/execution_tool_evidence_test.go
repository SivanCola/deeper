package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/provider"
)

func TestCurrentToolEvidenceSurvivesCompactionAndColdOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "current")
	s, err := CreateWithOptions(dir, "current", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	record := provider.ToolCallRecord{Identity: provider.ActionIdentity{AttemptID: "attempt", CallID: "write"}, State: provider.ToolRunStarted, Arguments: json.RawMessage(`{"private":"execution-only"}`)}
	done := record
	done.Identity.AttemptID = "done-attempt"
	done.State = provider.ToolRunCompleted
	message := provider.Message{ID: "proposal", Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
		{ID: "write", Name: "write_file", Arguments: `{}`, Recovery: &record, WriteIntents: []json.RawMessage{json.RawMessage(`{"version":1,"path":"private-target"}`)}},
		{ID: "done", Name: "write_file", Arguments: `{}`, Recovery: &done},
	}}
	appendReplayEvent(t, s, "proposal", "message/complete", map[string]any{"message": message})
	assertCurrentToolEvidence(t, s, false)
	appendReplayEvent(t, s, "compact", "model/context-replace", map[string]any{"messages": []provider.Message{{ID: "summary", Role: provider.RoleUser, Content: "compressed"}}})
	assertCurrentToolEvidence(t, s, true)
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	s, err = OpenWithOptions(dir, "current", OpenOptions{ExternalHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentToolEvidence(t, s, true)
	if len(s.Snapshot().Projection.Messages) != 1 {
		t.Fatal("execution evidence fabricated a historical result")
	}
}

func assertCurrentToolEvidence(t *testing.T, s *Session, compacted bool) {
	t.Helper()
	view := s.ExecutionSnapshot().Projection
	var evidence *provider.ToolCall
	for i := range view.ModelMessages {
		for j := range view.ModelMessages[i].ToolCalls {
			call := &view.ModelMessages[i].ToolCalls[j]
			if call.ID == "write" {
				evidence = call
				if compacted && !view.ModelMessages[i].LocalOnly {
					t.Fatal("old evidence reentered model workset")
				}
			}
		}
	}
	if evidence == nil || evidence.Recovery == nil || evidence.Recovery.State != provider.ToolRunStarted || len(evidence.WriteIntents) != 1 {
		t.Fatalf("lost evidence: %+v", evidence)
	}
	wire, err := json.Marshal(provider.ModelMessages(view.ModelMessages))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"tool_recovery", "write_intents", "execution-only", "private-target"} {
		if strings.Contains(string(wire), secret) {
			t.Fatalf("provider received %s", secret)
		}
	}
	if compacted && len(view.ToolCallEvidence["proposal"]) != 1 {
		t.Fatal("settled evidence outside model window was retained")
	}
	evidence.Recovery.State = provider.ToolRunCompleted
	evidence.WriteIntents[0][0] = 'X'
	next := s.ExecutionSnapshot().Projection.ToolCallEvidence["proposal"][0]
	if next.Recovery.State != provider.ToolRunStarted || next.WriteIntents[0][0] != '{' {
		t.Fatal("snapshot mutation reached authoritative evidence")
	}
}
