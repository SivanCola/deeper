package main

import (
	"deepzora/internal/config"
	"deepzora/internal/provider/openai"
	"testing"
)

func TestOfficialDeepSeekPerModelImageCapabilityRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	models := []string{"deepseek-flash", "deepseek-v4-pro", openai.OfficialDeepSeekVisionModel}
	overrides := []ProviderModelOverrideView{}
	for _, model := range models {
		overrides = append(overrides, ProviderModelOverrideView{Model: model, Vision: new(true)})
	}
	app := NewApp()
	if err := app.SaveProvider(ProviderView{Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Models: models, Default: "deepseek-flash", ModelOverrides: overrides}); err != nil {
		t.Fatal(err)
	}
	entry, ok := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !ok {
		t.Fatal("saved provider missing")
	}
	resolver := config.NewTransientModelCapabilityResolver()
	for _, model := range models {
		capability := resolveTestModelCapability(resolver, *entry, model)
		want := config.CapabilityUnsupported
		if model != "deepseek-v4-pro" {
			want = config.CapabilitySupported
		}
		if capability.State != want {
			t.Fatalf("model %s: capability=%+v want=%s", model, capability, want)
		}
	}
	view := providerViewFromEntry(*entry, false, true)
	if len(view.ModelOverrides) != len(models) {
		t.Fatalf("per-model overrides were lost: %+v", view.ModelOverrides)
	}
	if len(view.VisionModels) != 2 || view.VisionModels[0] != "deepseek-flash" || view.VisionModels[1] != openai.OfficialDeepSeekVisionModel {
		t.Fatalf("image-capable projection=%v", view.VisionModels)
	}
}
