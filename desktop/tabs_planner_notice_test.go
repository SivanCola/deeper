package main

import (
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/provider"
	"deepzora/internal/transcript"
	"deepzora/internal/turnevent"
	"encoding/json"
	"reflect"
	"testing"
)

// Exercise the current transcript snapshot sent to Desktop, including the JSON
// boundary for nested diagnostics and receipt fields.
func roundTripDesktopTranscript(t *testing.T, events ...event.Event) []transcript.Message {
	t.Helper()
	identity := transcript.Identity{SessionID: "session", RuntimeEpoch: "runtime"}
	p, err := transcript.NewProjection(identity, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		wire := eventwire.ToWire(e)
		if err := p.Apply(turnevent.Envelope{SessionID: identity.SessionID, RuntimeEpoch: identity.RuntimeEpoch, TurnID: "turn", Sequence: uint64(i + 1), Kind: wire.Kind, Status: e.Status, Event: wire}); err != nil {
			t.Fatal(err)
		}
	}
	state, err := p.Snapshot(transcript.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded transcript.Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	rows := make([]transcript.Message, len(decoded.Records))
	for i, record := range decoded.Records {
		rows[i] = record.Message
	}
	return rows
}

func TestTranscriptSnapshotKeepsPlannerNoticeDetail(t *testing.T) {
	rows := roundTripDesktopTranscript(t, event.Event{
		Kind: event.Notice, Level: event.LevelWarn, Source: event.UsageSourcePlanner,
		Text: "An MCP server failed to start.", Detail: `mcp server "github" failed to start: command not found`,
	})
	if len(rows) != 1 || rows[0].Role != "notice" || rows[0].Level != "warn" || rows[0].Content != "An MCP server failed to start." || rows[0].Detail != `mcp server "github" failed to start: command not found` {
		t.Fatalf("snapshot lost planner diagnostics: %+v", rows)
	}
}

func TestInterruptedTranscriptSnapshotKeepsStructuredDecisionReceipt(t *testing.T) {
	receipt := &provider.DecisionReceipt{ID: "approval-1", Kind: "tool", Tool: "bash", Subject: "npm run build", Outcome: "allow_once"}
	rows := roundTripDesktopTranscript(t,
		event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Decision recorded: allow_once", Code: event.NoticeCodeDecisionReceipt, DecisionReceipt: receipt},
		event.Event{Kind: event.TurnDone, Status: event.TurnInterrupted},
	)
	if len(rows) != 2 || !reflect.DeepEqual(rows[0].DecisionReceipt, receipt) || rows[1].Code != event.NoticeCodeCancelledTurn {
		t.Fatalf("snapshot lost receipt or interruption: %+v", rows)
	}
}

func TestInterruptedTranscriptSnapshotKeepsPartialOutput(t *testing.T) {
	for _, status := range []event.TurnStatus{event.TurnInterrupted, event.TurnRecoveryRequired} {
		t.Run(string(status), func(t *testing.T) {
			rows := roundTripDesktopTranscript(t,
				event.Event{Kind: event.Text, MessageID: "answer", Text: "partial answer"},
				event.Event{Kind: event.ToolDispatch, MessageID: "tools", Tool: event.Tool{ID: "call-1", Name: "read_file", Args: `{"path":"notes.txt"}`}},
				event.Event{Kind: event.TurnDone, Status: status},
			)
			if len(rows) != 3 || rows[0].Content != "partial answer" || len(rows[1].ToolCalls) != 1 || rows[1].ToolCalls[0].ID != "call-1" || rows[2].Code != event.NoticeCodeCancelledTurn {
				t.Fatalf("snapshot lost interrupted output: %+v", rows)
			}
		})
	}
}
