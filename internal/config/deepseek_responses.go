package config

import (
	"deepzora/internal/provider/openai"
)

func deepSeekV4EffortOverrides() map[string]ProviderModelOverride {
	flash := ProviderModelOverride{SupportedEfforts: []string{"disabled", "low", "high", "max"}, DefaultEffort: "high"}
	return map[string]ProviderModelOverride{
		"deepseek-flash":                   flash,
		"deepseek-v4-flash":                flash,
		openai.OfficialDeepSeekVisionModel: flash,
		"deepseek-v4-pro":                  {SupportedEfforts: []string{"disabled", "low", "high", "max"}, DefaultEffort: "high"},
	}
}
