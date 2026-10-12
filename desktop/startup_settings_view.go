package main

// DesktopStartupSettingsView is the lightweight Settings subset needed during
// frontend startup. It deliberately excludes providers and credential state so
// slow keychain/env resolution stays off the first-render path.
type DesktopStartupSettingsView struct {
	DesktopLanguage      string   `json:"desktopLanguage"`
	DesktopLayoutStyle   string   `json:"desktopLayoutStyle"`
	DesktopTheme         string   `json:"desktopTheme"`
	DesktopThemeStyle    string   `json:"desktopThemeStyle"`
	DesktopTerminalTheme string   `json:"desktopTerminalTheme,omitempty"`
	SessionExperience    string   `json:"sessionExperience"`
	StatusBarStyle       string   `json:"statusBarStyle"`
	StatusBarItems       []string `json:"statusBarItems"`
	CheckUpdates         bool     `json:"checkUpdates"`
	UpdaterEnabled       bool     `json:"updaterEnabled"`
	UpdateChannel        string   `json:"updateChannel"`
	ConversationWidth    string   `json:"conversationWidth,omitempty"`
	// ConfigWarnings report in-memory recovery without rewriting user/project files.
	ConfigWarnings         []string `json:"configWarnings,omitempty"`
	ConfigWarningsRevision uint64   `json:"configWarningsRevision"`
	ConfigPath             string   `json:"configPath,omitempty"`
}
