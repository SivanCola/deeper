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
	return nil
}
