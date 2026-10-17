package cli

import (
	"context"
	"errors"

	"deepzora/internal/control"
	"deepzora/internal/session"
)

// preparePresetResume uses the target identity before assembly, even when the
// same custom id is already active: the saved definition may have changed.
func (m *chatTUI) preparePresetResume(ref session.SessionRef, service *session.Service) (*control.Controller, error) {
	snapshot, err := service.Query().Snapshot(context.Background(), ref)
	if err != nil {
		return nil, err
	}
	current, ok := m.ctrl.(control.AgentPresets)
	if snapshot.Projection.AgentPreset == "" && (!ok || current.AgentPreset() == "") {
		return nil, nil
	}
	if m.buildController == nil {
		return nil, errors.New("resuming an agent preset requires the runtime rebuild seam")
	}
	if m.modelSwitchPending || m.runtimeSwitchBusy() {
		return nil, errors.New("finish active work and runtime switches before resuming an agent preset")
	}
	next, err := m.buildController(controllerBuildSpec{
		ModelRef: m.modelRef, AgentPresetSource: &ref, AgentPresetService: service,
	}, m.ctrl)
	if err != nil {
		return nil, err
	}
	next.EnableInteractiveApproval()
	next.SetToolApprovalMode(m.ctrl.ToolApprovalMode())
	return next, nil
}

func (m *chatTUI) publishPresetResume(next *control.Controller) error {
	if err := control.ActivateSessionAPIReplacement(m.ctrl, next); err != nil {
		return err
	}
	old := m.ctrl
	m.ctrl = activateGoalDriverAfterRebuild(next)
	m.lastBuildResult = nil
	m.label, m.modelRef = next.Label(), next.ModelRef()
	m.commands, m.skills = next.Commands(), next.SlashSkills()
	m.setHostAndInvalidateSlashCatalog(next.Host())
	m.updateWatchdogStatusProvider()
	m.refreshEffortStatus()
	if m.takeover != nil {
		m.takeover.AttachController(next)
	}
	if old != nil && old != next {
		m.oldControllers = append(m.oldControllers, old)
		if concrete, ok := old.(*control.Controller); ok {
			// A retained outgoing binding would leave its execution runtime active
			// and prevent returning to the session in this same process.
			if err := concrete.ReleaseSessionRuntimeBinding(); err != nil {
				m.notice("resume: release previous session: " + err.Error())
			}
		}
	}
	return nil
}
