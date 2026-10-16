package main

import (
	"fmt"

	"deepzora/internal/boot"
	"deepzora/internal/control"
	"deepzora/internal/session"
)

// sessionBinding is intentionally smaller than the public desktop control
// surface. Rebuilds preserve the exact host-owned Runtime.
type persistedSessionBinding interface {
	SessionBinding() (*session.Service, *session.Runtime, bool)
}

func exclusiveSessionBinding(ctrl control.SessionAPI) (*session.Service, *session.Runtime, bool) {
	bound, ok := ctrl.(persistedSessionBinding)
	if !ok || bound == nil {
		return nil, nil, false
	}
	service, runtime, exclusive := bound.SessionBinding()
	return service, runtime, exclusive && service != nil && runtime != nil
}

// buildDesktopControllerReplacement preserves the current identity through a
// runtime rebuild; an empty tab gets a current session before publication.
func (a *App) buildDesktopControllerReplacement(tab *WorkspaceTab, old control.SessionAPI, opts boot.Options) (control.SessionAPI, error) {
	if old != nil {
		concrete, ok := old.(*control.Controller)
		if !ok || !concrete.UsesExclusiveSession() {
			return nil, fmt.Errorf("replacement requires a current session controller")
		}
		if opts.SessionTemp == nil {
			opts.SessionTemp = concrete.SessionTemp()
		}
		result, err := rebuildTabRuntime(a, tab, concrete, opts)
		if err != nil {
			return nil, err
		}
		if err := a.runRebindCandidateHook("runtime_candidate_built"); err != nil {
			discardReplacementController(result.Controller, old)
			return nil, err
		}
		return result.Controller, nil
	}
	ctrl, err := a.buildTabControllerBoot(a.bootContext(), opts)
	if err != nil {
		return nil, err
	}
	identity, ok := ctrl.(control.IdentityLifecycle)
	if !ok {
		ctrl.Close()
		return nil, fmt.Errorf("replacement session identity is unavailable")
	}
	snap := a.tabRuntimeSnapshot(tab)
	if _, _, err := a.bindTabCanonicalSession(a.bootContext(), identity, nil, snap.scope, snap.workspaceRoot, snap.sessionID, opts.Model, false); err != nil {
		ctrl.Close()
		return nil, err
	}
	return ctrl, nil
}

// retireReplacedController must not close a SessionRuntime still used by its
// replacement. ReleaseResources tears down only the retired Agent generation.
func retireReplacedController(old, replacement control.SessionAPI) {
	if old == nil || old == replacement {
		return
	}
	_, oldRuntime, oldExclusive := exclusiveSessionBinding(old)
	_, newRuntime, newExclusive := exclusiveSessionBinding(replacement)
	if oldExclusive && newExclusive && oldRuntime == newRuntime {
		if concrete, ok := replacement.(*control.Controller); ok {
			concrete.ActivateGoalDriverAfterRebuild()
		}
		if concrete, ok := old.(*control.Controller); ok {
			concrete.ReleaseResources()
			return
		}
	}
	old.Close()
}

// activateReplacementController is called only inside the host's final
// compare-and-publish critical section. It transfers an exclusive Session
// Runtime without letting construction-time candidates steal Stop or commit
// authority from the controller still visible in the tab.
func activateReplacementController(old, replacement control.SessionAPI) error {
	return control.ActivateSessionAPIReplacement(old, replacement)
}

func discardReplacementController(candidate, current control.SessionAPI) {
	if candidate == nil || candidate == current {
		return
	}
	_, candidateRuntime, candidateExclusive := exclusiveSessionBinding(candidate)
	_, currentRuntime, currentExclusive := exclusiveSessionBinding(current)
	if candidateExclusive && currentExclusive && candidateRuntime == currentRuntime {
		if concrete, ok := candidate.(*control.Controller); ok {
			concrete.ReleaseResources()
			return
		}
	}
	candidate.Close()
}
