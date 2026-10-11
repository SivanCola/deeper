package config

import "strings"

// DesktopConfig controls desktop-only UI preferences. It is intentionally
// separate from top-level language and [ui] so desktop choices do not affect CLI
// language, terminal colours, or provider-visible prompt/request data.
type DesktopConfig struct {
	Language                string   `toml:"language"`                   // auto|en|zh; empty/auto = browser/OS auto-detect
	LayoutStyle             string   `toml:"layout_style"`               // workbench|creation
	Theme                   string   `toml:"theme"`                      // auto|dark|light; empty resolves to auto
	ThemeStyle              string   `toml:"theme_style"`                // graphite|aurora|slate|carbon|nocturne|amber
	TerminalTheme           string   `toml:"terminal_theme"`             // auto|dark|light; auto follows the desktop app theme
	ExternalOpener          string   `toml:"external_opener"`            // preferred installed app used by the desktop Open control
	CloseBehavior           string   `toml:"close_behavior"`             // quit|background; desktop window close behavior
	StatusBarStyle          string   `toml:"status_bar_style"`           // icon|text; desktop status bar metric labels
	StatusBarItems          []string `toml:"status_bar_items"`           // ordered visible desktop status bar items
	DefaultToolApprovalMode string   `toml:"default_tool_approval_mode"` // read-only|workspace-write|danger-full-access; defaults to workspace-write
	CheckUpdates            *bool    `toml:"check_updates"`              // startup update checks; nil keeps the default enabled
	Telemetry               *bool    `toml:"telemetry"`                  // anonymous launch ping plus scrubbed next-launch native crash diagnostics; nil keeps the default enabled
	Metrics                 *bool    `toml:"metrics"`                    // aggregate desktop metrics (anonymous signal/bucket counts, including lifecycle health; no content); nil keeps the default enabled
	ProviderAccess          []string `toml:"provider_access"`            // desktop-only list of provider entries shown in Settings > Model > Access
	SessionExperience       string   `toml:"session_experience"`         // standard|deep; canonical desktop transcript experience
	ConversationWidth       string   `toml:"conversation_width"`         // standard|full; max transcript width; empty = standard
}

// DesktopExternalOpener returns the selected opener id; unavailable ids fall
// back to the platform file manager in the desktop shell.
func (c *Config) DesktopExternalOpener() string {
	if c == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.Desktop.ExternalOpener))
}
