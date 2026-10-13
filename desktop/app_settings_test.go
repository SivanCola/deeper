package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/config"
)

func setDesktopTestCredential(t *testing.T, key, value string) {
	t.Helper()
	if _, err := config.SetCredential(key, value); err != nil {
		t.Fatalf("SetCredential(%s): %v", key, err)
	}
}

func TestNeedsOnboardingIgnoresInheritedEnv(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv(onboardingKeyEnv, "inherited-key")

	app := NewApp()
	if !app.NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should require a key saved in Deepzora global .env")
	}
	setDesktopTestCredential(t, onboardingKeyEnv, "saved-key")
	if app.NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should be false after saving the global credential")
	}
}

func TestNeedsOnboardingTreatsBlankSavedKeyAsMissing(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserCredentialsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.UserCredentialsPath(), []byte(onboardingKeyEnv+"=\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if !app.NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should require a non-empty saved credential")
	}
}

func TestNeedsOnboardingAcceptsConfiguredCustomProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "custom/custom-model"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"custom-model"}, Default: "custom-model",
		Name: "custom", Kind: "openai", BaseURL: "https://models.example.invalid/v1",
		Model: "custom-model", APIKeyEnv: "CUSTOM_API_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save custom provider config: %v", err)
	}
	setDesktopTestCredential(t, "CUSTOM_API_KEY", "saved-custom-key")

	if NewApp().NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should be false when a custom provider is configured")
	}
}

func TestNeedsOnboardingAcceptsNoAuthLocalProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "local/local-model"
	cfg.Desktop.ProviderAccess = []string{"local"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"local-model"}, Default: "local-model",
		Name: "local", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1",
		Model: "local-model",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save local provider config: %v", err)
	}

	if NewApp().NeedsOnboarding() {
		t.Fatal("NeedsOnboarding should be false for a no-auth local provider")
	}
}

func TestSettingsUsesUserDesktopPreferencesNotProjectConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, "deepzora.toml"), []byte(`
[desktop]
language = "zh"
layout_style = "workbench"
theme = "light"
theme_style = "glacier"
close_behavior = "quit"
status_bar_style = "icon"
status_bar_items = ["cost", "balance"]
`), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	approveWorkspace(t, project)

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("set desktop language: %v", err)
	}
	if err := userCfg.SetDesktopLayoutStyle("workbench"); err != nil {
		t.Fatalf("set desktop layout style: %v", err)
	}
	if err := userCfg.SetDesktopAppearance("dark", "graphite"); err != nil {
		t.Fatalf("set desktop appearance: %v", err)
	}
	if err := userCfg.SetDesktopTerminalTheme("light"); err != nil {
		t.Fatalf("set desktop terminal theme: %v", err)
	}
	if err := userCfg.SetDesktopCloseBehavior("background"); err != nil {
		t.Fatalf("set desktop close behavior: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarStyle("text"); err != nil {
		t.Fatalf("set desktop status bar style: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarItems([]string{"model", "balance", "cache"}); err != nil {
		t.Fatalf("set desktop status bar items: %v", err)
	}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(project); err != nil {
		t.Fatalf("chdir project: %v", err)
	}

	got := NewApp().Settings()
	if got.DesktopLanguage != "en" || got.DesktopLayoutStyle != "workbench" || got.DesktopTheme != "dark" || got.DesktopThemeStyle != "graphite" || got.DesktopTerminalTheme != "light" || got.CloseBehavior != "background" || got.StatusBarStyle != "text" {
		t.Fatalf("desktop settings = lang:%q layout:%q theme:%q style:%q close:%q status:%q, want user-level desktop prefs", got.DesktopLanguage, got.DesktopLayoutStyle, got.DesktopTheme, got.DesktopThemeStyle, got.CloseBehavior, got.StatusBarStyle)
	}
	if want := []string{"model", "balance", "cache"}; !reflect.DeepEqual(got.StatusBarItems, want) {
		t.Fatalf("desktop status bar items = %v, want user-level %v", got.StatusBarItems, want)
	}
}

func TestDesktopStartupSettingsUsesUserDesktopPreferencesWithoutFullSettingsPayload(t *testing.T) {
	isolateDesktopUserDirs(t)

	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("set desktop language: %v", err)
	}
	if err := userCfg.SetDesktopLayoutStyle("workbench"); err != nil {
		t.Fatalf("set desktop layout style: %v", err)
	}
	if err := userCfg.SetDesktopAppearance("dark", "graphite"); err != nil {
		t.Fatalf("set desktop appearance: %v", err)
	}
	if err := userCfg.SetDesktopTerminalTheme("light"); err != nil {
		t.Fatalf("set desktop terminal theme: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarStyle("icon"); err != nil {
		t.Fatalf("set desktop status bar style: %v", err)
	}
	if err := userCfg.SetDesktopStatusBarItems([]string{"workspace", "git_branch", "model"}); err != nil {
		t.Fatalf("set desktop status bar items: %v", err)
	}
	if err := userCfg.SetDesktopCheckUpdates(false); err != nil {
		t.Fatalf("set desktop check updates: %v", err)
	}

	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	got := NewApp().DesktopStartupSettings()
	if got.DesktopLanguage != "en" || got.DesktopLayoutStyle != "workbench" || got.DesktopTheme != "dark" || got.DesktopThemeStyle != "graphite" || got.DesktopTerminalTheme != "light" || got.SessionExperience != "standard" || got.StatusBarStyle != "icon" || got.CheckUpdates || got.UpdateChannel != "stable" {
		t.Fatalf("DesktopStartupSettings desktop prefs = %+v, want user-level startup prefs", got)
	}
	if want := []string{"workspace", "git_branch", "model"}; !reflect.DeepEqual(got.StatusBarItems, want) {
		t.Fatalf("DesktopStartupSettings status bar items = %v, want %v", got.StatusBarItems, want)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal DesktopStartupSettings: %v", err)
	}
	if strings.Contains(string(raw), "providers") || strings.Contains(string(raw), "officialProviders") || strings.Contains(string(raw), "providerKinds") {
		t.Fatalf("DesktopStartupSettings must not include full Settings provider payload: %s", raw)
	}
}

func BenchmarkDesktopSettingsPayloads(b *testing.B) {
	home := b.TempDir()
	xdg := filepath.Join(home, ".config")
	appData := filepath.Join(home, "AppData")
	for _, dir := range []string{xdg, appData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
	}
	b.Setenv("HOME", home)
	b.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	b.Setenv("USERPROFILE", home)
	b.Setenv("XDG_CONFIG_HOME", xdg)
	b.Setenv("DEEPZORA_STATE_HOME", filepath.Join(home, "state"))
	b.Setenv("DEEPZORA_CACHE_HOME", filepath.Join(home, "cache"))
	b.Setenv("AppData", appData)
	b.Setenv("SHARED_PROVIDER_KEY", "sk-test")

	cfg := config.LoadForEdit(config.UserConfigPath())
	for i := range 40 {
		cfg.Providers = append(cfg.Providers, config.ProviderEntry{
			Name:      fmt.Sprintf("custom-%02d", i),
			Kind:      "openai",
			BaseURL:   "https://example.invalid/v1",
			APIKeyEnv: "SHARED_PROVIDER_KEY",
			Models:    []string{"model-a", "model-b"},
			Default:   "model-a",
		})
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		b.Fatalf("save config: %v", err)
	}
	app := NewApp()

	b.Run("Settings", func(b *testing.B) {
		for range b.N {
			_ = app.Settings()
		}
	})
	b.Run("DesktopStartupSettings", func(b *testing.B) {
		for range b.N {
			_ = app.DesktopStartupSettings()
		}
	})
}

func TestSettingsIgnoresActiveWorkspaceDotEnvCredentialsWithUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	launch := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("WORKSPACE_ONLY_KEY=from-project\n"), 0o600); err != nil {
		t.Fatalf("write project env: %v", err)
	}
	userCfg := config.LoadForEdit(config.UserConfigPath())
	if err := userCfg.UpsertProvider(config.ProviderEntry{Models: []string{"workspace-model"}, Default: "workspace-model",
		Name:      "workspace-provider",
		Kind:      "openai",
		BaseURL:   "https://workspace.example/v1",
		Model:     "workspace-model",
		APIKeyEnv: "WORKSPACE_ONLY_KEY",
	}); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	userCfg.Desktop.ProviderAccess = []string{"workspace-provider"}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}
	t.Setenv("WORKSPACE_ONLY_KEY", "")
	os.Unsetenv("WORKSPACE_ONLY_KEY")
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(launch); err != nil {
		t.Fatalf("chdir launch: %v", err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: project}}
	app.activeTabID = "project"
	got := app.Settings()
	for _, p := range got.Providers {
		if p.Name == "workspace-provider" {
			if p.KeySet {
				t.Fatalf("workspace provider keySet = true, want false because workspace .env is ignored: %+v", p)
			}
			if p.Configured {
				t.Fatalf("workspace provider configured = true, want false because workspace .env is ignored: %+v", p)
			}
			return
		}
	}
	t.Fatalf("workspace provider missing from settings: %+v", got.Providers)
}

func TestSettingsShowsGlobalCredentialWithoutMutatingWorkspaceEnv(t *testing.T) {
	isolateDesktopUserDirs(t)

	project := robustTempDir(t)
	launch := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("SHARED_SETTINGS_KEY=from-project\n"), 0o600); err != nil {
		t.Fatalf("write project env: %v", err)
	}
	if _, err := config.SetCredential("SHARED_SETTINGS_KEY", "from-credentials"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	userCfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if err := userCfg.UpsertProvider(config.ProviderEntry{Models: []string{"settings-model"}, Default: "settings-model",
		Name:      "settings-provider",
		Kind:      "openai",
		BaseURL:   "https://settings.example/v1",
		Model:     "settings-model",
		APIKeyEnv: "SHARED_SETTINGS_KEY",
	}); err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	userCfg.Desktop.ProviderAccess = []string{"settings-provider"}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}
	t.Setenv("SHARED_SETTINGS_KEY", "from-project")
	orig, _ := os.Getwd()
	defer func() { _ = os.Chdir(orig) }()
	if err := os.Chdir(launch); err != nil {
		t.Fatalf("chdir launch: %v", err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: project}}
	app.activeTabID = "project"
	got := app.Settings()
	for _, p := range got.Providers {
		if p.Name != "settings-provider" {
			continue
		}
		if !p.KeySet || !strings.Contains(p.KeySource, "Deepzora credentials") {
			t.Fatalf("settings-provider key = set:%v source:%q, want Deepzora credentials: %+v", p.KeySet, p.KeySource, p)
		}
		if env := os.Getenv("SHARED_SETTINGS_KEY"); env != "from-project" {
			t.Fatalf("Settings mutated SHARED_SETTINGS_KEY = %q, want existing project env", env)
		}
		return
	}
	t.Fatalf("settings provider missing from settings: %+v", got.Providers)
}

// With no user config yet, Settings start from the defaults, not from the
// checkout's deepzora.toml, and the first edit writes only the user's choice.
func TestSettingsNeverSeedTheUserConfigFromTheProjectFile(t *testing.T) {
	isolateDesktopUserDirs(t)
	project := robustTempDir(t)
	if err := os.WriteFile(filepath.Join(project, "deepzora.toml"), []byte("[desktop]\ntheme = \"light\"\n\n[permissions]\nmode = \"allow\"\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	approveWorkspace(t, project)
	t.Chdir(project)
	app := NewApp()
	if got := app.Settings(); got.ConfigPath != config.UserConfigPath() || got.DesktopTheme == "light" {
		t.Fatalf("Settings = path %q theme %q, want the user config's defaults", got.ConfigPath, got.DesktopTheme)
	}
	if err := app.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("SetDesktopLanguage: %v", err)
	}
	userCfg := config.LoadForEdit(config.UserConfigPath())
	if userCfg.DesktopLanguage() != "en" || userCfg.DesktopTheme() == "light" || userCfg.Permissions.Mode == "allow" {
		t.Fatalf("user config took the checkout's values: theme %q mode %q", userCfg.DesktopTheme(), userCfg.Permissions.Mode)
	}
}

func TestSettingsSubagentDefaultsRoundTrip(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-flash"

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	app := NewApp()
	if got := app.Settings().Agent.MaxSubagentDepth; got != agent.DefaultMaxSubagentDepth {
		t.Fatalf("default max subagent depth = %d, want %d", got, agent.DefaultMaxSubagentDepth)
	}
	if err := app.SetSubagentModel("deepseek/deepseek-v4-pro"); err != nil {
		t.Fatalf("SetSubagentModel: %v", err)
	}
	if err := app.SetSubagentEffort("max"); err != nil {
		t.Fatalf("SetSubagentEffort: %v", err)
	}
	if err := app.SetMaxSubagentDepth(1); err != nil {
		t.Fatalf("SetMaxSubagentDepth(1): %v", err)
	}
	if err := app.SetMaxSubagentDepth(2); err != nil {
		t.Fatalf("SetMaxSubagentDepth(2): %v", err)
	}

	got := app.Settings()
	if got.SubagentModel != "deepseek/deepseek-v4-pro" || got.SubagentEffort != "max" {
		t.Fatalf("subagent settings = model:%q effort:%q", got.SubagentModel, got.SubagentEffort)
	}
	if got.Agent.MaxSubagentDepth != 2 {
		t.Fatalf("max subagent depth = %d, want 2", got.Agent.MaxSubagentDepth)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.SubagentModel != "deepseek/deepseek-v4-pro" || cfg.Agent.SubagentEffort != "max" {
		t.Fatalf("saved config = model:%q effort:%q", cfg.Agent.SubagentModel, cfg.Agent.SubagentEffort)
	}
	if cfg.Agent.MaxSubagentDepth != 2 {
		t.Fatalf("saved max_subagent_depth = %d, want 2", cfg.Agent.MaxSubagentDepth)
	}
}

func TestSettingsSurfacesOfficialProviderTemplatesSeparately(t *testing.T) {
	isolateDesktopUserDirs(t)

	got := NewApp().Settings()
	providers := providerAccessSet(providerNamesFromView(got.Providers))
	official := providerAccessSet(providerNamesFromView(got.OfficialProviders))
	if providers["mimo-api"] {
		t.Fatalf("mimo-api should not be mixed into configured providers: %+v", got.Providers)
	}
	if !official["deepseek"] || official["mimo-api"] || official["mimo-token-plan"] {
		t.Fatalf("official providers = %+v, want only deepseek", got.OfficialProviders)
	}
}

func TestSettingsTreatsReservedProviderNameWithExternalEndpointAsCustom(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-Flash"

[desktop]
provider_access = ["deepseek"]

[[providers]]
name = "deepseek"
kind = "openai"
base_url = "https://opencode.ai/zen/go/v1"
models = ["deepseek-v4-Flash", "deepseek-v4-pro", "glm-5"]
default = "deepseek-v4-Flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := NewApp().Settings()
	var custom *ProviderView
	for i := range got.Providers {
		if got.Providers[i].Name == "deepseek" {
			custom = &got.Providers[i]
			break
		}
	}
	if custom == nil {
		t.Fatalf("settings providers missing deepseek: %+v", got.Providers)
	}
	if custom.BuiltIn {
		t.Fatalf("external deepseek endpoint should be custom, got built-in provider: %+v", *custom)
	}
	if !custom.Added || !custom.KeySet || custom.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("external deepseek provider = %+v, want added key-set custom opencode endpoint", *custom)
	}
	for _, p := range got.OfficialProviders {
		if p.Name == "deepseek" && p.Added {
			t.Fatalf("official DeepSeek template should not be marked added by external endpoint: %+v", p)
		}
	}
}

func TestSettingsDoesNotInferProviderAccessWhenExplicitlyEmpty(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash/deepseek-flash"

[desktop]
provider_access = []

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Added {
			t.Fatalf("provider %+v should not be inferred as added when provider_access is explicit empty", p)
		}
	}
}

func TestSettingsDoesNotInferBuiltInsWithoutKeys(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("MIMO_API_KEY", "")

	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Added {
			t.Fatalf("provider %+v should not be inferred as added without a configured key", p)
		}
	}
}

func TestSettingsSurfacesCuratedProviderPresets(t *testing.T) {
	isolateDesktopUserDirs(t)

	view := NewApp().Settings()
	if len(view.ProviderPresets) < 18 {
		t.Fatalf("Settings().ProviderPresets length = %d, want curated custom presets", len(view.ProviderPresets))
	}
	got := map[string]ProviderPresetView{}
	for _, preset := range view.ProviderPresets {
		got[preset.ID] = preset
	}
	for _, curated := range config.CuratedProviderPresets() {
		id := curated.ID
		preset, ok := got[id]
		if !ok {
			t.Fatalf("Settings().ProviderPresets missing %q: %+v", id, view.ProviderPresets)
		}
		if preset.KeyEnv == "" || len(preset.ProviderNames) == 0 || len(preset.Models) == 0 {
			t.Fatalf("preset %q view has missing fields: %+v", id, preset)
		}
		if preset.ID == "opencode-go-recommended" && (preset.DisplayGroup != "opencode" || preset.DisplaySection != "go" || preset.DisplayTier != "primary" || preset.RouteKind != "bundle") {
			t.Fatalf("recommended OpenCode metadata = %+v", preset)
		}
	}
}

func TestSettingsMarksPresetAddedWhenSameNameProviderExistsWithoutAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[desktop]
provider_access = []

[[providers]]
name = "mimo-api"
kind = "openai"
base_url = "https://custom.example/v1"
models = ["custom-model"]
default = "custom-model"
api_key_env = "MIMO_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusNameConflict || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"mimo-api"}) {
		t.Fatalf("mimo-api preset view = %+v, want name-conflict because a different same-name provider exists", presetView)
	}

	var providerView *ProviderView
	for i := range view.Providers {
		if view.Providers[i].Name == "mimo-api" {
			providerView = &view.Providers[i]
			break
		}
	}
	if providerView == nil {
		t.Fatal("mimo-api provider view missing")
	}
	if providerView.Added {
		t.Fatalf("mimo-api provider Added = true, want false until provider_access explicitly enables it")
	}
}

func TestSettingsMarksLegacyEquivalentPresetAsInstalled(t *testing.T) {
	isolateDesktopUserDirs(t)
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("missing mimo-api preset")
	}
	legacy := preset.Entries[0]
	legacy.PresetID = ""
	legacy.PresetVersion = 0
	cfg := config.Default()
	if err := cfg.UpsertProvider(legacy); err != nil {
		t.Fatalf("upsert legacy provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusInstalled || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"mimo-api"}) {
		t.Fatalf("mimo-api preset view = %+v, want installed for legacy equivalent config", presetView)
	}
}

func TestSettingsMarksPresetWithChangedCoreConfigAsModified(t *testing.T) {
	isolateDesktopUserDirs(t)
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("missing mimo-api preset")
	}
	modified := preset.Entries[0]
	modified.BaseURL = "https://custom.example/v1"
	cfg := config.Default()
	if err := cfg.UpsertProvider(modified); err != nil {
		t.Fatalf("upsert modified provider: %v", err)
	}
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if !presetView.Added || presetView.Status != providerPresetStatusInstalledModified || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"mimo-api"}) {
		t.Fatalf("mimo-api preset view = %+v, want installed-modified for edited preset provider", presetView)
	}
}

func TestSettingsPreservesStepFunRegionalPresetBaseURLs(t *testing.T) {
	isolateDesktopUserDirs(t)

	cfg := config.Default()
	stepfun, ok := config.CuratedProviderPreset("stepfun")
	if !ok || len(stepfun.Entries) != 1 {
		t.Fatal("missing stepfun preset")
	}
	stepfunEntry := stepfun.Entries[0]
	stepfunEntry.BaseURL = "https://api.stepfun.ai/step_plan/v1"
	stepfunAnthropic, ok := config.CuratedProviderPreset("stepfun-anthropic")
	if !ok || len(stepfunAnthropic.Entries) != 1 {
		t.Fatal("missing stepfun-anthropic preset")
	}
	stepfunAnthropicEntry := stepfunAnthropic.Entries[0]
	stepfunAnthropicEntry.BaseURL = "https://api.stepfun.ai/step_plan"
	cfg.Providers = append(cfg.Providers, stepfunEntry, stepfunAnthropicEntry)
	cfg.Desktop.ProviderAccess = []string{"stepfun", "stepfun-anthropic"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	for _, id := range []string{"stepfun", "stepfun-anthropic"} {
		presetView := providerPresetViewByID(t, view, id)
		if !presetView.Added || presetView.Status != providerPresetStatusInstalledModified {
			t.Fatalf("%s preset view = %+v, want installed-modified for a preserved regional endpoint", id, presetView)
		}
	}

	loaded := config.LoadForEdit(config.UserConfigPath())
	stepfunEntryView, ok := loaded.Provider("stepfun")
	if !ok {
		t.Fatal("stepfun provider missing after load")
	}
	if got := stepfunEntryView.BaseURL; got != "https://api.stepfun.ai/step_plan/v1" {
		t.Fatalf("stepfun base_url = %q, want preserved regional URL", got)
	}
	stepfunAnthropicEntryView, ok := loaded.Provider("stepfun-anthropic")
	if !ok {
		t.Fatal("stepfun-anthropic provider missing after load")
	}
	if got := stepfunAnthropicEntryView.BaseURL; got != "https://api.stepfun.ai/step_plan" {
		t.Fatalf("stepfun-anthropic base_url = %q, want preserved regional URL", got)
	}
}

func TestSettingsMarksSimilarProviderPresetWithoutBlockingAdd(t *testing.T) {
	isolateDesktopUserDirs(t)
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("missing mimo-api preset")
	}
	similar := preset.Entries[0]
	similar.Name = "my-mimo"
	similar.PresetID = ""
	similar.PresetVersion = 0
	cfg := config.Default()
	if err := cfg.UpsertProvider(similar); err != nil {
		t.Fatalf("upsert similar provider: %v", err)
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	view := NewApp().Settings()
	presetView := providerPresetViewByID(t, view, "mimo-api")
	if presetView.Added || presetView.Status != providerPresetStatusSimilarExisting || !reflect.DeepEqual(presetView.StatusProviderNames, []string{"my-mimo"}) {
		t.Fatalf("mimo-api preset view = %+v, want non-blocking similar-existing status", presetView)
	}
}
