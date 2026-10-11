package config

import (
	"deepzora/internal/permissionpreset"
	"fmt"
)

// normalizeLoadedConfig applies post-merge compatibility repairs and validates
// mode enums in their established order.
func normalizeLoadedConfig(cfg *Config) error {
	if err := validatePermissionPresetFields(cfg); err != nil {
		return err
	}
	normalizePluginCommandLines(cfg)
	// Project deepzora.toml files remain read-only during runtime loading, but
	// exact catalog routes can still safely repair a stale protocol in memory.
	// The user-global startup path persists the same repair before this load.
	repairProviderEndpointContracts(cfg)
	normalizeOfficialDeepSeekModels(cfg)
	freezeProviderBillingCurrencies(cfg)
	applyDeepSeekOfficialDefaultPricing(cfg)
	backfillDeepSeekOfficialPrices(cfg)
	normalizeEffortConfig(cfg)
	return nil
}

func validatePermissionPresetFields(cfg *Config) error {
	validate := func(path, raw string) error {
		if raw != "" && !permissionpreset.Valid(raw) {
			return fmt.Errorf("%s: unsupported permission preset %q", path, raw)
		}
		return nil
	}
	if err := validate("desktop.default_tool_approval_mode", cfg.Desktop.DefaultToolApprovalMode); err != nil {
		return err
	}
	if err := validate("bot.tool_approval_mode", cfg.Bot.ToolApprovalMode); err != nil {
		return err
	}
	if err := validate("bot.qq.tool_approval_mode", cfg.Bot.QQ.ToolApprovalMode); err != nil {
		return err
	}
	if err := validate("bot.dingtalk.tool_approval_mode", cfg.Bot.Dingtalk.ToolApprovalMode); err != nil {
		return err
	}
	for i, c := range cfg.Bot.Connections {
		if err := validate(fmt.Sprintf("bot.connections[%d].tool_approval_mode", i), c.ToolApprovalMode); err != nil {
			return err
		}
	}
	for i, r := range cfg.Bot.Routes {
		if err := validate(fmt.Sprintf("bot.routes[%d].tool_approval_mode", i), r.ToolApprovalMode); err != nil {
			return err
		}
	}
	return nil
}
