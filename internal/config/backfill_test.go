package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/BurntSushi/toml"

	"deepzora/internal/provider"
)

func hasModel(c *Config, model string) *ProviderEntry {
	for i := range c.Providers {
		if slices.Contains(c.Providers[i].ModelList(), model) {
			return &c.Providers[i]
		}
	}
	return nil
}

func TestLoadAndSavePreserveStepFunRegionalBaseURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	stepfun, ok := CuratedProviderPreset("stepfun")
	if !ok || len(stepfun.Entries) != 1 {
		t.Fatal("missing stepfun preset")
	}
	stepfunEntry := stepfun.Entries[0]
	stepfunEntry.BaseURL = "https://api.stepfun.ai/step_plan/v1"
	stepfunAnthropic, ok := CuratedProviderPreset("stepfun-anthropic")
	if !ok || len(stepfunAnthropic.Entries) != 1 {
		t.Fatal("missing stepfun-anthropic preset")
	}
	stepfunAnthropicEntry := stepfunAnthropic.Entries[0]
	stepfunAnthropicEntry.BaseURL = "https://api.stepfun.ai/step_plan"
	cfg.Providers = append(cfg.Providers, stepfunEntry, stepfunAnthropicEntry)
	cfg.Desktop.ProviderAccess = []string{"stepfun", "stepfun-anthropic"}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	loaded := LoadForEdit(path)
	if got, _ := loaded.Provider("stepfun"); got == nil || got.BaseURL != "https://api.stepfun.ai/step_plan/v1" {
		t.Fatalf("loaded stepfun = %+v, want global base URL preserved", got)
	}
	if got, _ := loaded.Provider("stepfun-anthropic"); got == nil || got.BaseURL != "https://api.stepfun.ai/step_plan" {
		t.Fatalf("loaded stepfun-anthropic = %+v, want global base URL preserved", got)
	}

	var disk Config
	if _, err := toml.DecodeFile(path, &disk); err != nil {
		t.Fatalf("decode config after read-only load: %v", err)
	}
	if got, _ := disk.Provider("stepfun"); got == nil || got.BaseURL != "https://api.stepfun.ai/step_plan/v1" {
		t.Fatalf("read-only LoadForEdit rewrote stepfun = %+v, want legacy base URL preserved on disk", got)
	}
	if err := loaded.SaveTo(path); err != nil {
		t.Fatalf("explicit SaveTo: %v", err)
	}
	disk = Config{}
	if _, err := toml.DecodeFile(path, &disk); err != nil {
		t.Fatalf("decode explicitly saved config: %v", err)
	}
	if got, _ := disk.Provider("stepfun"); got == nil || got.BaseURL != "https://api.stepfun.ai/step_plan/v1" {
		t.Fatalf("persisted stepfun = %+v, want global base URL preserved", got)
	}
	if got, _ := disk.Provider("stepfun-anthropic"); got == nil || got.BaseURL != "https://api.stepfun.ai/step_plan" {
		t.Fatalf("persisted stepfun-anthropic = %+v, want global base URL preserved", got)
	}
}

func TestNormalizeOfficialDeepSeekModelsLeavesExternalEndpointUntouched(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://proxy.example.com/v1",
		Model:   "glm-5",
	}}}
	normalizeOfficialDeepSeekModels(c)

	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	if p.HasModel("deepseek-v4-flash") || p.HasModel("deepseek-v4-pro") {
		t.Fatalf("external endpoint models = %+v, want untouched custom list", p.ModelList())
	}
}

func TestBackfillDeepSeekOfficialPrices(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		Default: "deepseek-v4-flash",
	}}}
	backfillDeepSeekOfficialPrices(c)
	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	if p.Prices["deepseek-v4-flash"].Output != 1.2 || p.Prices["deepseek-v4-flash"].Currency != "$" || p.Prices["deepseek-v4-pro"].Output != 3.96 || p.Prices["deepseek-v4-pro"].Currency != "$" {
		t.Fatalf("deepseek prices = %+v, want default USD flash/pro prices", p.Prices)
	}
}

func TestBackfillDeepSeekOfficialPricesUsesConfiguredLanguage(t *testing.T) {
	// Language no longer selects list-price tables; backfill uses billing_currency/USD.
	c := &Config{Language: "zh", Providers: []ProviderEntry{{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		Default: "deepseek-v4-flash",
	}}}
	backfillDeepSeekOfficialPrices(c)
	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	if p.Prices["deepseek-v4-flash"].Output != 1.2 || p.Prices["deepseek-v4-flash"].Currency != "$" || p.Prices["deepseek-v4-pro"].Output != 3.96 || p.Prices["deepseek-v4-pro"].Currency != "$" {
		t.Fatalf("deepseek prices = %+v, want USD official flash/pro prices", p.Prices)
	}
}

func TestBackfillDeepSeekOfficialPricesKeepsProviderWidePrice(t *testing.T) {
	custom := &provider.Pricing{CacheHit: 9, Input: 9, Output: 9, Currency: "$"}
	c := &Config{Providers: []ProviderEntry{{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		Default: "deepseek-v4-flash",
		Price:   custom,
		Prices: map[string]*provider.Pricing{
			"deepseek-v4-flash": {CacheHit: 1, Input: 1, Output: 1, Currency: "$"},
		},
	}}}
	backfillDeepSeekOfficialPrices(c)
	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	if len(p.Prices) != 1 {
		t.Fatalf("deepseek prices = %+v, want existing per-model prices only", p.Prices)
	}
	pro, ok := c.ResolveModel("deepseek/deepseek-v4-pro")
	if !ok {
		t.Fatal("deepseek pro did not resolve")
	}
	if pro.Price == nil || pro.Price.Output != 9 {
		t.Fatalf("pro price = %+v, want provider-wide custom price", pro.Price)
	}
	flash, ok := c.ResolveModel("deepseek")
	if !ok {
		t.Fatal("deepseek default did not resolve")
	}
	if flash.Price == nil || flash.Price.Output != 1 {
		t.Fatalf("flash price = %+v, want existing per-model custom price", flash.Price)
	}
}

func TestApplyDeepSeekOfficialDefaultPricingUsesConfiguredLanguage(t *testing.T) {
	// Language must not rewrite frozen billing_currency list prices.
	c := Default()
	c.Language = "zh"
	applyDeepSeekOfficialDefaultPricing(c)
	flash, ok := c.Provider("deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash provider missing")
	}
	if flash.Price == nil || flash.Price.Output != 1.2 || flash.Price.Currency != "$" {
		t.Fatalf("flash price = %+v, want frozen USD default table", flash.Price)
	}
	pro, ok := c.Provider("deepseek-pro")
	if !ok {
		t.Fatal("deepseek-pro provider missing")
	}
	if pro.Price == nil || pro.Price.Output != 3.96 || pro.Price.Currency != "$" {
		t.Fatalf("pro price = %+v, want frozen USD default table", pro.Price)
	}
}

func TestApplyDeepSeekOfficialDefaultPricingKeepsCustomPrice(t *testing.T) {
	c := &Config{Language: "zh", Providers: []ProviderEntry{{
		Name:    "deepseek-flash",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Model:   "deepseek-v4-flash",
		Price:   &provider.Pricing{CacheHit: 9, Input: 9, Output: 9, Currency: "$"},
	}}}
	applyDeepSeekOfficialDefaultPricing(c)
	p, ok := c.Provider("deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash provider missing")
	}
	if p.Price == nil || p.Price.Output != 9 || p.Price.Currency != "$" {
		t.Fatalf("custom price = %+v, want unchanged", p.Price)
	}
}

func TestLoadForEditAutoCurrencyKeepsPersistedOfficialUSDPrice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `language = "zh"

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
prices = { "deepseek-v4-flash" = { cache_hit = 0.0028, input = 0.14, output = 0.28, currency = "$" } }
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := LoadForEdit(path)
	flash, ok := c.Provider("deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash provider missing")
	}
	if flash.PriceForModel(flash.DefaultModel()) == nil || flash.PriceForModel(flash.DefaultModel()).Output != 0.28 || flash.PriceForModel(flash.DefaultModel()).Currency != "$" {
		t.Fatalf("flash price = %+v, want persisted USD preset", flash.PriceForModel(flash.DefaultModel()))
	}

	if err := c.SetDisplayCurrency("CNY"); err != nil {
		t.Fatalf("SetDisplayCurrency CNY: %v", err)
	}
	// Display currency switch must freeze list prices (USD official table stays).
	if flash.PriceForModel(flash.DefaultModel()) == nil || flash.PriceForModel(flash.DefaultModel()).Output != 0.28 || flash.PriceForModel(flash.DefaultModel()).Currency != "$" {
		t.Fatalf("flash price = %+v, want frozen USD official table", flash.PriceForModel(flash.DefaultModel()))
	}
	if got := c.DisplayCurrencyPref(); got != "CNY" {
		t.Fatalf("display pref = %q, want CNY", got)
	}
}

func TestLoadForRootAutoCurrencyKeepsPersistedOfficialUSDPrice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	body := `language = "zh"

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
prices = { "deepseek-v4-flash" = { cache_hit = 0.0028, input = 0.14, output = 0.28, currency = "$" } }
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	flash, ok := c.Provider("deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash provider missing")
	}
	if flash.PriceForModel(flash.DefaultModel()) == nil || flash.PriceForModel(flash.DefaultModel()).Output != 0.28 || flash.PriceForModel(flash.DefaultModel()).Currency != "$" {
		t.Fatalf("flash price = %+v, want persisted USD preset", flash.PriceForModel(flash.DefaultModel()))
	}
}

func TestRuntimeAutoCurrencyKeepsPersistedOfficialUSDPrice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
prices = { "deepseek-v4-flash" = { cache_hit = 0.0028, input = 0.14, output = 0.28, currency = "$" } }
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := LoadForEdit(path)
	flash, ok := c.Provider("deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash provider missing")
	}
	if flash.PriceForModel(flash.DefaultModel()) == nil || flash.PriceForModel(flash.DefaultModel()).Output != 0.28 || flash.PriceForModel(flash.DefaultModel()).Currency != "$" {
		t.Fatalf("flash price = %+v, want persisted USD preset", flash.PriceForModel(flash.DefaultModel()))
	}
}

func TestLoadForRootAutoCurrencyDoesNotMixPartialOfficialPrices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	body := `language = "zh"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"

[providers.prices]
deepseek-v4-flash = { cache_hit = 0.0028, input = 0.14, output = 0.28, currency = "$" }
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	// Partial official USD prices freeze billing_currency=USD; missing pro row
	// backfills USD rather than mixing with language-selected CNY.
	for _, model := range p.Models {
		price := p.Prices[model]
		if price == nil || price.Currency != "$" {
			t.Fatalf("price for %q = %+v, want consistent USD table", model, price)
		}
	}
}

func TestDeepSeekOfficialPricingCurrencyResolution(t *testing.T) {
	// Official list-price currency follows provider billing_currency, not display.
	for _, tt := range []struct {
		name      string
		providers []ProviderEntry
		want      string
	}{
		{name: "no providers defaults to USD", want: "USD"},
		{name: "explicit provider billing CNY", providers: []ProviderEntry{{Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com", BillingCurrency: "CNY"}}, want: "CNY"},
		{name: "explicit provider billing USD", providers: []ProviderEntry{{Name: "deepseek-flash", Kind: "openai", BaseURL: "https://api.deepseek.com", BillingCurrency: "USD"}}, want: "USD"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Providers: tt.providers}
			if got := c.DeepSeekOfficialPricingCurrency(); got != tt.want {
				t.Fatalf("pricing currency = %q, want %q", got, tt.want)
			}
		})
	}
	// Display currency is independent.
	c := &Config{Language: "zh", Billing: BillingConfig{DisplayCurrency: "USD"}}
	if got := c.ExplicitDisplayCurrency(); got != "USD" {
		t.Fatalf("display = %q, want USD", got)
	}
	c2 := &Config{Language: "zh"}
	if got := c2.ExplicitDisplayCurrency(); got != "" {
		t.Fatalf("display auto zh = %q, want unresolved auto", got)
	}
}

func TestLoadForRootKeepsPricingRegionUserGlobal(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("DEEPZORA_HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[desktop]\nlanguage = \"en\"\n[billing]\ndisplay_currency = \"USD\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "deepzora.toml"), []byte("[desktop]\nlanguage = \"zh\"\n[billing]\ndisplay_currency = \"CNY\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if got := cfg.DisplayCurrencyPref(); got != "USD" {
		t.Fatalf("project config overrode user pricing currency: got %q, want USD", got)
	}
	if got := cfg.DesktopLanguage(); got != "en" {
		t.Fatalf("project config overrode user desktop language: got %q, want en", got)
	}
}

func TestApplyDeepSeekOfficialDefaultPricingExplicitCurrencyWins(t *testing.T) {
	c := Default()
	c.Desktop.Language = "zh"
	c.Billing.DisplayCurrency = "USD"
	// Display currency no longer selects list-price tables; billing_currency does.
	flash, _ := c.Provider("deepseek-flash")
	flash.BillingCurrency = "USD"
	applyDeepSeekOfficialDefaultPricing(c)
	if flash.Price == nil || flash.Price.Output != 1.2 || flash.Price.Currency != "$" {
		t.Fatalf("flash price = %+v, want USD billing_currency table", flash.Price)
	}

	c.Desktop.Language = "en"
	c.Billing.DisplayCurrency = "CNY"
	flash.BillingCurrency = "CNY"
	// Only refresh when price still matches an official default in the *new*
	// billing currency — force official CNY rates for this assertion.
	flash.Price = deepSeekV4FlashPriceCNY()
	applyDeepSeekOfficialDefaultPricing(c)
	if flash.Price == nil || flash.Price.Output != 8 || flash.Price.Currency != "¥" {
		t.Fatalf("flash price = %+v, want CNY billing_currency table", flash.Price)
	}
}

func TestResolveModelUsesPerModelPricing(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
		Default: "deepseek-v4-flash",
		Price:   &provider.Pricing{CacheHit: 9, Input: 9, Output: 9, Currency: "$"},
		Prices: map[string]*provider.Pricing{
			"deepseek-v4-flash": &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2, Currency: "¥"},
			"deepseek-v4-pro":   &provider.Pricing{CacheHit: 0.025, Input: 3, Output: 6, Currency: "¥"},
		},
	}}}
	pro, ok := c.ResolveModel("deepseek/deepseek-v4-pro")
	if !ok {
		t.Fatal("deepseek pro did not resolve")
	}
	if pro.Price == nil || pro.Price.Output != 6 {
		t.Fatalf("pro price = %+v, want model-specific pro price", pro.Price)
	}
	flash, ok := c.ResolveModel("deepseek")
	if !ok {
		t.Fatal("deepseek default did not resolve")
	}
	if flash.Price == nil || flash.Price.Output != 2 {
		t.Fatalf("flash price = %+v, want model-specific flash price", flash.Price)
	}
}

// ── Explicit model list: normalization must not override user selection ───────

func TestEnsureProviderModelsSkipsWhenExplicitModelList(t *testing.T) {
	// User saved with only flash via Settings → Models = ["deepseek-v4-flash"].
	p := &ProviderEntry{
		Name:    "deepseek",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash"},
		Default: "deepseek-v4-flash",
	}
	ensureProviderModels(p, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, "deepseek-v4-flash")
	if p.HasModel("deepseek-v4-pro") {
		t.Fatal("ensureProviderModels must not merge required models when Models is explicitly set")
	}
	if len(p.Models) != 1 || p.Models[0] != "deepseek-v4-flash" {
		t.Fatalf("models = %v, want [deepseek-v4-flash]", p.Models)
	}
}

func TestMergeCuratedModelsIntoProviderSkipsWhenExplicitModelList(t *testing.T) {
	// User saved with only two mimo models via Settings.
	p := &ProviderEntry{
		Name:    "mimo-api",
		BaseURL: "https://api.xiaomimimo.com/v1",
		Models:  []string{"mimo-v2.5-pro", "mimo-v2.5"},
		Default: "mimo-v2.5-pro",
	}
	mergeCuratedModelsIntoProvider(p, []string{"mimo-v2.5-pro", "mimo-v2.5", "mimo-v2-omni"}, "mimo-v2.5-pro")
	if p.HasModel("mimo-v2-omni") {
		t.Fatal("mergeCuratedModelsIntoProvider must not add mimo-v2-omni when Models is explicitly set")
	}
	if len(p.Models) != 2 {
		t.Fatalf("models = %v, want 2 selected models", p.Models)
	}
}

func TestNormalizeOfficialDeepSeekModelsSkipsExplicitModelList(t *testing.T) {
	// User saved with only flash via Settings → Models = ["deepseek-v4-flash"].
	c := &Config{Providers: []ProviderEntry{{
		Name:      "deepseek",
		Kind:      "openai",
		BaseURL:   "https://api.deepseek.com",
		Models:    []string{"deepseek-v4-flash"},
		Default:   "deepseek-v4-flash",
		APIKeyEnv: "DEEPSEEK_API_KEY",
	}}}
	normalizeOfficialDeepSeekModels(c)
	p, ok := c.Provider("deepseek")
	if !ok {
		t.Fatal("deepseek provider missing")
	}
	if p.HasModel("deepseek-v4-pro") {
		t.Fatal("normalizeOfficialDeepSeekModels must not add pro when Models is explicitly set")
	}
}
