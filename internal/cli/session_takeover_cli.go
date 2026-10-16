package cli

// Mirror side of CLI takeover: once a resident serve releases the lease the
// manager below owns the session, streams frames back to the remote tab that
// keeps watching read-only, and returns the lease when the tab reclaims it.

import (
	"bytes"
	"context"
	"deepzora/internal/processidentity"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/eventwire"
)

type cliTakeoverBinding struct {
	path   string
	record cliServeRecord
	client *http.Client
	grant  cliTakeoverGrant
}

const (
	cliTakeoverFlushEvery = 120 * time.Millisecond
	cliTakeoverHeartbeat  = 5 * time.Second
	cliTakeoverMaxFrames  = eventwire.MirrorBatchMaxFrames
)

const cliTakeoverRediscoverFailures = 3

// cliTakeoverManager is the outermost CLI event sink while a handed-off
// session is active. It preserves the terminal sink, mirrors the same typed
// frames to Serve, and cooperatively returns the lease when reclaim is seen.
// One manager survives controller rebuilds; AttachController updates its live
// authority pointer without replacing the sink wired into boot.
type cliTakeoverManager struct {
	event.AuditForwarder
	inner event.Sink

	// Lock order is returnMu -> sendMu -> mu. Emit only takes mu, so the model
	// event sink never waits for an HTTP request.
	returnMu sync.Mutex
	sendMu   sync.Mutex
	mu       sync.Mutex
	binding  *cliTakeoverBinding
	// yielded is the binding the last reclaim (or Close) returned. "/takeover
	// takes it back" routes by that mirror's session identity.
	yielded  *cliTakeoverBinding
	revision uint64
	failures int
	ctrl     control.SessionAPI
	queue    eventwire.MirrorQueue
	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	onYield  func()

	started    bool
	stopOnce   sync.Once
	reclaiming atomic.Bool
	returned   atomic.Bool
	closed     atomic.Bool
}

func newCLITakeoverManager(inner event.Sink) *cliTakeoverManager {
	return &cliTakeoverManager{AuditForwarder: event.AuditForwarder{Inner: inner}, inner: inner}
}

func (m *cliTakeoverManager) SetInner(inner event.Sink) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.inner = inner
	m.Inner = inner
	m.mu.Unlock()
}

func (m *cliTakeoverManager) Emit(e event.Event) {
	if m == nil {
		return
	}
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if inner != nil {
		inner.Emit(e)
	}
	m.mu.Lock()
	if m.binding != nil && !m.returned.Load() {
		m.queue.Push(eventwire.ToWire(e))
	}
	wake := m.wake
	m.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (m *cliTakeoverManager) EmitChecked(e event.Event) error {
	m.mu.Lock()
	inner := m.inner
	m.mu.Unlock()
	if checked, ok := inner.(event.CheckedSink); ok {
		if err := checked.EmitChecked(e); err != nil {
			return err
		}
	} else if inner != nil {
		inner.Emit(e)
	}
	m.mu.Lock()
	if m.binding != nil && !m.returned.Load() {
		m.queue.Push(eventwire.ToWire(e))
	}
	wake := m.wake
	m.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *cliTakeoverManager) AttachController(ctrl control.SessionAPI) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ctrl = ctrl
	m.mu.Unlock()
}

func (m *cliTakeoverManager) Activate(binding *cliTakeoverBinding) {
	if m == nil || binding == nil {
		return
	}
	m.returnMu.Lock()
	defer m.returnMu.Unlock()
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	m.mu.Lock()
	m.binding = binding
	m.yielded = nil
	m.revision++
	m.failures = 0
	m.returned.Store(false)
	m.reclaiming.Store(false)
	m.ensureStartedLocked()
	m.mu.Unlock()
}

func (m *cliTakeoverManager) ensureStartedLocked() {
	if m.started {
		return
	}
	m.started = true
	m.wake = make(chan struct{}, 1)
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	go m.run()
}

func (m *cliTakeoverManager) SetYieldCallback(fn func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.onYield = fn
	m.mu.Unlock()
}

func (m *cliTakeoverManager) Reclaiming() bool { return m != nil && m.reclaiming.Load() }
func (m *cliTakeoverManager) Returned() bool   { return m != nil && m.returned.Load() }

// ResumeAfterYield clears the terminal-side marker after the TUI has acquired
// a different session. Returning a mirror ends that mirror permanently; the
// manager must not keep blocking the new session just because the process that
// hosted the old one stayed alive.
func (m *cliTakeoverManager) ResumeAfterYield() {
	if m == nil {
		return
	}
	m.returnMu.Lock()
	defer m.returnMu.Unlock()
	m.mu.Lock()
	if m.binding == nil {
		m.yielded = nil
		m.returned.Store(false)
		m.reclaiming.Store(false)
	}
	m.mu.Unlock()
}

// yieldedBinding reports the mirror a reclaim returned, until the TUI either
// re-takes it or moves on to another session.
func (m *cliTakeoverManager) yieldedBinding() *cliTakeoverBinding {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.yielded
}

func (m *cliTakeoverManager) snapshot() (*cliTakeoverBinding, control.SessionAPI, func(), uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.binding, m.ctrl, m.onYield, m.revision
}

func (m *cliTakeoverManager) run() {
	m.mu.Lock()
	wake, stop, done := m.wake, m.stop, m.done
	m.mu.Unlock()
	defer close(done)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	heartbeat := time.NewTicker(cliTakeoverHeartbeat)
	defer heartbeat.Stop()
	armed := false
	for {
		select {
		case <-stop:
			m.push(false)
			return
		case <-wake:
			if !armed {
				timer.Reset(cliTakeoverFlushEvery)
				armed = true
			}
		case <-timer.C:
			armed = false
			if !m.push(false) {
				return
			}
		case <-heartbeat.C:
			if !m.push(true) {
				return
			}
		}
	}
}

func (m *cliTakeoverManager) drain() []eventwire.Event {
	m.mu.Lock()
	frames := m.queue.Take(cliTakeoverMaxFrames)
	m.mu.Unlock()
	return frames
}

func (m *cliTakeoverManager) requeue(frames []eventwire.Event) {
	if len(frames) == 0 {
		return
	}
	m.mu.Lock()
	m.queue.Prepend(frames)
	m.mu.Unlock()
}

func (m *cliTakeoverManager) wakeIfQueued() {
	m.mu.Lock()
	pending, wake := m.queue.Len() > 0, m.wake
	m.mu.Unlock()
	if pending && wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (m *cliTakeoverManager) push(heartbeat bool) bool {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	return m.pushLocked(heartbeat)
}

func (m *cliTakeoverManager) pushLocked(heartbeat bool) bool {
	if m.returned.Load() {
		return false
	}
	binding, _, _, revision := m.snapshot()
	if binding == nil || binding.client == nil || binding.grant.MirrorID == "" {
		return true
	}
	frames := m.drain()
	if len(frames) == 0 && !heartbeat {
		return true
	}
	marshal := func(batch []eventwire.Event) ([]byte, error) {
		return json.Marshal(map[string]any{
			"sessionPath": binding.path, "mirrorId": binding.grant.MirrorID, "frames": batch,
		})
	}
	batch, remainder, payload, marshalErr := eventwire.MarshalMirrorBatch(frames, eventwire.MirrorBatchMaxBytes, marshal)
	if marshalErr == nil && len(batch) == 0 && len(frames) > 0 && len(remainder) > 0 {
		remainder = remainder[1:]
	}
	m.requeue(remainder)
	if marshalErr != nil {
		m.requeue(batch)
		return true
	}
	if len(batch) == 0 && len(frames) > 0 {
		// A single frame larger than the HTTP protocol permits cannot ever be
		// delivered. Durable history remains authoritative for its content.
		m.wakeIfQueued()
		if !heartbeat {
			return true
		}
		payload, _ = marshal(nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.record.base+"/external/frames", bytes.NewReader(payload))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var resp *http.Response
	if err == nil {
		resp, err = binding.client.Do(req)
	}
	if err != nil {
		cancel()
		if !m.bindingCurrent(binding, revision) {
			return true
		}
		m.requeue(batch)
		return m.readoptLocked(binding, revision)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	cancel()
	if !m.bindingCurrent(binding, revision) {
		return true
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusConflict {
		m.requeue(batch)
		return m.readoptLocked(binding, revision)
	}
	if resp.StatusCode != http.StatusOK {
		m.requeue(batch)
		m.mu.Lock()
		if m.binding == binding && m.revision == revision {
			m.failures++
		}
		failures := m.failures
		m.mu.Unlock()
		if failures >= cliTakeoverRediscoverFailures {
			return m.readoptLocked(binding, revision)
		}
		return true
	}
	m.mu.Lock()
	if m.binding == binding && m.revision == revision {
		m.failures = 0
	}
	m.mu.Unlock()
	var out struct {
		ReclaimRequested bool   `json:"reclaimRequested"`
		ReclaimMode      string `json:"reclaimMode"`
	}
	if json.Unmarshal(body, &out) == nil && out.ReclaimRequested {
		m.requestYieldFor(binding, revision, out.ReclaimMode == "interrupt")
		return true
	}
	m.wakeIfQueued()
	return true
}

func (m *cliTakeoverManager) bindingCurrent(binding *cliTakeoverBinding, revision uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.binding == binding && m.revision == revision && !m.returned.Load()
}

// readoptLocked discovers the current Serve endpoint and rotates the mirror
// generation. sendMu is held by the caller, so a binding switch cannot race a
// late response from the endpoint being replaced.
func (m *cliTakeoverManager) readoptLocked(binding *cliTakeoverBinding, revision uint64) bool {
	if binding == nil || !m.bindingCurrent(binding, revision) {
		return false
	}
	conflicted := false
	for _, record := range discoverCLIServesForTakeover() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		client, err := cliServeClient(ctx, record)
		if err != nil {
			cancel()
			continue
		}
		payload, _ := json.Marshal(map[string]string{"sessionPath": binding.path, "writerId": processidentity.WriterID()})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, record.base+"/adopt", bytes.NewReader(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
		var resp *http.Response
		if err == nil {
			resp, err = client.Do(req)
		}
		if err != nil {
			cancel()
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		cancel()
		if resp.StatusCode == http.StatusConflict {
			conflicted = true
			continue
		}
		var grant cliTakeoverGrant
		if resp.StatusCode == http.StatusOK && json.Unmarshal(body, &grant) == nil &&
			grant.MirrorID != "" && grant.ReturnHandoffID != "" && grant.SourceWriterID != "" &&
			grant.TargetWriterID == processidentity.WriterID() &&
			grant.SessionPath == binding.path {
			m.mu.Lock()
			if m.binding == binding && m.revision == revision && !m.returned.Load() {
				next := *binding
				next.record, next.client, next.grant = record, client, grant
				m.binding = &next
				m.revision++
				m.failures = 0
			}
			m.mu.Unlock()
			return true
		}
	}
	if conflicted {
		m.requestYieldFor(binding, revision, false)
	}
	return true
}

func (m *cliTakeoverManager) requestYieldFor(binding *cliTakeoverBinding, revision uint64, interrupt bool) {
	if !m.bindingCurrent(binding, revision) {
		return
	}
	if !m.reclaiming.CompareAndSwap(false, true) {
		return
	}
	current, ctrl, callback, currentRevision := m.snapshot()
	if current != binding || currentRevision != revision {
		m.reclaiming.Store(false)
		return
	}
	if interrupt && ctrl != nil {
		ctrl.Cancel()
	}
	go func() {
		deadline := time.Now().Add(cliTakeoverTimeout)
		for cliControllerHasActiveRuntimeWork(ctrl) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if cliControllerHasActiveRuntimeWork(ctrl) {
			m.reclaiming.Store(false)
			return
		}
		if err := m.returnLeaseFor(binding, revision); err != nil {
			m.reclaiming.Store(false)
			return
		}
		if callback != nil {
			callback()
		}
	}()
}

func (m *cliTakeoverManager) returnLease() error {
	return m.returnLeaseFor(nil, 0)
}

func (m *cliTakeoverManager) returnLeaseFor(expected *cliTakeoverBinding, revision uint64) error {
	expectedPath := ""
	if expected != nil {
		if !m.bindingCurrent(expected, revision) {
			current, _, _, _ := m.snapshot()
			if current == nil || current.path != expected.path {
				return fmt.Errorf("takeover mirror changed before reclaim completed")
			}
		}
		expectedPath = expected.path
	}
	return m.returnMirrorTransaction(expectedPath, true, true)
}

// leaveMirror ends forwarding before another session binding is published.
func (m *cliTakeoverManager) leaveMirror(expectedPath string) error {
	if m == nil {
		return nil
	}
	current, _, _, _ := m.snapshot()
	if current == nil || m.returned.Load() {
		return nil
	}
	if expectedPath == "" {
		expectedPath = current.path
	}
	return m.returnMirrorTransaction(expectedPath, false, false)
}

func (m *cliTakeoverManager) mirrorEnd(binding *cliTakeoverBinding) {
	if m == nil {
		return
	}
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	m.mirrorEndLocked(binding)
}

func (m *cliTakeoverManager) mirrorEndLocked(binding *cliTakeoverBinding) {
	if binding == nil || binding.client == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"sessionPath": binding.path, "mirrorId": binding.grant.MirrorID})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, binding.record.base+"/mirror-end", bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := binding.client.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

func cliEndFailedHandoff(binding *cliTakeoverBinding) {
	if binding == nil {
		return
	}
	m := &cliTakeoverManager{}
	m.mirrorEnd(binding)
}

// Close returns an active mirrored session on ordinary CLI exit. A concurrent
// reclaim owns the same transaction; wait for it rather than publishing a
// second reservation.
func (m *cliTakeoverManager) Close() error {
	if m == nil {
		return nil
	}
	if !m.closed.CompareAndSwap(false, true) {
		return nil
	}
	var closeErr error
	if m.reclaiming.Load() {
		_, ctrl, _, _ := m.snapshot()
		deadline := time.Now().Add(cliTakeoverTimeout)
		for cliControllerHasActiveRuntimeWork(ctrl) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if cliControllerHasActiveRuntimeWork(ctrl) {
			return fmt.Errorf("timed out waiting for the active turn to yield its session")
		}
	}
	if err := m.returnLease(); err != nil {
		closeErr = err
	}
	m.mu.Lock()
	started, stop, done := m.started, m.stop, m.done
	m.mu.Unlock()
	if started {
		m.stopOnce.Do(func() { close(stop) })
		<-done
	}
	return closeErr
}
