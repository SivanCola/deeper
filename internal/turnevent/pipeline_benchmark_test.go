package turnevent_test

import (
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/turnevent"
)

type benchmarkLedgerSink struct {
	ledger    *turnevent.Ledger
	projected int
}

func (s *benchmarkLedgerSink) Emit(e event.Event) { _ = s.EmitChecked(e) }

func (s *benchmarkLedgerSink) EmitChecked(e event.Event) error {
	status := event.TurnInProgress
	if e.Kind == event.TurnDone {
		status = event.TurnCompleted
	}
	_, ok, err := s.ledger.Append(e, status)
	if err == nil && ok {
		s.projected++
	}
	return err
}

func BenchmarkCoalesceLedgerFrontend10000TextDeltas(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ledger := turnevent.NewMemory("benchmark")
		if _, err := ledger.Begin(); err != nil {
			b.Fatal(err)
		}
		sink := &benchmarkLedgerSink{ledger: ledger}
		stream := event.Coalesce(sink, event.DefaultStreamDeltaWindow)
		for range 10_000 {
			stream.Emit(event.Event{Kind: event.Text, Text: "x"})
		}
		if err := event.EmitChecked(stream, event.Event{Kind: event.TurnDone}); err != nil {
			b.Fatal(err)
		}
		if sink.projected < 2 || sink.projected >= 10_000 {
			b.Fatalf("projected records = %d, want coalesced stream plus terminal", sink.projected)
		}
	}
}

func BenchmarkLedger10000ToolProgress(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ledger := turnevent.NewMemory("benchmark")
		if _, err := ledger.Begin(); err != nil {
			b.Fatal(err)
		}
		for i := range 10_000 {
			if _, ok, err := ledger.Append(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "tool", Name: "bash", Output: "tick"}}, event.TurnInProgress); err != nil || !ok {
				b.Fatalf("progress %d: ok=%v err=%v", i, ok, err)
			}
		}
		if _, ok, err := ledger.Append(event.Event{Kind: event.TurnDone}, event.TurnCompleted); err != nil || !ok {
			b.Fatalf("terminal: ok=%v err=%v", ok, err)
		}

	}
}
