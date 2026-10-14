package config

import (
	"deepzora/internal/permission"
	"deepzora/internal/permissionpreset"
	"fmt"
)

// normalizeLoadedConfig applies post-merge compatibility repairs and validates
// mode enums in their established order.
func normalizeLoadedConfig(cfg *Config) error {
	if err := validateCurrentConfigInputs(cfg); err != nil {
		return err
	}
	if err := validatePermissionPresetFields(cfg); err != nil {
		return err
	}
	normalizePluginCommandLines(cfg)
	repairProviderEndpointContracts(cfg)
	normalizeOfficialDeepSeekModels(cfg)
	freezeProviderBillingCurrencies(cfg)
	applyDeepSeekOfficialDefaultPricing(cfg)
	backfillDeepSeekOfficialPrices(cfg)
	normalizeEffortConfig(cfg)
	return nil
}

// Validate before scope merging can discard ignored project declarations.
func validateCurrentConfigInputs(cfg *Config) error {
	switch cfg.Desktop.LayoutStyle {
	case "", "workbench", "creation":
	default:
		return fmt.Errorf("unsupported desktop.layout_style %q", cfg.Desktop.LayoutStyle)
	}
	for _, rules := range [][]string{cfg.Permissions.Allow, cfg.Permissions.Ask, cfg.Permissions.Deny} {
		for _, raw := range rules {
			if _, ok := permission.ParseRule(raw); !ok {
				return fmt.Errorf("unsupported permission rule %q", raw)
			}
		}
	}
	for _, plugin := range cfg.Plugins {
		if pluginEntryTransport(plugin) == "sse" {
			return fmt.Errorf("plugin %q: retired SSE transport is unsupported; use streamable HTTP", plugin.Name)
		}
	}
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
	return nil
}
