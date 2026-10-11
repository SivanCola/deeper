package config

import (
	"strings"

	"deepzora/internal/provider"
	"deepzora/internal/provider/openai"
)

func deepSeekV4FlashPriceCNY() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.04, Input: 2, Output: 8, Currency: "¥"}
}

func deepSeekV4ProPriceCNY() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.30, Input: 9, Output: 27, Currency: "¥"}
}

// deepSeekV4FlashModelIDs are the ids the vendor serves at the Flash price: the
// V4.1 name, and the retired V4 ids it still routes there.
func deepSeekV4FlashModelIDs() []string {
	return []string{"deepseek-flash", "deepseek-v4-flash", openai.OfficialDeepSeekVisionModel}
}

func deepSeekV4PricesCNY() map[string]*provider.Pricing {
	prices := map[string]*provider.Pricing{"deepseek-v4-pro": deepSeekV4ProPriceCNY()}
	for _, model := range deepSeekV4FlashModelIDs() {
		prices[model] = deepSeekV4FlashPriceCNY()
	}
	return prices
}

func deepSeekV4FlashPriceUSD() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.006, Input: 0.3, Output: 1.2, Currency: "$"}
}

func deepSeekV4ProPriceUSD() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.044, Input: 1.32, Output: 3.96, Currency: "$"}
}

func deepSeekV4PricesUSD() map[string]*provider.Pricing {
	prices := map[string]*provider.Pricing{"deepseek-v4-pro": deepSeekV4ProPriceUSD()}
	for _, model := range deepSeekV4FlashModelIDs() {
		prices[model] = deepSeekV4FlashPriceUSD()
	}
	return prices
}

// DeepSeekV4PricesForCurrency returns the official regional price table.
// Persisted custom prices still win; this is only used for built-in templates
// and known-default refreshes.
func DeepSeekV4PricesForCurrency(currency string) map[string]*provider.Pricing {
	if normalizeDeepSeekPricingCurrency(currency) == "CNY" {
		return deepSeekV4PricesCNY()
	}
	return deepSeekV4PricesUSD()
}

func deepSeekV4PriceForModel(currency, model string) *provider.Pricing {
	return clonePricing(DeepSeekV4PricesForCurrency(currency)[strings.TrimSpace(model)])
}

func normalizeDeepSeekPricingCurrency(currency string) string {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "CNY", "RMB", "CNH", "¥", "￥":
		return "CNY"
	case "USD", "$", "US$":
		return "USD"
	default:
		return ""
	}
}

// ApplyDeepSeekOfficialDefaultPricing refreshes built-in/official DeepSeek
// prices that still match known official defaults for each provider's frozen
// billing_currency. Custom user prices and display-currency switches never
// rewrite list prices.
func (c *Config) ApplyDeepSeekOfficialDefaultPricing() {
	applyDeepSeekOfficialDefaultPricing(c)
}

func applyDeepSeekOfficialDefaultPricing(c *Config) {
	applyDeepSeekOfficialDefaultPricingWithOverride(c, false)
}

func applyDeepSeekOfficialDefaultPricingWithOverride(c *Config, overridePersisted bool) {
	if c == nil {
		return
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if officialProviderKind(p) != "deepseek" || !isOfficialDeepSeekBillingEndpoint(p) {
			continue
		}
		currency := p.ProviderBillingCurrency()
		if currency == "" {
			currency = "USD"
		}
		// Only refresh when the row still matches a known official default in
		// the provider's own billing currency. Display currency must not win.
		if isKnownDeepSeekOfficialPricing(p.Model, p.Price) && (overridePersisted || p.persistedOfficialCurrency == "" || p.persistedOfficialCurrency == currency) {
			if samePricing(p.Price, deepSeekV4PriceForModel(currency, p.Model)) || overridePersisted {
				p.Price = deepSeekV4PriceForModel(currency, p.Model)
			}
		}
		for model, price := range p.Prices {
			if isKnownDeepSeekOfficialPricing(model, price) && (overridePersisted || p.persistedOfficialCurrency == "" || p.persistedOfficialCurrency == currency) {
				if samePricing(price, deepSeekV4PriceForModel(currency, model)) || overridePersisted {
					p.Prices[model] = deepSeekV4PriceForModel(currency, model)
				}
			}
		}
		if strings.TrimSpace(p.BillingCurrency) == "" {
			p.BillingCurrency = currency
		}
	}
}

// markPersistedDeepSeekOfficialPricing records which recognized regional
// prices came from TOML. Auto locale refreshes preserve those values, while an
// explicit currency choice can still replace them with the selected table.
func markPersistedDeepSeekOfficialPricing(c *Config) {
	if c == nil {
		return
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		if officialProviderKind(p) != "deepseek" || !isOfficialDeepSeekBillingEndpoint(p) {
			continue
		}
		p.persistedOfficialCurrency = completeDeepSeekOfficialPricingCurrency(p)
		if c.ConfigVersion >= Default().ConfigVersion && isStandardDeepSeekProviderTemplate(p) {
			p.persistedOfficialCurrency = ""
		}
	}
}

func isStandardDeepSeekProviderTemplate(p *ProviderEntry) bool {
	if p == nil || officialProviderKind(p) != "deepseek" {
		return false
	}
	return strings.TrimSpace(p.APIKeyEnv) == "DEEPSEEK_API_KEY" &&
		strings.TrimSpace(p.BalanceURL) == "https://api.deepseek.com/user/balance" &&
		p.ContextWindow == 1_000_000
}

func completeDeepSeekOfficialPricingCurrency(p *ProviderEntry) string {
	if p == nil {
		return ""
	}
	models := p.ModelList()
	if len(models) == 1 && isKnownDeepSeekOfficialPricing(models[0], p.Price) {
		return normalizeDeepSeekPricingCurrency(p.Price.Currency)
	}
	if len(models) == 0 || p.Price != nil {
		return ""
	}
	currency := ""
	for _, model := range models {
		price := p.Prices[strings.TrimSpace(model)]
		if !isKnownDeepSeekOfficialPricing(model, price) {
			return ""
		}
		nextCurrency := normalizeDeepSeekPricingCurrency(price.Currency)
		if nextCurrency == "" {
			return ""
		}
		if currency == "" {
			currency = nextCurrency
		} else if currency != nextCurrency {
			return ""
		}
	}
	return currency
}

func mimoV25ProPrice() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.025, Input: 3, Output: 6, Currency: "¥"}
}

func mimoV25Price() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"}
}

func mimoV2FlashPrice() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.07, Input: 0.70, Output: 2.10, Currency: "¥"}
}

func mimoDomesticPrices(models []string) map[string]*provider.Pricing {
	prices := map[string]*provider.Pricing{}
	for _, model := range models {
		switch strings.TrimSpace(model) {
		case "mimo-v2.6-pro", "mimo-v2.5-pro", "mimo-v2-pro":
			prices[model] = mimoV25ProPrice()
		case "mimo-v2.6-flash", "mimo-v2.5", "mimo-v2-omni":
			prices[model] = mimoV25Price()
		case "mimo-v2-flash":
			prices[model] = mimoV2FlashPrice()
		}
	}
	return prices
}

func longCat20Price() *provider.Pricing {
	return &provider.Pricing{CacheHit: 0.04, Input: 2, Output: 8, Currency: "¥"}
}

func longCat20Prices(models []string) map[string]*provider.Pricing {
	prices := map[string]*provider.Pricing{}
	for _, model := range models {
		switch strings.TrimSpace(model) {
		case "LongCat-2.0":
			prices[model] = longCat20Price()
		}
	}
	return prices
}

func isKnownDeepSeekOfficialPricing(model string, price *provider.Pricing) bool {
	model = strings.TrimSpace(model)
	if model == "" || price == nil {
		return false
	}
	for _, prices := range []map[string]*provider.Pricing{
		deepSeekV4PricesCNY(), deepSeekV4PricesUSD(),
	} {
		if samePricingNormalizedCurrency(price, prices[model]) {
			return true
		}
	}
	return false
}

// IsOfficialDeepSeekProvider reports whether an entry targets DeepSeek's
// official API endpoint. Desktop telemetry uses this after a regional-currency
// change so custom endpoints and rates stay untouched.
func IsOfficialDeepSeekProvider(p *ProviderEntry) bool {
	return officialProviderKind(p) == "deepseek"
}

// IsKnownDeepSeekOfficialPricing reports whether price is one of Deepzora's
// built-in DeepSeek regional defaults for model.
func IsKnownDeepSeekOfficialPricing(model string, price *provider.Pricing) bool {
	return isKnownDeepSeekOfficialPricing(model, price)
}

func samePricing(a, b *provider.Pricing) bool {
	if a == nil || b == nil {
		return false
	}
	return a.CacheHit == b.CacheHit && a.Input == b.Input && a.Output == b.Output && a.Currency == b.Currency
}

func samePricingNormalizedCurrency(a, b *provider.Pricing) bool {
	if a == nil || b == nil {
		return false
	}
	return a.CacheHit == b.CacheHit && a.Input == b.Input && a.Output == b.Output &&
		normalizeDeepSeekPricingCurrency(a.Currency) == normalizeDeepSeekPricingCurrency(b.Currency)
}
