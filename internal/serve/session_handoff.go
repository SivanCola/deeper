package serve

import (
	"context"
	"deepzora/internal/processidentity"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/session"
)

type handoffRequest struct {
	SessionPath    string `json:"sessionPath"`
	TargetWriterID string `json:"targetWriterId"`
	Force          bool   `json:"force"`
	Mode           string `json:"mode"`
	TimeoutMs      int    `json:"timeoutMs"`
}

// handoff releases a session Serve holds so a local runtime on this machine
// can take it over. With force unset it refuses while a remote client is
// attached — the caller is expected to have confirmed the takeover with its
// user via GET /ownership. wait drains a running turn; interrupt cancels it.
// Final-format identities may arrive as a "session-id:<id>" route or the
// explicit sessionId field; both take the identity release path, which swaps
// the writer lease instead of a path lease.
func (s *Server) handoff(w http.ResponseWriter, r *http.Request) {
	var body handoffRequest
	if err := decodeTakeoverJSON(w, r, &body); err != nil || strings.TrimSpace(body.SessionPath) == "" || strings.TrimSpace(body.TargetWriterID) == "" {
		if err == nil {
			http.Error(w, "missing sessionPath or targetWriterId", http.StatusBadRequest)
		}
		return
	}
	s.handoffIdentity(w, r, strings.TrimSpace(body.SessionPath), strings.TrimSpace(body.TargetWriterID), body)
}

// handoffIdentity releases a final-format identity the serve's foreground
// currently writes. The single-writer credential is the session directory's
// writer lock, so the release is: quiesce the foreground turn, flush, drop the
// foreground's binding without allocating a replacement identity, and
// synchronously close the handed-off runtime — its writer lock drops before the
// grant is answered.
func (s *Server) handoffIdentity(w http.ResponseWriter, r *http.Request, route, targetWriterID string, body handoffRequest) {
	ref, _, err := s.resolveSessionIdentity(route)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing, ok := s.mirroredEntry(route); ok {
		if existing.targetWriterID != targetWriterID {
			http.Error(w, "session is already handed off to another writer", http.StatusConflict)
			return
		}
		writeJSON(w, existing.grant("already_handed_off"))
		return
	}
	if !body.Force && s.bc.Subscribers() > 0 {
		http.Error(w, "session is attached to a remote client; retry with force after confirming the takeover", http.StatusConflict)
		return
	}
	mode := parseHandoffMode(body.Mode)
	timeout := handoffTimeout(body.TimeoutMs)
	// Drain or cancel outside bindMu, mirroring the legacy path.
	if err := s.quietIdentityForHandoff(ref, mode, timeout); err != nil {
		http.Error(w, err.Error(), statusForHandoffError(err))
		return
	}
	if handoffIdentityBeforeLockHookForTest != nil {
		handoffIdentityBeforeLockHookForTest()
	}

	s.bindMu.Lock()
	m, err := s.handoffIdentityLocked(r.Context(), route, ref, targetWriterID)
	s.bindMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), statusForHandoffError(err))
		return
	}
	writeJSON(w, m.grant("handed_off"))
}

// quietHandoffTarget answers the drain/cancel poll for one handoff target:
// held reports whether this serve currently runs the target, busy whether a
// turn is still active on it. Callers cancel toward idle in interrupt mode.
type quietHandoffTarget func() (ctrl control.SessionAPI, held, busy bool)

// quietHandoffLoop waits for (or cancels toward) an idle handoff target before
// the release transaction runs. It re-checks under bindMu afterwards: turn
// admission holds bindMu, so once the caller holds it and the target is idle,
// no new turn can start on it.
func quietHandoffLoop(target quietHandoffTarget, mode handoffMode, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ctrl, held, busy := target()
		if !held {
			return errSessionNotHeld
		}
		if busy && mode == handoffModeInterrupt && ctrl != nil {
			ctrl.Cancel()
		}
		if !busy {
			return nil
		}
		if time.Now().After(deadline) {
			if mode == handoffModeInterrupt {
				return fmt.Errorf("session did not stop within %s; retry", timeout)
			}
			return fmt.Errorf("session is still running after %s; retry with mode=interrupt to cancel it", timeout)
		}
		time.Sleep(handoffPollInterval)
	}
}

// quietIdentityForHandoff waits for (or cancels toward) an idle holder of the
// identity — the foreground or a detached session — before the release
// transaction runs.
func (s *Server) quietIdentityForHandoff(ref session.SessionRef, mode handoffMode, timeout time.Duration) error {
	return quietHandoffLoop(func() (control.SessionAPI, bool, bool) {
		if cur, ok := s.ctl().(*control.Controller); ok {
			if current, bound := cur.SessionRef(); bound && current == ref {
				return cur, true, controllerHasActiveRuntimeWork(cur)
			}
		}
		if d := s.detachedIdentityHolder(ref); d != nil {
			return d.ctrl, true, controllerHasActiveRuntimeWork(d.ctrl)
		}
		return nil, false, false
	}, mode, timeout)
}

// handoffIdentityBeforeLockHookForTest runs between the unlocked quiet probe
// and the locked release so tests can admit a turn in that window.
var handoffIdentityBeforeLockHookForTest func()

// identityHolderLocked resolves which controller of this serve runs ref: the
// foreground, else the detached session bound to it. Callers hold bindMu.
func (s *Server) identityHolderLocked(ref session.SessionRef) (*control.Controller, *detachedSession) {
	if cur, ok := s.ctl().(*control.Controller); ok && cur.UsesExclusiveSession() {
		if current, bound := cur.SessionRef(); bound && current == ref {
			return cur, nil
		}
	}
	if d := s.detachedIdentityHolder(ref); d != nil {
		if concrete, ok := d.ctrl.(*control.Controller); ok {
			return concrete, d
		}
	}
	return nil, nil
}

// handoffIdentityLocked performs the identity release. Callers hold bindMu and
// have already quieted the holder; the busy state is re-checked here because
// turn admission also holds bindMu, so an idle holder observed under the lock
// cannot start a turn before the release completes.
func (s *Server) handoffIdentityLocked(ctx context.Context, route string, ref session.SessionRef, targetWriterID string) (mirroredSession, error) {
	holder, detached := s.identityHolderLocked(ref)
	if holder == nil {
		return mirroredSession{}, errSessionNotHeld
	}
	if controllerHasActiveRuntimeWork(holder) {
		return mirroredSession{}, errHandoffBusyAgain
	}
	service := holder.SessionService()
	if service == nil {
		return mirroredSession{}, errors.New("handoff: session service unavailable")
	}
	// The runtime can still be finalizing a turn the controller already reports
	// as done; Close would refuse it as busy, so treat it as busy up front.
	if live, ok := service.Runtime(ref); ok && live.Snapshot().Phase.Busy() {
		return mirroredSession{}, errHandoffBusyAgain
	}
	m, err := newMirroredSession(route, processidentity.WriterID(), targetWriterID, mirrorPhasePending)
	if err != nil {
		return mirroredSession{}, fmt.Errorf("handoff: create generation: %w", err)
	}
	var taken *detachedSession
	if detached != nil {
		// Transfer ownership from the close-on-idle watcher before releasing,
		// before releasing its writer; a retiring entry is
		// already closing and must not be handed off.
		taken = s.takeDetached(detached.path)
		if taken == nil {
			return mirroredSession{}, errHandoffBusyAgain
		}
		if controllerHasActiveRuntimeWork(holder) {
			_, _ = s.registerDetached(taken.ctrl, taken.tag)
			return mirroredSession{}, errHandoffBusyAgain
		}
	}
	restore := func() {
		s.reattachAfterFailedHandoff(ctx, holder, ref)
		if taken != nil {
			_, _ = s.registerDetached(taken.ctrl, taken.tag)
		}
	}
	// Flush and unbind without allocating a replacement. The holder is left never-bound, so the next turn or
	// /new allocates lazily and no empty canonical row is left in /sessions.
	if err := holder.ReleaseSessionForHandoff(); err != nil {
		restore()
		return mirroredSession{}, fmt.Errorf("handoff: release session binding: %w", err)
	}
	// Deterministic writer release: Close drops the writer lock now instead of
	// waiting out the idle-retirement TTL, so the taker's open cannot race a
	// lingering lease. A refused close re-attaches the holder to the live runtime.
	if err := service.Close(ctx, ref); err != nil {
		restore()
		if errors.Is(err, session.ErrRuntimeBusy) {
			return mirroredSession{}, errHandoffBusyAgain
		}
		return mirroredSession{}, fmt.Errorf("handoff: release session writer: %w", err)
	}
	if taken != nil {
		holder.Close()
		s.forgetSessionTag(holder)
	} else {
		// Re-point the frame tag: a stale tag stamps live frames with the
		// handed-off identity and the desktop pump drops them as background.
		s.setControllerIdentity(holder)
	}
	s.markMirrored(m)
	slog.Info("serve: final-format session handed off to local runtime", "session", route)
	s.bc.Emit(event.Event{
		Kind:        event.Notice,
		Level:       event.LevelWarn,
		Code:        event.NoticeCodeSessionTakenOver,
		Text:        "This session was taken over by a local Deepzora window and is read-only here.",
		Detail:      "A Deepzora window on this machine took over the conversation. It keeps streaming here; use \"take back\" to reclaim it.",
		SessionPath: route,
	})
	return m, nil
}

// reattachAfterFailedHandoff restores the foreground's binding to an identity
// whose release did not complete. The runtime is still the service's live
// instance (a refused close never retires it), so OpenSession re-binds and
// re-projects it; only when even that fails is the controller left unbound,
// which the next turn resolves by allocating lazily.
func (s *Server) reattachAfterFailedHandoff(ctx context.Context, concrete *control.Controller, ref session.SessionRef) {
	if cur, bound := concrete.SessionRef(); bound && cur == ref {
		return
	}
	if _, err := concrete.OpenSession(ctx, ref); err != nil {
		slog.Error("serve: re-attach identity after failed handoff", "session", ref.SessionID, "err", err)
	}
}

func parseHandoffMode(raw string) handoffMode {
	if handoffMode(raw) == handoffModeInterrupt {
		return handoffModeInterrupt
	}
	return handoffModeWait
}

func handoffTimeout(ms int) time.Duration {
	if ms <= 0 {
		return handoffDefaultTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func statusForHandoffError(err error) int {
	switch {
	case errors.Is(err, errSessionNotHeld), errors.Is(err, errHandoffBusyAgain):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

var (
	errSessionNotHeld   = errors.New("session is not held by this serve process")
	errHandoffBusyAgain = errors.New("session became busy again during handoff; retry")
)
