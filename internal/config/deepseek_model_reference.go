package config

func acceptsDeepSeekModelReference(p *ProviderEntry, model string) bool { return p.HasModel(model) }

func normalizeOfficialDeepSeekModels(c *Config) {
	if c == nil {
		return
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if officialProviderHost(p.BaseURL) != "api.deepseek.com" {
			continue
		}
		backfillOfficialDeepSeekVisionPrice(p)

		backfillDeepSeekAnthropicCapabilities(p)
	}
}
