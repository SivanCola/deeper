package cli

import (
	"deepzora/internal/event"
	"deepzora/internal/i18n"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m chatTUI) maintenanceCancellable() bool {
	if m.maintenance == nil || m.ctrl == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(m.maintenance.Activity)) {
	case "", "starting", "running":
		return true
	default:
		return false
	}
}

func (m chatTUI) runningWorkingLine(cancelRequested, styled bool) string {
	if m.state != tuiRunning {
		if m.maintenance == nil {
			return ""
		}
		var label string
		switch strings.ToLower(strings.TrimSpace(m.maintenance.Activity)) {
		case "cancelling":
			label = i18n.M.CompactionStopping
		case "finalizing":
			label = i18n.M.CompactionSaving
		case "recovery_required":
			label = i18n.M.CompactionRecoveryRequired
		default:
			label = i18n.M.CompactionWorking
		}
		return fmt.Sprintf("  %s %s", m.spinner.View(), label)
	}
	if m.retryAttempt > 0 && !cancelRequested {
		if line, ok := m.waitingRecoveryLine(); ok {
			return line
		}

		return fmt.Sprintf("  "+i18n.M.ChatStatusRetryingFmt, m.spinner.View(), m.retryAttempt, m.retryMax)
	}

	var working string
	if cancelRequested {
		working = fmt.Sprintf("  "+i18n.M.ChatStatusCancellingFmt, m.spinner.View(), m.elapsed)
	} else {
		phaseLabel := m.readStatusLabel
		if phaseLabel == "" {
			phaseLabel = turnPhaseStatusLabel(m.turnPhase)
		}
		if phaseLabel != "" {
			working = fmt.Sprintf("  %s %s · %ds", m.spinner.View(), phaseLabel, m.elapsed)
		} else {
			working = fmt.Sprintf("  "+i18n.M.ChatStatusThinkingFmt, m.spinner.View(), m.elapsed)
		}
	}
	if m.turnTokens > 0 {
		working += " · ↓" + shortTokens(m.turnTokens)
	}
	if n := m.inboxQueuedCount(); n > 0 {
		var queued string
		if n == 1 {
			queued = " · ✎ 1 in inbox"
		} else {
			queued = fmt.Sprintf(" · ✎ %d in inbox", n)
		}
		if m.inboxSnap().Paused {
			queued += " (paused)"
		}
		if styled {
			working += dim(queued)
		} else {
			working += queued
		}
	}
	return working
}

func (m *chatTUI) runCompactCommand(input string) tea.Cmd {
	m.echoLocalCommand(input)
	// Register maintenance before returning so Esc and new input see its owner.
	// The worker runs in the background; trailing text remains summary guidance.
	result := m.ctrl.SubmitDisplayWithResult(input, input)
	if result.OperationID != "" {
		op := &event.SessionOperationInfo{
			OperationID: result.OperationID,
			Kind:        "compact",
			Activity:    "running",
			Status:      "running",
		}
		m.maintenance = op
		m.renderSessionOperation(op)
	}
	return nil
}

func (m *chatTUI) stopMaintenance() {
	m.ctrl.Cancel()
	updated := *m.maintenance
	updated.Activity = "cancelling"
	updated.Status = "cancelling"
	m.maintenance = &updated
	m.renderSessionOperation(&updated)
}
