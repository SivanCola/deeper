package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"deepzora/internal/provider"
)

func TestDeepSeekCurrentCatalogSurvivesSaveAndReload(t *testing.T) {
	t.Setenv("DEEPZORA_HOME", t.TempDir())
	for _, id := range []string{"deepseek-chat", "deepseek-anthropic", "deepseek-responses"} {
		t.Run(id, func(t *testing.T) {
			preset, _ := CuratedProviderPreset(id)
			cfg := &Config{Providers: preset.Entries, DefaultModel: id + "/deepseek-flash"}
			path := filepath.Join(t.TempDir(), "config.toml")
			for range 2 {
				if err := cfg.SaveTo(path); err != nil {
					t.Fatal(err)
				}
				cfg = LoadForEditWithoutCredentials(path)
				p, ok := cfg.Provider(id)
				if !ok || !reflect.DeepEqual(p.ModelList(), []string{"deepseek-flash", "deepseek-v4-pro"}) || p.DefaultModel() != "deepseek-flash" {
					t.Fatalf("saved preset catalog changed: %+v", p)
				}
				for _, model := range []string{"deepseek-flash"} {
					resolved, ok := cfg.ResolveModel(id + "/" + model)
					if !ok || resolved.Model != model || !EffectiveVision(resolved) {
						t.Fatalf("model reference %q lost wire identity or vision: %+v", model, resolved)
					}
				}
			}
		})
	}
}

func TestDeepSeekCuratedCatalogSurvivesRuntimeAndEditLoad(t *testing.T) {
	for _, models := range []string{`["deepseek-flash"]`, `["deepseek-v4-flash"]`, `["deepseek-v4-flash", "deepseek-v4-pro"]`, `["deepseek-flash", "Custom-Case-ID"]`} {
		t.Run(models, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("DEEPZORA_HOME", home)
			path := filepath.Join(home, "config.toml")
			raw := "config_version = 12\n[[providers]]\nname = \"deepseek-responses\"\nkind = \"responses\"\nbase_url = \"https://api.deepseek.com\"\nmodels = " + models + "\nmodel_overrides = { deepseek-flash = { vision = false }, deepseek-v4-flash = { vision = false }, deepseek-v4-pro = { vision = false }, Custom-Case-ID = { vision = false } }\nfuture_field = \"preserve\"\n"
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			var original Config
			if _, err := decodeTOMLFile(path, &original); err != nil {
				t.Fatal(err)
			}
			runtime, err := LoadForRootReadOnly(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, cfg := range []*Config{runtime, LoadForEditWithoutCredentials(path)} {
				p, _ := cfg.Provider("deepseek-responses")
				if !reflect.DeepEqual(p.ModelList(), original.Providers[0].ModelList()) {
					t.Fatalf("loader overwrote a curated catalog: %+v", p)
				}
				for _, model := range p.ModelList() {
					selected, ok := cfg.ResolveModel(p.Name + "/" + model)
					if !ok || EffectiveVision(selected) {
						t.Fatalf("explicit image opt-out changed for %s", model)
					}
				}
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != raw {
				t.Fatal("loading changed the user's file or unknown fields")
			}
		})
	}
}

func TestDeepSeekLegacyReferenceCompatibilityIsScoped(t *testing.T) {
	for _, tc := range []struct {
		name, kind, base, request string
		models                    []string
		want                      bool
	}{
		{"chat", "openai", "https://api.deepseek.com/v1", "", []string{"deepseek-flash"}, false},
		{"exact override", "openai", "https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions", []string{"deepseek-flash"}, false},
		{"responses", "responses", "https://api.deepseek.com", "", []string{"deepseek-flash"}, false},
		{"anthropic", "anthropic", "https://api.deepseek.com/anthropic", "", []string{"deepseek-flash"}, false},
		{"third party", "openai", "https://gateway.example/v1", "", []string{"deepseek-flash"}, false},
		{"custom route", "openai", "https://api.deepseek.com/custom", "", []string{"deepseek-flash"}, false},
		{"custom override", "openai", "https://api.deepseek.com", "https://gateway.example/chat/completions", []string{"deepseek-flash"}, false},
		{"Flash removed", "openai", "https://api.deepseek.com", "", []string{"deepseek-v4-pro"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const old = "deepseek-v4-flash"
			price := &provider.Pricing{Input: 5, Output: 10, Currency: "$"}
			cfg := &Config{Providers: []ProviderEntry{{Name: "test", Kind: tc.kind, BaseURL: tc.base, RequestURL: tc.request, Models: tc.models,
				Prices: map[string]*provider.Pricing{old: price}, ModelOverrides: map[string]ProviderModelOverride{old: {SupportedEfforts: []string{"high"}, DefaultEffort: "high"}},
			}}}
			for _, ref := range []string{"test/" + old, old} {
				got, ok := cfg.ResolveModel(ref)
				if ok != tc.want {
					t.Fatalf("ResolveModel(%q) = %v, want %v", ref, ok, tc.want)
				}
				if ok && (got.Model != old || !reflect.DeepEqual(got.Price, price) || !reflect.DeepEqual(got.SupportedEfforts, []string{"high"})) {
					t.Fatalf("legacy reference lost its model-specific configuration: %+v", got)
				}
			}
			for _, unknown := range []string{"DeepSeek-V4-Flash", "deepseek-v4.1-flash-expires-on-0910", "deepseek-future"} {
				if _, ok := cfg.ResolveModel("test/" + unknown); ok {
					t.Fatalf("invented compatibility for %q", unknown)
				}
			}
		})
	}
}
