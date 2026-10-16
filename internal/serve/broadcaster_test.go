package serve

import (
	"encoding/json"
	"strings"
	"testing"

	"deepzora/internal/event"
	"deepzora/internal/eventwire"
)

func TestBroadcasterPublishesCurrentIdentityRoutes(t *testing.T) {
	b := NewBroadcaster()
	b.SetCurrentSession("session-id:current")
	current, stopCurrent := b.Subscribe()
	all, stopAll := b.Subscribe()
	defer stopCurrent()
	defer stopAll()

	b.Emit(event.Event{Kind: event.Text, Text: "current", SessionPath: "session-id:current"})
	b.Emit(event.Event{Kind: event.Text, Text: "background", SessionPath: "session-id:background"})
	b.Emit(event.Event{Kind: event.Text, Text: "host notice"})

	drain := func(ch <-chan []byte) []string {
		var frames []string
		for {
			select {
			case frame := <-ch:
				frames = append(frames, string(frame))
			default:
				return frames
			}
		}
	}
	if got := len(drain(current)); got != 3 {
		t.Fatalf("identity subscription received %d frames, want 3", got)
	}
	if got := len(drain(all)); got != 3 {
		t.Fatalf("all-session subscription received %d frames, want 3", got)
	}
}

func TestBroadcasterMarksForegroundFramesAtPublication(t *testing.T) {
	b := NewBroadcaster()
	b.SetCurrentSession("session-id:current")
	all, stop := b.Subscribe()
	defer stop()

	b.Emit(event.Event{Kind: event.Text, Text: "current", SessionPath: "session-id:current"})
	b.Emit(event.Event{Kind: event.Text, Text: "background", SessionPath: "session-id:background"})

	var current, background eventwire.Event
	if err := json.Unmarshal(<-all, &current); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(<-all, &background); err != nil {
		t.Fatal(err)
	}
	if !current.SessionCurrent {
		t.Fatalf("foreground frame was not marked current: %+v", current)
	}
	if background.SessionCurrent {
		t.Fatalf("background frame was marked current: %+v", background)
	}
}

func TestBroadcasterFanOut(t *testing.T) {
	b := NewBroadcaster()
	a, ca := b.Subscribe()
	d, cd := b.Subscribe()
	defer ca()
	defer cd()

	if got := b.Subscribers(); got != 2 {
		t.Fatalf("subscribers = %d, want 2", got)
	}

	b.Emit(event.Event{Kind: event.Text, Text: "hi"})

	for i, ch := range []<-chan []byte{a, d} {
		var w eventwire.Event
		if err := json.Unmarshal(<-ch, &w); err != nil {
			t.Fatalf("subscriber %d: %v", i, err)
		}
		if w.Kind != "text" || w.Text != "hi" {
			t.Errorf("subscriber %d got %+v", i, w)
		}
	}
}

func TestBroadcasterEmitToKeepsIdentityRoutes(t *testing.T) {
	b := NewBroadcaster()
	b.SetCurrentSession("session-id:b")
	current, stopCurrent := b.Subscribe()
	all, stopAll := b.Subscribe()
	defer stopCurrent()
	defer stopAll()
	b.EmitTo(current, event.Event{Kind: event.ApprovalRequest, SessionPath: "session-id:a"})
	b.EmitTo(all, event.Event{Kind: event.ApprovalRequest, SessionPath: "session-id:a"})
	if len(current) != 1 {
		t.Fatal("subscriber lost identity-routed replay")
	}
	if len(all) != 1 {
		t.Fatal("all-session subscriber lost a tagged background replay")
	}
	b.EmitTo(current, event.Event{Kind: event.ApprovalRequest, SessionPath: "session-id:b"})
	if len(current) != 2 {
		t.Fatal("subscriber lost the current session replay")
	}
}

func TestBroadcasterEmitsRetryingJSON(t *testing.T) {
	b := NewBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Emit(event.Event{Kind: event.Retrying, RetryAttempt: 3, RetryMax: 10})

	s := string(<-ch)
	for _, want := range []string{`"kind":"retrying"`, `"retryAttempt":3`, `"retryMax":10`} {
		if !strings.Contains(s, want) {
			t.Fatalf("retrying broadcast JSON = %s, want it to contain %s", s, want)
		}
	}
}

func TestBroadcasterUnsubscribe(t *testing.T) {
	b := NewBroadcaster()
	_, cancel := b.Subscribe()
	if b.Subscribers() != 1 {
		t.Fatalf("want 1 subscriber")
	}
	cancel()
	if b.Subscribers() != 0 {
		t.Fatalf("unsubscribe should drop to 0, got %d", b.Subscribers())
	}
	// Emitting with no subscribers must not panic.
	b.Emit(event.Event{Kind: event.TurnDone})
}

func TestBroadcasterDropsSlowSubscriber(t *testing.T) {
	b := NewBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()
	// Overfill far past the subscriber buffer without reading; Emit must not block.
	for range 1000 {
		b.Emit(event.Event{Kind: event.Text, Text: "x"})
	}
	if len(ch) == 0 {
		t.Error("expected some buffered frames")
	}
}

func TestBroadcasterReservesCapacityForTerminalFrames(t *testing.T) {
	b := NewBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()
	for range subscriberBufferSize * 10 {
		b.Emit(event.Event{Kind: event.Text, Text: "delta"})
	}
	b.Emit(event.Event{Kind: event.TurnDone})

	found := false
	for len(ch) > 0 {
		var frame eventwire.Event
		if err := json.Unmarshal(<-ch, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Kind == "turn_done" {
			found = true
		}
	}
	if !found {
		t.Fatal("slow subscriber lost the terminal frame after a delta flood")
	}
}

func TestBroadcasterEvictsRecoverableFramesForTerminalEvents(t *testing.T) {
	b := NewBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()
	for range subscriberBufferSize - subscriberPriorityReserve {
		b.Emit(event.Event{Kind: event.Text, Text: "delta"})
	}
	for range subscriberPriorityReserve {
		b.Emit(event.Event{Kind: event.Notice, Text: "priority"})
	}
	if got := len(ch); got != subscriberBufferSize {
		t.Fatalf("saturated subscriber length = %d, want %d", got, subscriberBufferSize)
	}

	b.Emit(event.Event{Kind: event.TurnDone})
	b.Emit(event.Event{Kind: event.SessionChanged, SessionPath: "session-id:next"})

	found := map[string]bool{}
	for len(ch) > 0 {
		var frame eventwire.Event
		if err := json.Unmarshal(<-ch, &frame); err != nil {
			t.Fatal(err)
		}
		found[frame.Kind] = true
	}
	for _, kind := range []string{"turn_done", "session_changed"} {
		if !found[kind] {
			t.Fatalf("slow subscriber lost %s after priority reserve saturation", kind)
		}
	}
}

func TestBroadcasterPreservesBackgroundJobCompletionNotice(t *testing.T) {
	b := NewBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()
	for range subscriberBufferSize - subscriberPriorityReserve {
		b.Emit(event.Event{Kind: event.Text, Text: "delta"})
	}
	for range subscriberPriorityReserve {
		b.Emit(event.Event{Kind: event.Notice, Text: "priority"})
	}
	b.Emit(event.Event{Kind: event.Notice, Code: event.NoticeCodeBackgroundJobFinished, Text: "background task finished"})

	found := false
	for len(ch) > 0 {
		var frame eventwire.Event
		if err := json.Unmarshal(<-ch, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Kind == "notice" && frame.Code == event.NoticeCodeBackgroundJobFinished {
			found = true
		}
	}
	if !found {
		t.Fatal("slow subscriber lost background-job completion after priority reserve saturation")
	}
}

// Notices about final-format identities (taken over, reclaim requested,
// reclaimed, adopted) route by "session-id:<id>", which is not a filesystem
// path. Every emit path must carry it verbatim and current-only subscribers
// must still receive it: a canonicalized "<cwd>/session-id:x" matched no
// subscriber and was dropped, so a browser tab never saw the takeover banner.
func TestBroadcasterKeepsIdentityRoutesVerbatim(t *testing.T) {
	b := NewBroadcaster()
	b.SetCurrentSession("session-id:current")
	current, stopCurrent := b.Subscribe()
	all, stopAll := b.Subscribe()
	defer stopCurrent()
	defer stopAll()
	const route = "session-id:abc"

	b.Emit(event.Event{Kind: event.Notice, Code: event.NoticeCodeSessionTakenOver, SessionPath: route})
	b.EmitTo(current, event.Event{Kind: event.Notice, Code: event.NoticeCodeSessionReclaimed, SessionPath: route})
	b.EmitWire(eventwire.Event{Kind: "text", Text: "mirrored", SessionPath: route})

	decode := func(name string, ch <-chan []byte) eventwire.Event {
		t.Helper()
		var frame eventwire.Event
		select {
		case raw := <-ch:
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("%s subscriber did not receive the identity-routed frame", name)
		}
		return frame
	}
	for range 3 {
		if frame := decode("current-only", current); frame.SessionPath != route {
			t.Fatalf("current-only frame route = %q, want %q (%+v)", frame.SessionPath, route, frame)
		}
	}
	for range 2 {
		if frame := decode("all-session", all); frame.SessionPath != route {
			t.Fatalf("all-session frame route = %q, want %q (%+v)", frame.SessionPath, route, frame)
		}
	}

}
