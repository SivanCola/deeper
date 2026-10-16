package serve

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"deepzora/internal/boot"
	"deepzora/internal/control"
)

func TestDetachedModelSettingsRefreshTargetsItsOwnerAndPreservesFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, foreground, _, _ := newExclusiveSessionServe(t)
			_, old, _, _ := newExclusiveSessionServeWithOptions(t, func(options *control.Options) {
				options.ModelRef, options.ModelSettingsRevision = "p/m", "old"
				options.ModelSettingsCurrent = func() (string, error) { return "new", nil }
			})
			identity, err := old.BindFreshSession(t.Context(), "background")
			if err != nil {
				t.Fatal(err)
			}
			path := remoteSessionIDQueryPrefix + identity.SessionID

			d := &detachedSession{ctrl: old, path: path, buildOptions: boot.Options{StatsSource: "serve"}}
			s.detached = map[string]*detachedSession{path: d}
			builds := 0
			s.rebuildControllerWithOptions = func(_ context.Context, current *control.Controller, ref string, opts boot.Options) (*control.Controller, error) {
				builds++
				if current != old || opts.SessionDir != old.SessionDir() || ref != "p/m" {
					t.Fatal("rebuild used another runtime's identity")
				}
				if fail {
					return nil, fmt.Errorf("injected background build failure")
				}
				return serveModelReplacement(opts, func(options *control.Options) {
					options.ModelSettingsRevision = "new"
					options.ModelSettingsCurrent = func() (string, error) { return "new", nil }
				}), nil
			}
			release, err := s.beforeInboxDispatch(old)
			if release != nil {
				release()
				t.Fatal("the outgoing runtime was admitted after a settings change")
			}
			if s.ctl() != foreground || builds != 1 {
				t.Fatal("background application changed the foreground owner")
			}
			if fail {
				if err == nil || d.ctrl != old {
					t.Fatal("failed background build replaced its owner")
				}
				return
			}
			if !errors.Is(err, control.ErrInboxRuntimeUnpublished) || d.ctrl == old {
				t.Fatalf("replacement was not published: %v", err)
			}
			next := d.ctrl.(*control.Controller)
			defer func() { next.Close(); <-next.Closed() }()
			release, err = s.beforeInboxDispatch(next)
			if err != nil || release == nil {
				t.Fatalf("replacement was not admitted: %v", err)
			}
			release()
			if builds != 1 {
				t.Fatal("unchanged background settings rebuilt again")
			}
		})
	}
}

func TestDetachedModelSettingsKeepsQueuedOwnerUntilAdmission(t *testing.T) {
	s, ctrl, _, identity := newExclusiveSessionServe(t)
	ctrl.SetBeforeInboxDispatch(func(*control.Controller) (func(), error) {
		return nil, control.ErrInboxRuntimeUnpublished
	})
	if _, err := ctrl.EnqueueInbox(control.InboxRequest{Submit: "next background run"}); err != nil {
		t.Fatal(err)
	}
	d := &detachedSession{path: remoteSessionIDQueryPrefix + identity.SessionID, ctrl: ctrl}
	s.detached = map[string]*detachedSession{d.path: d}
	if !s.detachedHasPendingWork(d) || d.retiring {
		t.Fatal("close-on-idle discarded the pending admission owner")
	}
	if err := ctrl.SetInboxPausedPassive(true); err != nil {
		t.Fatal(err)
	}
	if s.detachedHasPendingWork(d) || !d.retiring {
		t.Fatal("paused durable work prevented owner retirement")
	}
}
