package main

import (
	"strings"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/provider"
	"deepzora/internal/transcript"
	"deepzora/internal/turnevent"
)

func TestDisplayTurnBufferPreservesStreamingReplacementAndTools(t *testing.T) {
	var buffer transcript.Buffer
	buffer.Apply(event.Event{Kind: event.Reasoning, Text: "draft reason "})
	buffer.Apply(event.Event{Kind: event.Reasoning, Text: "continued"})
	buffer.Apply(event.Event{Kind: event.Text, Text: "draft answer"})
	buffer.Apply(event.Event{
		Kind:      event.Message,
		Text:      "final answer",
		Reasoning: "final reason",
		MemoryCitations: []provider.MemoryCitation{{
			ID: "memory-1", Source: "project",
		}},
	})
	buffer.Apply(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: "call-1", Name: "read_file", Args: `{"path":"settings.json"}`,
	}})
	buffer.Apply(event.Event{Kind: event.ToolResult, Tool: event.Tool{
		ID: "call-1", Output: "settings contents",
	}})

	got := buffer.Messages()
	if len(got) != 3 {
		t.Fatalf("messages = %d, want 3: %+v", len(got), got)
	}
	if got[0].Role != "assistant" || got[0].Content != "final answer" || got[0].Reasoning != "final reason" {
		t.Fatalf("stream replacement changed: %+v", got[0])
	}
	if len(got[0].MemoryCitations) != 1 || got[0].MemoryCitations[0].ID != "memory-1" {
		t.Fatalf("memory citations changed: %+v", got[0].MemoryCitations)
	}
	if got[1].Role != "assistant" || len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("tool call changed: %+v", got[1])
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call-1" || got[2].ToolName != "read_file" {
		t.Fatalf("tool result changed: %+v", got[2])
	}
}

func TestDisplayTurnBufferMessageIdentityAndDiscardMatchRecovery(t *testing.T) {
	events := []event.Event{
		{Kind: event.StreamAttempt, MessageID: "failed", AttemptID: "failed", StreamAttempt: event.StreamAttemptInfo{ID: "failed", Action: event.StreamAttemptBegin}},
		{Kind: event.Reasoning, MessageID: "failed", AttemptID: "failed", Text: "discard this"},
		{Kind: event.Message, MessageID: "failed", AttemptID: "failed", Text: "rejected full response"},
		{Kind: event.StreamAttempt, MessageID: "failed", AttemptID: "failed", StreamAttempt: event.StreamAttemptInfo{ID: "failed", Action: event.StreamAttemptDiscard}},
		{Kind: event.Reasoning, MessageID: "a", AttemptID: "a", Text: "first thought"},
		{Kind: event.ToolDispatch, MessageID: "a", Tool: event.Tool{ID: "call", Name: "read_file", Args: `{}`}},
		{Kind: event.ToolResult, Tool: event.Tool{ID: "call", Name: "read_file", Output: "done"}},
		{Kind: event.Reasoning, MessageID: "b", AttemptID: "b", Text: "second thought"},
		{Kind: event.Message, MessageID: "a", AttemptID: "a", Reasoning: "first thought", Text: "first answer"},
	}
	var live, recovered transcript.Buffer
	for i, e := range events {
		live.Apply(e)
		wire := eventwire.ToWire(e)
		replay, ok := transcript.EventFromEnvelope(turnevent.Envelope{Kind: wire.Kind, Sequence: uint64(i + 1), Event: wire})
		if !ok {
			t.Fatalf("event %s is not replayable", wire.Kind)
		}
		recovered.Apply(replay)
	}
	for name, buffer := range map[string]*transcript.Buffer{"live": &live, "recovered": &recovered} {
		rows := buffer.Messages()
		if len(rows) != 3 || rows[0].MessageID != "a" || rows[0].Content != "first answer" || rows[0].Reasoning != "first thought" || len(rows[0].ToolCalls) != 1 || rows[2].MessageID != "b" || rows[2].Reasoning != "second thought" {
			t.Fatalf("%s message ownership/discard mismatch: %+v", name, rows)
		}
	}
}

func TestDisplayTurnBufferStreamingAllocationsStayNearLinear(t *testing.T) {
	const (
		chunks    = 2_000
		chunkSize = 32
	)
	chunk := strings.Repeat("x", chunkSize)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			var buffer transcript.Buffer
			for range chunks {
				buffer.Apply(event.Event{Kind: event.Text, Text: chunk})
			}
			messages := buffer.Messages()
			if len(messages) != 1 || len(messages[0].Content) != chunks*chunkSize {
				b.Fatalf("materialized display length changed")
			}
		}
	})

	// Repeated string concatenation allocated roughly one full growing prefix
	// per chunk (>69 MiB for this 64 KiB stream). Keep a generous ceiling for
	// platform/runtime variance while pinning the intended near-linear shape.
	if got, max := result.AllocedBytesPerOp(), int64(chunks*chunkSize*16); got > max {
		t.Fatalf("stream allocated %d bytes/op, want <= %d (%s)", got, max, result.String())
	}
	if got := result.AllocsPerOp(); got > 100 {
		t.Fatalf("stream allocated %d objects/op, want <= 100 (%s)", got, result.String())
	}
	t.Logf("64 KiB stream: %d bytes/op, %d allocs/op", result.AllocedBytesPerOp(), result.AllocsPerOp())
}
