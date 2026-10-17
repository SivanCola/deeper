package cli

import (
	"context"

	"deepzora/internal/session"

	tea "charm.land/bubbletea/v2"
)

// webHandoffState is populated by /web before the TUI restores the terminal.
// The Web controller resumes the same current session identity.
type webHandoffState struct {
	launchWebOnExit    bool
	launchWebSessionID string
	launchWebModelRef  string
}

func (m *chatTUI) runHelpOrWebSlash(input, command string) tea.Cmd {
	m.echoLocalCommand(input)
	if command == "/help" {
		m.showHelp()
		return nil
	}
	return m.runWebSlash()
}

func (m *chatTUI) runWebSlash() tea.Cmd {
	if m.runtimeSwitchBusy() {
		m.notice("wait for active work to finish, then retry /web")
		return nil
	}
	ref, err := m.ctrl.EnsureSession(context.Background(), session.CreateOptions{})
	if err != nil {
		m.notice("web: could not bind the current session: " + err.Error())
		return nil
	}
	if err := m.ctrl.Snapshot(); err != nil {
		m.notice("web: could not save the current session: " + err.Error())
		return nil
	}
	m.syncTakeoverIdentity()
	m.launchWebOnExit = true
	m.launchWebSessionID = ref.SessionID
	m.launchWebModelRef = m.modelRef
	return shutdownNow
}

func webHandoffArgs(sessionID, modelRef string) []string {
	args := []string{}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	if modelRef != "" {
		args = append(args, "--model", modelRef)
	}
	return args
}
