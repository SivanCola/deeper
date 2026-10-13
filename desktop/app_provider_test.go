package main

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/config"
)

func providerPresetViewByID(t *testing.T, view SettingsView, id string) ProviderPresetView {
	t.Helper()
	for _, preset := range view.ProviderPresets {
		if preset.ID == id {
			return preset
		}
	}
	t.Fatalf("Settings().ProviderPresets missing %q: %+v", id, view.ProviderPresets)
	return ProviderPresetView{}
}

func TestAddProviderPresetAccessSavesEditableProviderAndKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")
	os.Unsetenv("MIMO_API_KEY")

	if warning, err := NewApp().AddProviderPresetAccess("mimo-api", "sk-mimo"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none", warning)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	p, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("mimo-api provider not saved")
	}
	if p.Kind != "openai" || p.BaseURL != "https://api.xiaomimimo.com/v1" || p.Default != "mimo-v2.6-pro" {
		t.Fatalf("mimo-api provider after preset add = %+v", p)
	}
	if p.PresetID != "mimo-api" || p.PresetVersion != config.ProviderPresetVersion {
		t.Fatalf("mimo-api preset metadata = %q/%d, want mimo-api/%d", p.PresetID, p.PresetVersion, config.ProviderPresetVersion)
	}
	if !p.NoProxy {
		t.Fatal("mimo-api preset should save no_proxy = true")
	}
	resolver := config.NewTransientModelCapabilityResolver()
	if resolveTestModelCapability(resolver, *p, "mimo-v2.5").State != config.CapabilitySupported || resolveTestModelCapability(resolver, *p, "mimo-v2.5-pro").State != config.CapabilityUnsupported {
		t.Fatalf("mimo vision_models = %+v, want only vision-capable MiMo models", p.VisionModels)
	}
	if price := p.PriceForModel("mimo-v2.5-pro"); price == nil || price.Currency != "¥" {
		t.Fatalf("mimo-v2.5-pro price = %+v, want RMB pricing", price)
	}
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["mimo-api"] {
		t.Fatalf("provider_access missing mimo-api: %+v", cfg.Desktop.ProviderAccess)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	if p.APIKeyEnv == "MIMO_API_KEY" || !strings.Contains(string(data), p.APIKeyEnv+"=sk-mimo") {
		t.Fatal("saved credentials missing the isolated MiMo key reference")
	}

	view := NewApp().Settings()
	var presetView *ProviderPresetView
	var providerView *ProviderView
	for i := range view.ProviderPresets {
		if view.ProviderPresets[i].ID == "mimo-api" {
			presetView = &view.ProviderPresets[i]
		}
	}
	for i := range view.Providers {
		if view.Providers[i].Name == "mimo-api" {
			providerView = &view.Providers[i]
		}
	}
	if presetView == nil || !presetView.Added || presetView.Status != providerPresetStatusInstalled || !presetView.KeySet {
		t.Fatalf("mimo-api preset view = %+v, want installed/key-set", presetView)
	}
	if providerView == nil || providerView.BuiltIn || !providerView.Added || !providerView.KeySet {
		t.Fatalf("mimo provider view = %+v, want editable added custom provider with key", providerView)
	}
}

func TestAddProviderPresetAccessDoesNotOverwriteExistingProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")
	os.Unsetenv("MIMO_API_KEY")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-original")

	cfg := config.Default()
	custom := config.ProviderEntry{
		Name:      "mimo-api",
		Kind:      "openai",
		BaseURL:   "https://custom.example/v1",
		Models:    []string{"custom-model"},
		Default:   "custom-model",
		APIKeyEnv: "MIMO_API_KEY",
		Headers:   map[string]string{"X-Custom": "keep-me"},
	}
	if err := cfg.UpsertProvider(custom); err != nil {
		t.Fatalf("upsert custom provider: %v", err)
	}
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if warning, err := NewApp().AddProviderPresetAccess("mimo-api", "sk-new"); err == nil {
		t.Fatal("AddProviderPresetAccess unexpectedly overwrote an existing provider")
	} else if !strings.Contains(err.Error(), "provider name(s) already exist") {
		t.Fatalf("AddProviderPresetAccess error = %v, want name-exists guard", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none on rejected add", warning)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("mimo-api provider missing after rejected add")
	}
	if got.BaseURL != custom.BaseURL || got.DefaultModel() != custom.DefaultModel() || !reflect.DeepEqual(got.ModelList(), custom.ModelList()) || !reflect.DeepEqual(got.Headers, custom.Headers) {
		t.Fatalf("mimo-api provider was overwritten: %+v, want custom %+v", got, custom)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	if strings.Contains(string(data), "sk-new") || !strings.Contains(string(data), "MIMO_API_KEY=sk-original") {
		t.Fatalf("credentials changed after rejected add:\n%s", data)
	}
}

func TestResetProviderPresetAccessOverwritesSameNameProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")
	os.Unsetenv("MIMO_API_KEY")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-original")

	cfg := config.Default()
	custom := config.ProviderEntry{
		Name:      "mimo-api",
		Kind:      "openai",
		BaseURL:   "https://custom.example/v1",
		Models:    []string{"custom-model"},
		Default:   "custom-model",
		APIKeyEnv: "MIMO_API_KEY",
		Headers:   map[string]string{"X-Custom": "remove-me"},
	}
	if err := cfg.UpsertProvider(custom); err != nil {
		t.Fatalf("upsert custom provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := NewApp().ResetProviderPresetAccess("mimo-api"); err != nil {
		t.Fatalf("ResetProviderPresetAccess: %v", err)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("mimo-api provider missing after reset")
	}
	if got.BaseURL != "https://api.xiaomimimo.com/v1" || got.DefaultModel() != "mimo-v2.6-pro" || got.PresetID != "mimo-api" || got.PresetVersion != config.ProviderPresetVersion {
		t.Fatalf("mimo-api provider after reset = %+v, want preset template", got)
	}
	if len(got.Headers) != 0 {
		t.Fatalf("mimo-api headers after reset = %+v, want preset headers", got.Headers)
	}
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["mimo-api"] {
		t.Fatalf("provider_access missing mimo-api after reset: %+v", cfg.Desktop.ProviderAccess)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	if !strings.Contains(string(data), "MIMO_API_KEY=sk-original") {
		t.Fatalf("credentials changed after reset:\n%s", data)
	}

	presetView := providerPresetViewByID(t, NewApp().Settings(), "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusInstalled {
		t.Fatalf("mimo-api preset view = %+v, want installed after reset", presetView)
	}
}

func TestResetProviderPresetAccessRejectsMissingSameNameProvider(t *testing.T) {
	isolateDesktopUserDirs(t)

	if err := NewApp().ResetProviderPresetAccess("mimo-api"); err == nil {
		t.Fatal("ResetProviderPresetAccess unexpectedly reset a missing provider")
	} else if !strings.Contains(err.Error(), "no same-name provider exists") {
		t.Fatalf("ResetProviderPresetAccess error = %v, want missing same-name provider guard", err)
	}
}

func TestAddEveryProviderPresetAccessInstallsTemplate(t *testing.T) {
	for _, preset := range config.CuratedProviderPresets() {
		t.Run(preset.ID, func(t *testing.T) {
			isolateDesktopUserDirs(t)

			if warning, err := NewApp().AddProviderPresetAccess(preset.ID, "sk-test"); err != nil {
				t.Fatalf("AddProviderPresetAccess(%q): %v", preset.ID, err)
			} else if warning != "" {
				t.Fatalf("AddProviderPresetAccess(%q) warning = %q, want none", preset.ID, warning)
			}

			cfg := config.LoadForEdit(config.UserConfigPath())
			access := providerAccessSet(cfg.Desktop.ProviderAccess)
			for _, entry := range preset.Entries {
				got, ok := cfg.Provider(entry.Name)
				if !ok {
					t.Fatalf("provider %q from preset %q was not saved", entry.Name, preset.ID)
				}
				if !access[entry.Name] {
					t.Fatalf("provider_access for preset %q missing %q: %+v", preset.ID, entry.Name, cfg.Desktop.ProviderAccess)
				}
				if got.Kind != entry.Kind || got.BaseURL != entry.BaseURL || got.DefaultModel() != entry.DefaultModel() || got.APIKeyEnv == entry.APIKeyEnv || !config.CredentialStored(got.APIKeyEnv) || got.AuthHeader != entry.AuthHeader || got.NoProxy != entry.NoProxy {
					t.Fatalf("provider %q core fields = %+v, want template %+v", entry.Name, got, entry)
				}
				if got.PresetID != preset.ID || got.PresetVersion != config.ProviderPresetVersion {
					t.Fatalf("provider %q preset metadata = %q/%d, want %q/%d", entry.Name, got.PresetID, got.PresetVersion, preset.ID, config.ProviderPresetVersion)
				}
				if got.ContextWindow != entry.ContextWindow || got.Thinking != entry.Thinking || got.DefaultEffort != entry.DefaultEffort || got.ReasoningProtocol != entry.ReasoningProtocol {
					t.Fatalf("provider %q capability fields = %+v, want template %+v", entry.Name, got, entry)
				}
				if !reflect.DeepEqual(got.ModelList(), entry.ModelList()) || !reflect.DeepEqual(got.SupportedEfforts, entry.SupportedEfforts) {
					t.Fatalf("provider %q models/capabilities = %+v, want template %+v", entry.Name, got, entry)
				}
				resolver := config.NewTransientModelCapabilityResolver()
				for _, model := range entry.ModelList() {
					if actual, expected := resolveTestModelCapability(resolver, *got, model), resolveTestModelCapability(resolver, entry, model); actual.State != expected.State {
						t.Fatalf("provider %s model %s lost capability: %v != %v", entry.Name, model, actual.State, expected.State)
					}
				}
				if !reflect.DeepEqual(got.Headers, entry.Headers) || !reflect.DeepEqual(got.ExtraBody, entry.ExtraBody) {
					t.Fatalf("provider %q request extras = %+v, want template %+v", entry.Name, got, entry)
				}
			}

			view := NewApp().Settings()
			var presetView *ProviderPresetView
			for i := range view.ProviderPresets {
				if view.ProviderPresets[i].ID == preset.ID {
					presetView = &view.ProviderPresets[i]
					break
				}
			}
			if presetView == nil || !presetView.Added || presetView.Status != providerPresetStatusInstalled || !presetView.KeySet || !presetView.Configured {
				t.Fatalf("preset view for %q = %+v, want installed/key-set/configured", preset.ID, presetView)
			}
		})
	}
}

func TestAddOpenCodeGoRecommendedPresetCompletesMissingRoutes(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	preset, ok := config.CuratedProviderPreset("opencode-go-recommended")
	if !ok || len(preset.Entries) != 3 {
		t.Fatalf("recommended preset = %+v, found=%v", preset, ok)
	}
	cfg := config.Default()
	seed := preset.Entries[0]
	seed.PresetID = "opencode-go"
	if err := cfg.UpsertProvider(seed); err != nil {
		t.Fatalf("seed existing OpenCode Go route: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save seed config: %v", err)
	}
	partial := providerPresetViewByID(t, NewApp().Settings(), preset.ID)
	if partial.Status != providerPresetStatusPartial || len(partial.MissingProviderNames) != 2 {
		t.Fatalf("recommended preset partial view = %+v, want two missing routes", partial)
	}

	if warning, err := NewApp().AddProviderPresetAccess(preset.ID, "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none", warning)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	for _, entry := range preset.Entries {
		if _, ok := cfg.Provider(entry.Name); !ok {
			t.Fatalf("missing recommended route %q after completion", entry.Name)
		}
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read saved credentials: %v", err)
	}
	for _, route := range preset.Entries {
		entry, _ := cfg.Provider(route.Name)
		if entry.APIKeyEnv == "OPENCODE_GO_API_KEY" || !strings.Contains(string(data), entry.APIKeyEnv+"=sk-opencode") {
			t.Fatalf("route %s did not receive an isolated credential reference", route.Name)
		}
	}
}

func TestAddOpenCodeGoRecommendedPresetSelectsUsableDefaultForFreshSetup(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	cfg := config.Default()
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save fresh config: %v", err)
	}
	if _, err := NewApp().AddProviderPresetAccess("opencode-go-recommended", "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	if got.DefaultModel != "opencode-go/glm-5.3" {
		t.Fatalf("default model = %q, want ready-to-use OpenCode Go default", got.DefaultModel)
	}
}

func TestAddOpenCodeGoRecommendedPresetPreservesConfiguredDefault(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	cfg := config.Default()
	if err := cfg.UpsertProvider(config.ProviderEntry{
		Name:    "local-ready",
		Kind:    "openai",
		BaseURL: "http://127.0.0.1:11434/v1",
		Models:  []string{"local-model"},
		Default: "local-model",
	}); err != nil {
		t.Fatalf("upsert configured provider: %v", err)
	}
	if err := cfg.SetDefaultModel("local-ready/local-model"); err != nil {
		t.Fatalf("set configured default: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save configured default: %v", err)
	}

	if _, err := NewApp().AddProviderPresetAccess("opencode-go-recommended", "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	}
	if got := config.LoadForEdit(config.UserConfigPath()).DefaultModel; got != "local-ready/local-model" {
		t.Fatalf("default model = %q, want existing configured default preserved", got)
	}
}

func TestAddOpenCodeGoRecommendedPresetPreservesModifiedRoute(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	preset, ok := config.CuratedProviderPreset("opencode-go-recommended")
	if !ok || len(preset.Entries) != 3 {
		t.Fatalf("recommended preset = %+v, found=%v", preset, ok)
	}
	cfg := config.Default()
	modified := preset.Entries[0]
	modified.BaseURL = "https://custom.example/v1"
	modified.PresetID = "opencode-go"
	if err := cfg.UpsertProvider(modified); err != nil {
		t.Fatalf("seed modified OpenCode Go route: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save modified config: %v", err)
	}

	if _, err := NewApp().AddProviderPresetAccess(preset.ID, "sk-opencode"); err != nil {
		t.Fatalf("AddProviderPresetAccess: %v", err)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("opencode-go")
	if !ok || got.BaseURL != "https://custom.example/v1" {
		t.Fatalf("modified route = %+v, want preserved custom endpoint", got)
	}
	for _, name := range []string{"opencode-go-anthropic", "opencode-go-responses"} {
		if _, ok := cfg.Provider(name); !ok {
			t.Fatalf("missing route %q after completing bundle around modified route", name)
		}
	}
}

func TestAddOpenCodeGoRecommendedPresetRejectsConflictAtomically(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("OPENCODE_GO_API_KEY", "")
	os.Unsetenv("OPENCODE_GO_API_KEY")

	cfg := config.Default()
	conflict := config.ProviderEntry{
		Name:          "opencode-go",
		Kind:          "openai",
		BaseURL:       "https://custom.example/v1",
		Models:        []string{"custom-model"},
		Default:       "custom-model",
		APIKeyEnv:     "OPENCODE_GO_API_KEY",
		PresetID:      "custom",
		PresetVersion: 1,
	}
	if err := cfg.UpsertProvider(conflict); err != nil {
		t.Fatalf("upsert conflicting provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save conflict config: %v", err)
	}

	if warning, err := NewApp().AddProviderPresetAccess("opencode-go-recommended", "sk-should-not-save"); err == nil {
		t.Fatal("AddProviderPresetAccess unexpectedly accepted same-name conflict")
	} else if !strings.Contains(err.Error(), "opencode-go") {
		t.Fatalf("AddProviderPresetAccess error = %v, want opencode-go conflict", err)
	} else if warning != "" {
		t.Fatalf("AddProviderPresetAccess warning = %q, want none", warning)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	if _, ok := cfg.Provider("opencode-go-anthropic"); ok {
		t.Fatal("conflicting bundle partially installed Anthropic route")
	}
	if _, err := os.Stat(config.UserCredentialsPath()); err == nil {
		data, readErr := os.ReadFile(config.UserCredentialsPath())
		if readErr != nil {
			t.Fatalf("read credentials: %v", readErr)
		}
		if strings.Contains(string(data), "sk-should-not-save") {
			t.Fatalf("conflicting bundle saved credentials: %s", data)
		}
	}
}

func TestSubmitEntryPointsRejectEmptyProviderInput(t *testing.T) {
	app := NewApp()
	for _, tt := range []struct {
		name string
		call func() error
	}{
		{name: "plain", call: func() error { return app.SubmitToTab("missing", " \n\t ") }},
		{name: "display", call: func() error { return app.SubmitDisplayToTab("missing", "visible prompt", " ") }},
		{name: "invocations", call: func() error {
			return app.SubmitInvocationsToTab("missing", "/skill visible", "", nil)
		}},
		{name: "edited display", call: func() error {
			return app.SubmitEditedDisplayToTab("missing", "visible prompt", "\n", "original prompt")
		}},
		{name: "initial goal", call: func() error {
			_, err := app.SubmitInitialGoalToTab("missing", "goal", "visible prompt", "", nil, "normal", "auto")
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errEmptyTurnInput) {
				t.Fatalf("error = %v, want errEmptyTurnInput", err)
			}
		})
	}
}
