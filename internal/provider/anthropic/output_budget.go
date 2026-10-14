package anthropic

import "deepzora/internal/provider"

func (c *client) ContextBudgetPolicy() provider.ContextBudgetPolicy {
	if lim, ok := provider.LookupOfficialOpenCodeGo("anthropic", c.baseURL, c.model); ok {
		return provider.ContextBudgetPolicy{
			WindowMode:       provider.ContextWindowShared,
			AutoOutputTokens: lim.MaxOutput,
			MaxOutputTokens:  lim.MaxOutput,
			LimitMode:        provider.OutputLimitRequired,
		}
	}
	if c.deepseek {
		return provider.ContextBudgetPolicy{
			WindowMode:       provider.ContextWindowShared,
			AutoOutputTokens: provider.DeepSeekMaxOutputTokens,
			MaxOutputTokens:  provider.DeepSeekMaxOutputTokens,
			LimitMode:        provider.OutputLimitRequired,
		}
	}
	return provider.ContextBudgetPolicy{WindowMode: provider.ContextWindowUnknown, LimitMode: provider.OutputLimitRequired}
}
