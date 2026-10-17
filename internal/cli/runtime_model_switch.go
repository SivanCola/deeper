package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"deepzora/internal/control"
	"deepzora/internal/i18n"
)

func (m *chatTUI) handleModelSwitch(msg modelSwitchMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if msg.resumeTurn != nil && msg.err == nil && m.ctrl != msg.oldCtrl {
		// The user left the originating session while the replacement built.
		if concrete, ok := msg.ctrl.(*control.Controller); ok {
			concrete.ReleaseResources()
		} else if msg.ctrl != nil {
			msg.ctrl.Close()
		}
		return nil
	}
	m.modelSwitchPending = false
	m.pendingModelSwitch = nil
	if msg.err != nil {
		prefix := msg.failurePrefix
		if prefix == "" {
			prefix = "model"
		}
		m.notice(prefix + ": " + msg.err.Error())
		// A failed candidate leaves the current identity and its mirror intact.
		m.syncTakeoverIdentity()
	} else {
		if err := control.ActivateSessionAPIReplacement(msg.oldCtrl, msg.ctrl); err != nil {
			if concrete, ok := msg.ctrl.(*control.Controller); ok {
				concrete.ReleaseResources()
			} else if msg.ctrl != nil {
				msg.ctrl.Close()
			}
			m.notice("runtime activation: " + err.Error())
			m.syncTakeoverIdentity()
			return cmds
		}
		m.ctrl = activateGoalDriverAfterRebuild(msg.ctrl)
		if err := m.ctrl.Snapshot(); err != nil {
			m.notice("runtime snapshot: " + err.Error())
		}
		if m.takeover != nil {
			m.takeover.AttachController(msg.ctrl)
		}
		m.updateWatchdogStatusProvider()
		m.label = msg.label
		m.commands = msg.commands
		m.skills = msg.skills
		m.setHostAndInvalidateSlashCatalog(msg.host)
		m.modelRef = msg.ref
		m.refreshEffortStatus()
		// Defer Close to exit; skip when subgraph rebuild reused the pointer.
		if msg.oldCtrl != nil && msg.oldCtrl != msg.ctrl {
			m.oldControllers = append(m.oldControllers, msg.oldCtrl)
		}
		// Preserve a mirror only while its captured identity is still current.
		m.syncTakeoverIdentity()
		if msg.successNotice != "" {
			m.notice(msg.successNotice)
		} else {
			m.notice(fmt.Sprintf(i18n.M.ModelSwitchedFmt, m.label))
		}
		cmds = append(cmds, fetchBalance(m.ctrl))
		if msg.resumeTurn != nil {
			cmds = append(cmds, m.resumeControllerTurn(*msg.resumeTurn, false))
		}
		if c := m.runStatusline(); c != nil {
			cmds = append(cmds, c)
		}
		// Keep the existing waitForAgentEvent reader: a second consumer
		// would race it and deliver streamed events out of order.
	}

	// A /reload queued behind this switch runs now that it settled. On a
	// failed switch the old controller still serves, so the reload simply
	// retries against it.
	if c := m.drainQueuedRuntimeReload(); c != nil {
		cmds = append(cmds, c)
	}
	return cmds
}
