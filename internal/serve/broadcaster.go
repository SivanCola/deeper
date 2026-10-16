package serve

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"deepzora/internal/billing"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
	"deepzora/internal/session"
)

const (
	subscriberBufferSize      = 128
	subscriberPriorityReserve = 32
)

// Broadcaster is the event.Sink the controllers emit to in server mode. It
// marshals each event once and fans it out to every connected SSE subscriber.
// A slow subscriber's buffer is allowed to drop rather than back-pressure the
// agent goroutine — a browser that can't keep up loses intermediate frames, not
// the whole session (it can refetch /history).
type Broadcaster struct {
	mu                      sync.Mutex
	subs                    map[chan []byte]struct{}
	ledgers                 map[string]*billing.Ledger
	current                 string
	displayCurrency         string
	modelApplicationChanged func()
}

// NewBroadcaster returns an empty Broadcaster ready to accept subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs:    map[chan []byte]struct{}{},
		ledgers: map[string]*billing.Ledger{},
	}
}

// SetDisplayCurrency rebinds the session ledger to a stored valuation. Empty
// keeps automatic mode: a single original currency is selected and mixed
// currencies remain buckets.
func (b *Broadcaster) SetDisplayCurrency(currency string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.displayCurrency = billing.NormalizeCurrency(currency)
	b.mu.Unlock()
}

// sessionRouteKey accepts only current identity routes. Session IDs are opaque
// and must never be normalized as filesystem paths.
func sessionRouteKey(path string) string {
	path = strings.TrimSpace(path)
	id, ok := strings.CutPrefix(path, remoteSessionIDQueryPrefix)
	if !ok || session.ValidateSessionID(id) != nil {
		return ""
	}
	return remoteSessionIDQueryPrefix + id
}

// SetCurrentSession records the controller shown by current-only subscribers.
// Untagged events remain compatible and are attributed to this session.
func (b *Broadcaster) SetCurrentSession(path string) {
	if b == nil {
		return
	}
	path = sessionRouteKey(path)
	b.mu.Lock()
	b.current = path
	b.mu.Unlock()
}

// CurrentSession reports the session currently selected by Serve.
func (b *Broadcaster) CurrentSession() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.current
}

// ResetSessionPath clears one session ledger without affecting detached
// sessions. Empty selects the current session.
func (b *Broadcaster) ResetSessionPath(path string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if path == "" {
		path = b.current
	} else {
		path = sessionRouteKey(path)
	}
	delete(b.ledgers, path)
	b.mu.Unlock()
}

// SessionCostQuote returns the current aggregate quote without repricing.
func (b *Broadcaster) SessionCostQuote() billing.CostQuote {
	return b.SessionCostQuoteFor("")
}

// SessionCostQuoteFor returns one session's aggregate quote. Empty selects
// the current session so existing single-session callers keep their contract.
func (b *Broadcaster) SessionCostQuoteFor(path string) billing.CostQuote {
	if b == nil {
		return billing.AggregateQuotes(nil, "")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ledgerLocked(path).Total(b.displayCurrency)
}

func (b *Broadcaster) ledgerLocked(path string) *billing.Ledger {
	if path == "" {
		path = b.current
	} else {
		path = sessionRouteKey(path)
	}
	ledger := b.ledgers[path]
	if ledger == nil {
		ledger = billing.NewLedger()
		b.ledgers[path] = ledger
	}
	return ledger
}

// Emit marshals the event to JSON and delivers it to every subscriber. Drops to
// a subscriber whose buffer is full rather than blocking. A marshal failure is
// dropped silently — one bad event shouldn't stall the stream.
func (b *Broadcaster) Emit(e event.Event) {
	if e.SessionPath != "" {
		e.SessionPath = sessionRouteKey(e.SessionPath)
	}
	wired := eventwire.ToWire(e)
	route := e.SessionPath
	if e.SessionID != "" {
		route = remoteSessionIDQueryPrefix + e.SessionID
	}
	b.mu.Lock()
	observedCurrent := b.current
	b.mu.Unlock()
	wired.SessionCurrent = route != "" && route == observedCurrent
	data, err := json.Marshal(wired)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current != observedCurrent {
		wired.SessionCurrent = route != "" && route == b.current
		data, err = json.Marshal(wired)
		if err != nil {
			return
		}
	}
	if e.Kind == event.Usage && e.Usage != nil && e.CostQuote != nil {
		b.ledgerLocked(route).Add(*e.CostQuote, billing.UsageTokens{
			PromptTokens: e.Usage.PromptTokens, CompletionTokens: e.Usage.CompletionTokens,
			CacheHitTokens: e.Usage.CacheHitTokens, CacheMissTokens: e.Usage.CacheMissTokens,
			CacheWriteTokens: e.Usage.CacheWriteTokens, CacheWriteBilledTokens: e.Usage.CacheWriteBilledTokens,
			Estimated: e.Usage.Estimated,
		}, time.Now().UTC())
	}
	for ch := range b.subs {
		enqueueSubscriberFrame(ch, data, e.Kind)
	}
}

// EmitTo delivers an event only to the supplied subscriber. It is used for
// connection-local recovery frames, such as replaying a prompt to a browser
// that attached after the original event was emitted. Normal runtime events
// should continue to use Emit so every subscriber receives them.
func (b *Broadcaster) EmitTo(target <-chan []byte, e event.Event) {
	if e.SessionPath != "" {
		e.SessionPath = sessionRouteKey(e.SessionPath)
	}
	wired := eventwire.ToWire(e)
	route := e.SessionPath
	if e.SessionID != "" {
		route = remoteSessionIDQueryPrefix + e.SessionID
	}
	b.mu.Lock()
	observedCurrent := b.current
	b.mu.Unlock()
	wired.SessionCurrent = route != "" && route == observedCurrent
	data, err := json.Marshal(wired)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current != observedCurrent {
		wired.SessionCurrent = route != "" && route == b.current
		data, err = json.Marshal(wired)
		if err != nil {
			return
		}
	}
	for ch := range b.subs {
		if (<-chan []byte)(ch) != target {
			continue
		}
		enqueueSubscriberFrame(ch, data, e.Kind)
		return
	}
}

// eventIsPriority keeps lifecycle and terminal frames out of the high-volume
// delta budget. Slow subscribers may recover text/history over HTTP, but they
// must still learn that a turn, prompt, or foreground-session transition ended.
func eventIsPriority(kind event.Kind) bool {
	switch kind {
	case event.Reasoning, event.Text, event.ToolProgress, event.StreamAttempt:
		return false
	default:
		return true
	}
}

// eventMustReachSubscriber identifies lifecycle truth that cannot be recovered
// by refetching history. The reserved queue budget protects these frames from
// deltas; if other priority traffic also exhausts that budget, the newest
// terminal/routing frame evicts a recoverable queued frame instead of vanishing.
func eventMustReachSubscriber(kind event.Kind, data []byte) bool {
	switch kind {
	case event.TurnDone, event.SessionChanged:
		return true
	case event.Notice:
		return wireFrameMustReachSubscriber(data)
	default:
		return false
	}
}

func enqueueSubscriberFrame(ch chan []byte, data []byte, kind event.Kind) {
	priority := eventIsPriority(kind)
	if !priority && len(ch) >= cap(ch)-subscriberPriorityReserve {
		return
	}
	select {
	case ch <- data:
		return
	default:
		// A slow subscriber exhausted even the priority reserve. Ordinary
		// priority events remain lossy, but lifecycle truth gets one slot by
		// evicting an older frame while Broadcaster.mu serializes producers.
		if !eventMustReachSubscriber(kind, data) || !evictRecoverableSubscriberFrame(ch) {
			return
		}
	}
	// Broadcaster.mu excludes every producer, and eviction leaves at least one
	// slot. A concurrent consumer can only create more capacity, so this send is
	// bounded while guaranteeing the terminal frame is retained.
	ch <- data
}

func evictRecoverableSubscriberFrame(ch chan []byte) bool {
	queued := len(ch)
	if queued < cap(ch) {
		return true
	}
	frames := make([][]byte, 0, queued)
drain:
	for range queued {
		select {
		case frame := <-ch:
			frames = append(frames, frame)
		default:
			break drain
		}
	}
	if len(frames) == 0 {
		return true
	}
	evict := -1
	for i, frame := range frames {
		if !wireFrameMustReachSubscriber(frame) {
			evict = i
			break
		}
	}
	if evict < 0 {
		// A bounded queue cannot retain an unbounded run of terminal frames.
		// Prefer the latest lifecycle truth over an older one in that degenerate
		// case; normal saturation always finds a recoverable delta/status frame.
		evict = 0
	}
	for i, frame := range frames {
		if i != evict {
			ch <- frame
		}
	}
	return true
}

func wireFrameMustReachSubscriber(data []byte) bool {
	return eventwire.FrameMustReachMirror(data)
}

// EmitWire publishes an externally authored wire frame (same JSON contract as
// locally emitted events) to every subscriber. The local-takeover mirror uses
// it: a desktop writer that owns the session file pushes its frames through
// Serve so the remote tab keeps rendering the conversation live without any
// change to its own pipeline.
func (b *Broadcaster) EmitWire(wired eventwire.Event) {
	if wired.SessionPath != "" {
		wired.SessionPath = sessionRouteKey(wired.SessionPath)
	}
	b.mu.Lock()
	observedCurrent := b.current
	b.mu.Unlock()
	wired.SessionCurrent = wired.SessionPath != "" && wired.SessionPath == observedCurrent
	data, err := json.Marshal(wired)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current != observedCurrent {
		wired.SessionCurrent = wired.SessionPath != "" && wired.SessionPath == b.current
		data, err = json.Marshal(wired)
		if err != nil {
			return
		}
	}
	for ch := range b.subs {
		enqueueSubscriberWireFrame(ch, data, wired.Kind)
	}
}

// wireKindIsPriority mirrors eventIsPriority for externally supplied frames,
// which arrive as wire kind strings rather than typed event.Kind values.
func wireKindIsPriority(kind string) bool {
	return !eventwire.WireKindIsRecoverable(kind)
}

func enqueueSubscriberWireFrame(ch chan []byte, data []byte, kind string) {
	priority := wireKindIsPriority(kind)
	if !priority && len(ch) >= cap(ch)-subscriberPriorityReserve {
		return
	}
	select {
	case ch <- data:
		return
	default:
		if !wireFrameMustReachSubscriber(data) || !evictRecoverableSubscriberFrame(ch) {
			return
		}
	}
	ch <- data
}

// Subscribe registers an identity-routed SSE client. Every client receives
// current and detached frames and attributes them using their session identity.
// The handler must call unsubscribe when the client disconnects.
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, subscriberBufferSize)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Subscribers reports the current connection count (for diagnostics/tests).
func (b *Broadcaster) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
