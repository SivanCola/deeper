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

func isOfficialDeepSeekModelReferenceEndpoint(p *ProviderEntry) bool {
	endpoint, exact := normalizedExactProviderRequestURL(ProviderEffectiveRequestURL(p))
	if !exact {
		return false
	}
	switch normalizedProviderProtocol(p.Kind) {
	case "openai":
		return endpoint == "https://api.deepseek.com/chat/completions" || endpoint == "https://api.deepseek.com/v1/chat/completions"
	case "responses":
		return endpoint == "https://api.deepseek.com/responses" || endpoint == "https://api.deepseek.com/v1/responses"
	case "anthropic":
		return endpoint == "https://api.deepseek.com/anthropic/v1/messages"
	default:
		return false
	}
}
