package config

import (
	"testing"
)

func TestNormalizeOfficialDeepSeekMultiModelPreservesProviderEfforts(t *testing.T) {
	for _, tc := range []struct {
		name, providerName, kind, baseURL string
	}{
		{name: "responses", providerName: "deepseek-responses", kind: "responses", baseURL: "https://api.deepseek.com"},
		{name: "anthropic", providerName: "deepseek", kind: "anthropic", baseURL: deepSeekAnthropicBaseURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Providers: []ProviderEntry{{
				Name: tc.providerName, Kind: tc.kind, BaseURL: tc.baseURL,
				Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash",
				SupportedEfforts: []string{"disabled", "high"}, DefaultEffort: "high",
			}}}

			normalizeOfficialDeepSeekModels(c)
			for _, model := range []string{"deepseek-v4-flash", "deepseek-v4-pro"} {
				entry, ok := c.ResolveModel(tc.providerName + "/" + model)
				if !ok {
					t.Fatalf("%s did not resolve", model)
				}
				if !stringSlicesEqual(entry.SupportedEfforts, []string{"disabled", "high"}) {
					t.Errorf("%s supported_efforts = %v, want provider-level custom vocabulary", model, entry.SupportedEfforts)
				}
				if _, err := NormalizeEffort(entry, "low"); err == nil {
					t.Errorf("%s unexpectedly accepted low outside provider-level vocabulary", model)
				}
			}
		})
	}
}

func TestNormalizeOfficialDeepSeekProviderEffortsKeepsExplicitModelOverride(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name: "deepseek-responses", Kind: "responses", BaseURL: "https://api.deepseek.com",
		Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash",
		SupportedEfforts: []string{"disabled", "high"}, DefaultEffort: "high",
		ModelOverrides: map[string]ProviderModelOverride{
			"deepseek-v4-pro": {SupportedEfforts: []string{"disabled", "low", "high"}, DefaultEffort: "low"},
		},
	}}}

	normalizeOfficialDeepSeekModels(c)
	pro, ok := c.ResolveModel("deepseek-responses/deepseek-v4-pro")
	if !ok {
		t.Fatal("Pro did not resolve")
	}
	if !stringSlicesEqual(pro.SupportedEfforts, []string{"disabled", "low", "high"}) || pro.DefaultEffort != "low" {
		t.Fatalf("Pro override = %v/%q, want explicit per-model values", pro.SupportedEfforts, pro.DefaultEffort)
	}
}

func TestNormalizeOfficialDeepSeekResponsesDoesNotRestoreUncheckedPro(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]ProviderModelOverride
	}{
		{
			name: "settings uncheck keeps flash override",
			overrides: map[string]ProviderModelOverride{
				"deepseek-v4-flash": {SupportedEfforts: []string{"disabled", "low", "high", "max"}, DefaultEffort: "high"},
			},
		},
		{
			name: "leftover pro override is still treated as curated",
			overrides: map[string]ProviderModelOverride{
				"deepseek-v4-pro": {SupportedEfforts: []string{"disabled", "low", "high", "max"}, DefaultEffort: "high"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Providers: []ProviderEntry{{
				Name: "deepseek-responses", Kind: "responses", BaseURL: "https://api.deepseek.com",
				Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
				ModelOverrides: tc.overrides,
			}}}

			normalizeOfficialDeepSeekModels(c)
			p, ok := c.Provider("deepseek-responses")
			if !ok {
				t.Fatal("deepseek-responses provider missing after normalization")
			}
			if p.HasModel("deepseek-v4-pro") {
				t.Fatalf("unchecked Pro was restored: %v", p.ModelList())
			}
		})
	}
}
