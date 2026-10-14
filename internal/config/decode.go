package config

import (
	"fmt"

	"github.com/BurntSushi/toml"

	fileencoding "deepzora/internal/fileutil/encoding"
)

func decodeTOMLFile(path string, v any) (toml.MetaData, error) {
	resolved, err := resolveConfigReadPath(path)
	if err != nil {
		return toml.MetaData{}, err
	}
	return decodeTOMLFileResolved(resolved, v)
}

func decodeTOMLFileResolved(path string, v any) (toml.MetaData, error) {
	data, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return toml.MetaData{}, err
	}
	return decodeTOMLBytes(data, v)
}

func decodeTOMLBytes(data []byte, v any) (toml.MetaData, error) {
	text := string(fileencoding.DecodeToUTF8(data))
	meta, err := toml.Decode(text, v)
	if err != nil {
		return meta, err
	}
	if cfg, ok := v.(*Config); ok {
		if err := validateCurrentConfigInputs(cfg); err != nil {
			return meta, err
		}
		if meta.IsDefined("config_version") && cfg.ConfigVersion != CurrentConfigVersion {
			return meta, fmt.Errorf("unsupported config_version %d; current schema is %d", cfg.ConfigVersion, CurrentConfigVersion)
		}
		for _, retired := range [][2]string{{"desktop", "status_bar_style_initialized"}, {"ui", "shortcut_layout"}, {"bot", "queue_drop"}, {"bot", "allowlist"}, {"permissions", "allow_dynamic_bash"}} {
			if meta.IsDefined(retired[0], retired[1]) {
				return meta, fmt.Errorf("unsupported retired setting %s.%s", retired[0], retired[1])
			}
		}
		for _, key := range []string{
			"auto_recovery", "max_steps", "planner_max_steps", "recovery_model", "recovery_temperature",
			"auto_plan", "auto_plan_classifier", "soft_compact_ratio", "tool_result_snip_ratio",
			"compact_force_ratio", "context_editing", "keep", "recent_keep",
			"plan_mode_read_only_commands", "legacy_anchor_safety_gate", "completion_validation",
			"completion_evaluator_model", "redact_tool_output", "memory_compiler",
		} {
			if meta.IsDefined("agent", key) {
				return meta, fmt.Errorf("unsupported retired setting agent.%s", key)
			}
		}
		for _, platform := range []string{"qq", "feishu", "weixin", "dingtalk"} {
			for _, key := range []string{"enabled", "app_id", "app_secret_env", "account_id", "token_env", "client_id", "client_secret", "client_id_env", "secret_env", "model", "tool_approval_mode", "workspace_root", "session_mappings"} {
				if meta.IsDefined("bot", platform, key) {
					return meta, fmt.Errorf("unsupported direct bot setting bot.%s.%s; use bot.connections", platform, key)
				}
			}
		}
		for _, path := range meta.Keys() {
			if len(path) == 2 && path[0] == "plugins" && path[1] == "tier" {
				return meta, fmt.Errorf("unsupported retired setting plugins.tier")
			}
			if len(path) != 2 || path[0] != "providers" {
				continue
			}
			for _, key := range []string{"chat_url", "responses_stateful", "model", "price", "vision", "vision_models", "reasoning_defaults"} {
				if path[len(path)-1] == key {
					return meta, fmt.Errorf("unsupported retired setting providers.%s", key)
				}
			}
		}
		if err := isolateDecodedProviders(text, meta, cfg); err != nil {
			return meta, err
		}
	}
	return meta, nil
}

// isolateDecodedProviders stops one vendor's defaults from being written onto
// another vendor's provider entry.
//
// TOML array-of-tables decoding is positional: BurntSushi/toml unifies
// [[providers]][i] onto whatever the destination slice already holds at index i
// instead of onto a zero value. Every config load seeds from Default(), which
// ships two official DeepSeek entries, so a user's first two [[providers]]
// inherit every DeepSeek field they did not set themselves — balance_url,
// the USD price table and context_window = 1000000 (#7357, #7358).
//
// Re-decoding the same bytes onto an empty slice makes a declared provider list
// carry only what the file declares. Defaults that are genuinely correct for an
// entry are then reapplied by the explicit backfill helpers, which key on the
// endpoint rather than on list position.
func isolateDecodedProviders(text string, meta toml.MetaData, cfg *Config) error {
	if cfg == nil || len(cfg.Providers) == 0 || !meta.IsDefined("providers") {
		return nil
	}
	var isolated struct {
		Providers []ProviderEntry `toml:"providers"`
	}
	if _, err := toml.Decode(text, &isolated); err != nil {
		return fmt.Errorf("decode providers: %w", err)
	}
	cfg.Providers = isolated.Providers
	return nil
}
