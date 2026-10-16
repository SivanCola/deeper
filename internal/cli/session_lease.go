package cli

import (
	"deepzora/internal/control"
)

func cliControllerHasActiveRuntimeWork(ctrl control.SessionAPI) bool {
	if ctrl == nil {
		return false
	}
	status := ctrl.RuntimeStateSnapshot()
	return status.Running || status.PendingPrompt || status.BackgroundJobs > 0
}

// syncTakeoverIdentity retires a mirror only after a successful identity
// change. Model rebuilds keep the same Runtime and its existing mirror.
func (m *chatTUI) syncTakeoverIdentity() {
	if m == nil || m.takeover == nil {
		return
	}
	identity, ok := m.ctrl.(control.IdentityLifecycle)
	if !ok {
		return
	}
	ref, bound := identity.SessionRef()
	if !bound {
		return
	}
	binding, _, _, _ := m.takeover.snapshot()
	if binding == nil || binding.path == cliCanonicalRoute(ref.SessionID) {
		return
	}
	if err := m.takeover.leaveMirror(binding.path); err != nil {
		m.notice("finish session mirror: " + err.Error())
	}
}
