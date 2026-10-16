package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"deepzora/internal/control"
	"deepzora/internal/i18n"
	"deepzora/internal/session"
)

// cliCanonicalRoutePrefix marks a takeover/resume target as a final-format
// identity instead of a legacy transcript path. It matches the serve and
// desktop routing prefix for exclusive identity sessions.
const cliCanonicalRoutePrefix = "session-id:"

func isCLICanonicalRoute(target string) bool {
	return strings.HasPrefix(strings.TrimSpace(target), cliCanonicalRoutePrefix)
}

func cliCanonicalRouteID(target string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(target), cliCanonicalRoutePrefix)
	return id, ok && id != ""
}

func cliCanonicalRoute(sessionID string) string {
	return cliCanonicalRoutePrefix + strings.TrimSpace(sessionID)
}

// sessionWriterHeldNotice is the friendly refusal for a final-format session
// whose writer another runtime owns.
func sessionWriterHeldNotice() string {
	return "this session is written by another Deepzora runtime on this machine; run /takeover to take it over"
}

// cliServeUnreachableError marks a takeover attempt that never obtained a
// serve's verdict: the dial, the token exchange, or the request itself failed
// at the transport. Only this kind of failure justifies a re-discovery pass.
// An HTTP verdict — a refusal, "still running; retry with mode=interrupt", an
// invalid grant — is the serve's answer and repeating the round would only
// repeat the same bounded wait.
type cliServeUnreachableError struct{ err error }

func (e *cliServeUnreachableError) Error() string { return e.err.Error() }
func (e *cliServeUnreachableError) Unwrap() error { return e.err }

func cliServeUnreachable(err error) bool {
	var unreachable *cliServeUnreachableError
	return errors.As(err, &unreachable)
}

// cliTakeoverIdentityHeldSession asks every resident serve to hand the
// final-format identity over. The writer lock carries no PID, so discovery is
// exhaustive rather than PID-matched; the serve that actually holds the
// identity grants, the others refuse.
func cliTakeoverIdentityHeldSession(route string, manager *cliTakeoverManager) (*cliTakeoverBinding, error) {
	if manager != nil && manager.Reclaiming() {
		return nil, fmt.Errorf("the remote side is reclaiming the current session")
	}
	// One re-discovery pass, and only when no serve could be reached: a
	// desktop reconnect respawns the serve and rewrites its state file, so an
	// all-unreachable round may simply have raced the restart. PIDs of dead
	// serves are pruned during discovery.
	var lastErr error
	for range 2 {
		records := discoverCLIServesForTakeover()
		if len(records) == 0 {
			break
		}
		answered := false
		lastErr = nil
		for i := range records {
			binding, err := cliTakeoverIdentityFromServe(route, &records[i])
			if err == nil {
				return binding, nil
			}
			if !cliServeUnreachable(err) {
				// A serve that answered is the one worth reporting.
				answered = true
				lastErr = err
			} else if lastErr == nil {
				lastErr = err
			}
		}
		if answered {
			break
		}
	}
	if lastErr == nil {
		return nil, fmt.Errorf("no resident serve on this machine holds this session; check that the desktop is connected and retry")
	}
	return nil, lastErr
}

// cliTakeoverIdentityFromServe performs one serve's identity handoff. The
// grant must name the exact route this process asked for.
func cliTakeoverIdentityFromServe(route string, record *cliServeRecord) (*cliTakeoverBinding, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTakeoverTimeout+15*time.Second)
	defer cancel()
	client, err := cliServeClient(ctx, *record)
	if err != nil {
		return nil, fmt.Errorf("takeover from local serve: %w", err)
	}
	grant, err := postCLITakeoverHandoff(ctx, client, record.base, route, "takeover from local serve")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(grant.SessionPath) != route {
		return nil, fmt.Errorf("invalid handoff grant")
	}
	return &cliTakeoverBinding{path: route, record: *record, client: client, grant: grant}, nil
}

// takeoverTarget resolves a route within its selected catalog. Explicit picker
// and reclaimed targets retain their store even after the controller is detached.
func (m *chatTUI) takeoverTarget(route string) (cliResumeTarget, error) {
	id, ok := cliCanonicalRouteID(route)
	if !ok {
		return cliResumeTarget{}, errors.New("invalid session identity")
	}
	for _, target := range []cliResumeTarget{m.pendingTakeoverTarget, m.reclaimedTarget} {
		if target.ref.SessionID == id && target.service != nil {
			return target, nil
		}
	}
	if identity, ok := m.ctrl.(control.IdentityLifecycle); ok {
		if service := identity.SessionService(); service != nil {
			ref := session.SessionRef{HostID: service.HostID(), SessionID: id}
			if _, err := service.SessionDir(context.Background(), ref); err == nil {
				return cliResumeTarget{ref: ref, service: service}, nil
			}
		}
	}
	service := cliSessionService(m.ctrl.SessionDir())
	if service == nil {
		return cliResumeTarget{}, errors.New("session service unavailable")
	}
	ref := session.SessionRef{HostID: service.HostID(), SessionID: id}
	if _, err := service.SessionDir(context.Background(), ref); err != nil {
		return cliResumeTarget{}, fmt.Errorf("unknown session: %w", err)
	}
	return cliResumeTarget{ref: ref, service: service}, nil
}

// runCanonicalTakeoverCommand handles "/takeover" for a final-format identity:
// every resident serve is asked to hand the writer over; on grant the
// controller attaches through OpenSession and the mirror manager forwards
// frames so the remote tab keeps rendering read-only. When no runtime holds
// the identity any more there is nothing to hand over and it is resumed
// directly, which is what the reclaim notice promises after the desktop has
// closed the session it took back.
func (m *chatTUI) runCanonicalTakeoverCommand(route string) {
	if m.ctrl.Running() {
		m.notice(i18n.M.ResumeBusy)
		return
	}
	target, err := m.takeoverTarget(route)
	if err != nil {
		m.notice("takeover: " + err.Error())
		return
	}
	if resumeEntryIsActive(m.ctrl, resumeEntry{target: target}) {
		m.notice(i18n.M.ResumeAlreadyActive)
		return
	}
	detached := m.sessionDetached()
	if !detached {
		if err := m.ctrl.Snapshot(); err != nil {
			m.notice("takeover: snapshot current session: " + err.Error())
			return
		}
		m.syncTakeoverIdentity()
	}
	binding, takeoverErr := cliTakeoverIdentityHeldSession(route, m.takeover)
	if takeoverErr != nil {
		m.resumeUnheldCanonicalSession(target, takeoverErr)
		return
	}
	if err := m.commitResumeTarget(target); err != nil {
		cliEndFailedHandoff(binding)
		m.notice("takeover: " + err.Error())
		return
	}
	m.pendingTakeoverTarget = cliResumeTarget{}
	if m.takeover != nil {
		m.takeover.AttachController(m.ctrl)
		m.takeover.Activate(binding)
	}
	m.resumeAfterReclaim()
	m.replayActiveBranch(i18n.M.ResumedTitle)
	m.notice("session taken over; the remote side is now read-only and can take it back")
}

// resumeUnheldCanonicalSession runs after no serve granted the identity. The
// writer lock is the authority on whether the refusal mattered: a session
// nobody holds (the desktop closed it after reclaiming, or the holder exited)
// is simply resumed, while a still-held one reports why the handoff failed
// rather than the generic ownership error.
func (m *chatTUI) resumeUnheldCanonicalSession(target cliResumeTarget, takeoverErr error) {
	if err := m.commitResumeTarget(target); err != nil {
		if errors.Is(err, session.ErrWriterOwned) {
			m.notice("takeover: " + takeoverErr.Error())
			return
		}
		m.notice("takeover: " + err.Error())
		return
	}
	m.pendingTakeoverTarget = cliResumeTarget{}
	m.resumeAfterReclaim()
	m.replayActiveBranch(i18n.M.ResumedTitle)
	m.notice("session resumed; no other runtime holds it")
}

// cliStartupCanonicalTakeover is the startup counterpart: after a confirmed
// prompt (or --takeover), the resident serve releases the identity and this
// process attaches as the new writer.
func cliStartupCanonicalTakeover(ctrl control.SessionAPI, manager *cliTakeoverManager, target cliResumeTarget) error {
	route := cliCanonicalRoute(target.ref.SessionID)
	binding, err := cliTakeoverIdentityHeldSession(route, manager)
	if err != nil {
		return err
	}
	identity, ok := ctrl.(control.IdentityLifecycle)
	if !ok {
		cliEndFailedHandoff(binding)
		return errors.New("session resume requires the session engine")
	}
	if _, err := identity.OpenSessionFromService(context.Background(), resumeTargetService(target), target.ref); err != nil {
		cliEndFailedHandoff(binding)
		return err
	}
	if manager != nil {
		manager.AttachController(ctrl)
		manager.Activate(binding)
	}
	return nil
}
