package config

type ProviderModelOverride struct {
	ReasoningProtocol string   `toml:"reasoning_protocol"`
	SupportedEfforts  []string `toml:"supported_efforts"`
	DefaultEffort     string   `toml:"default_effort"`
	Vision            *bool    `toml:"vision"`
	// ContextWindow overrides the provider-wide context budget for this model.
	// Zero inherits ProviderEntry.ContextWindow so existing configurations keep
	// their current compaction behavior.
	ContextWindow int `toml:"context_window"`
	// MaxOutputTokens overrides the provider-wide output budget. Zero inherits;
	// positive values set a cap and negative values omit optional wire limits.
	MaxOutputTokens int `toml:"max_output_tokens"`
}
