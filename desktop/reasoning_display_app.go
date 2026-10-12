package main

import (
	"runtime"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/provider"
	"deepzora/internal/sandbox"
)

func desktopStartupSettingsFromConfig(cfg *config.Config) DesktopStartupSettingsView {
	if cfg == nil {
		return DesktopStartupSettingsView{
			DesktopLayoutStyle: "workbench",
			DesktopTheme:       "auto", DesktopThemeStyle: "graphite", DesktopTerminalTheme: "auto",
			SessionExperience: "standard", StatusBarStyle: "icon",
			StatusBarItems: config.DefaultDesktopStatusBarItems(), CheckUpdates: false, UpdaterEnabled: desktopUpdaterEnabled(),
			UpdateChannel: "stable", ConversationWidth: "standard",
		}
	}
	return DesktopStartupSettingsView{
		DesktopLanguage:    cfg.DesktopLanguage(),
		DesktopLayoutStyle: cfg.DesktopLayoutStyle(), DesktopTheme: cfg.DesktopTheme(),
		DesktopThemeStyle: cfg.DesktopThemeStyle(), DesktopTerminalTheme: cfg.DesktopTerminalTheme(),
		SessionExperience: cfg.DesktopSessionExperience(),
		StatusBarStyle:    cfg.DesktopStatusBarStyle(),
		StatusBarItems:    cfg.DesktopStatusBarItems(), CheckUpdates: cfg.DesktopCheckUpdates(), UpdaterEnabled: desktopUpdaterEnabled(),
		UpdateChannel: cfg.DesktopUpdateChannel(), ConversationWidth: cfg.DesktopConversationWidth(),
		ConfigWarnings: cfg.LoadWarnings(), ConfigPath: config.UserConfigPath(),
	}
}

func (a *App) defaultSettingsView() SettingsView {
	defaults := config.Default()
	return SettingsView{
		WebSearchModels: []string{},
		Providers:       []ProviderView{}, OfficialProviders: officialProviderViews(map[string]bool{}),
		ProviderPresets: providerPresetViewsForRootWithResolver(nil, a.activeWorkspaceRoot(), nil),
		ProviderKinds:   nonNil(provider.Kinds()),
		Permissions:     PermissionsView{Mode: "ask", Allow: []string{}, Ask: []string{}, Deny: []string{}},
		Sandbox: SandboxView{Bash: defaults.BashMode(), AllowWrite: []string{}, EffectiveWriteRoots: []string{}, Shell: "auto",
			EffectiveShell:      sandboxEffectiveShellView(sandbox.ResolveShell("", "", nil)),
			ResolvedShell:       sandboxEffectiveShellView(sandbox.ResolveShell("", "", nil)),
			ShellCapabilities:   sandboxCapabilityViews("", ""),
			GitCapability:       gitCapabilityView("", ""),
			ShellRepairGuidance: shellRepairGuidanceForGOOS(runtime.GOOS),
			GitRepairGuidance:   gitRepairGuidanceForGOOS(runtime.GOOS)},
		Agent: AgentView{
			MaxSubagentDepth:       agent.DefaultMaxSubagentDepth,
			MaxSubagentConcurrency: agent.DefaultMaxSubagentConcurrency, MaxParallelWriters: agent.DefaultMaxParallelWriters,
			ReasoningLanguage: "auto",
			CompactRatio:      defaults.Agent.CompactRatio, EffectiveCompactRatio: defaults.Agent.CompactRatio,
		},
		DesktopLayoutStyle: "workbench",
		DesktopTheme:       "auto", DesktopThemeStyle: "graphite", DesktopTerminalTheme: "auto",
		CloseBehavior:  "background",
		StatusBarStyle: "icon", StatusBarItems: config.DefaultDesktopStatusBarItems(), SessionExperience: "standard",
		DefaultToolApprovalMode: "workspace-write", CheckUpdates: false, UpdaterEnabled: desktopUpdaterEnabled(), UpdateChannel: "stable",
		Telemetry: true, Metrics: true, ConversationWidth: "standard",
	}
}

// SetSessionExperience persists the canonical two-state desktop presentation.
func (a *App) SetSessionExperience(mode string) error {
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopSessionExperience(mode) })
}
