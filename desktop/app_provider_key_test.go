package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/billing"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestSetProviderKeyRestoresOfficialProviderAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	os.Unsetenv("DEEPSEEK_API_KEY")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek/deepseek-v4-flash"

[desktop]
provider_access = []

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

	if _, err := NewApp().SetProviderKey("DEEPSEEK_API_KEY", "sk-test"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if !providerAccessSet(cfg.Desktop.ProviderAccess)["deepseek"] {
		t.Fatalf("provider_access = %+v, want deepseek restored", cfg.Desktop.ProviderAccess)
	}
	got := NewApp().Settings()
	for _, p := range got.Providers {
		if p.Name == "deepseek" {
			if !p.Added || !p.KeySet {
				t.Fatalf("deepseek settings = %+v, want added and key-set", p)
			}
			return
		}
	}
	t.Fatalf("settings providers missing deepseek: %+v", got.Providers)
}

func TestSetProviderKeyKeepsCustomAliasProviderAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("PROXY_DEEPSEEK_KEY", "")
	os.Unsetenv("PROXY_DEEPSEEK_KEY")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
[desktop]
provider_access = []

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://proxy.example/v1"
models = ["deepseek-v4-flash"]
api_key_env = "PROXY_DEEPSEEK_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := NewApp().SetProviderKey("PROXY_DEEPSEEK_KEY", "sk-test"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	cfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	access := providerAccessSet(cfg.Desktop.ProviderAccess)
	if !access["deepseek-flash"] {
		t.Fatalf("provider_access = %+v, want custom alias deepseek-flash", cfg.Desktop.ProviderAccess)
	}
	if access["deepseek"] {
		t.Fatalf("provider_access = %+v, should not canonicalize custom proxy to deepseek", cfg.Desktop.ProviderAccess)
	}
}

func TestSetProviderKeyKeepsCurrentController(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Models: []string{"longcat-chat"}, Default: "longcat-chat", Name: "longcat", Kind: "openai", BaseURL: "https://longcat.example/v1", Model: "longcat-chat", APIKeyEnv: "LONGCAT_API_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	tab := &WorkspaceTab{
		ID:          "tab_provider",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_provider", app: app},
		disabledMCP: map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, oldCtrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	warning, err := app.SetProviderKey("LONGCAT_API_KEY", "sk-longcat")
	if err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SetProviderKey warning = %q; saving does not acquire the session lease", warning)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after provider key save")
	}
	if tab.StartupErr != "" {
		t.Fatalf("tab startup error = %q, want unchanged current session", tab.StartupErr)
	}
	if got := tab.Ctrl.History(); len(got) < 2 || got[1].Content != "hello" {
		t.Fatalf("history after provider key save = %+v", got)
	}
	if access := providerAccessSet(config.LoadForEditWithoutCredentials(config.UserConfigPath()).Desktop.ProviderAccess); !access["longcat"] {
		t.Fatalf("provider_access should still persist longcat after key save")
	}
}

func TestSetProviderKeyPreservesInFlightStartupBuild(t *testing.T) {
	isolateDesktopUserDirs(t)

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"old-model"}, Default: "old-model",
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "startup-build-in-flight.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	// Model the async startup build still being in flight: no controller yet,
	// a live build generation, and a cancellable build context.
	buildCtx, buildCancel := context.WithCancel(context.Background())
	const startupGeneration = 1
	tab := &WorkspaceTab{
		ID:    "tab_key_rebuild",
		Scope: "global",

		model:           "old/old-model",
		buildGeneration: startupGeneration,
		buildCancel:     buildCancel,
		disabledMCP:     map[string]ServerView{},
	}
	tab.sink = &tabEventSink{tabID: tab.ID, app: app}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if _, err := app.SetProviderKey("OLD_MODEL_KEY", "sk-new"); err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if tab.Ctrl != nil || tab.buildGeneration != startupGeneration || buildCtx.Err() != nil {
		t.Fatal("saving a key published or cancelled an in-flight startup; publication owns its version check")
	}
	buildCancel()
}

func TestSaveProviderWithKeyPersistsCustomProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"old-model"}, Default: "old-model",
		Name:      "old",
		Kind:      "openai",
		BaseURL:   "https://example.invalid/v1",
		Model:     "old-model",
		APIKeyEnv: "OLD_MODEL_KEY",
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	tab := &WorkspaceTab{
		ID:          "tab_custom_provider",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_custom_provider", app: app},
		disabledMCP: map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, oldCtrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	warning, err := app.SaveProviderWithKey(ProviderView{
		Name:      "proxy",
		Kind:      "openai",
		BaseURL:   "https://proxy.example/v1",
		Models:    []string{"model-a", "model-b"},
		Default:   "model-a",
		APIKeyEnv: "PROXY_API_KEY",
	}, "sk-proxy")
	if err != nil {
		t.Fatalf("SaveProviderWithKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SaveProviderWithKey warning = %q; saving does not acquire the session lease", warning)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after provider save")
	}
	gotCfg := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	got, ok := gotCfg.Provider("proxy")
	if !ok {
		t.Fatal("custom provider was not saved")
	}
	if want := []string{"model-a", "model-b"}; !reflect.DeepEqual(got.ModelList(), want) {
		t.Fatalf("custom provider models = %v, want %v", got.ModelList(), want)
	}
	if !providerAccessSet(gotCfg.Desktop.ProviderAccess)["proxy"] {
		t.Fatalf("provider_access = %+v, want proxy", gotCfg.Desktop.ProviderAccess)
	}
	data, err := os.ReadFile(config.UserCredentialsPath())
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if got.APIKeyEnv == "PROXY_API_KEY" || !strings.Contains(string(data), got.APIKeyEnv+"=sk-proxy") {
		t.Fatal("provider key was not saved with an isolated reference")
	}
}

func TestModelsForTabOnlyListsProviderAccessWhenConfigured(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-v4-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek", "mimo-pro"}
	cfg.Providers = append(cfg.Providers, config.ProviderEntry{
		Name: "deepseek", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
		Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY",
	})
	cfg.Providers = append(cfg.Providers, config.ProviderEntry{Name: "mimo-pro", Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Models: []string{"mimo-v2.5-pro", "mimo-v2.5"}, Default: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY"})
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{
		"deepseek/deepseek-v4-flash",
		"deepseek/deepseek-v4-pro",
		"mimo-pro/mimo-v2.5-pro",
		"mimo-pro/mimo-v2.5",
	} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
	for _, hidden := range []string{
		"deepseek-pro/deepseek-v4-pro",
		"mimo-flash/mimo-v2.5",
	} {
		if refs[hidden] {
			t.Fatalf("Models() refs = %+v, should not include hidden provider %s", models, hidden)
		}
	}
	if len(models) != 4 {
		t.Fatalf("Models() len = %d, want only explicitly selected provider models: %+v", len(models), models)
	}
}

func TestModelsForTabListsNothingWhenProviderAccessExplicitlyEmpty(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.Desktop.ProviderAccess = []string{}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if models := NewApp().Models(); len(models) != 0 {
		t.Fatalf("Models() = %+v, want no models when provider access is explicitly empty", models)
	}
}

func TestSaveProviderPersistsReasoningProtocol(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name:              "deepseek-proxy",
		Kind:              "openai",
		BaseURL:           "https://proxy.example.com/v1",
		Models:            []string{"deepseek-v4-flash"},
		Default:           "deepseek-v4-flash",
		APIKeyEnv:         "DEEPSEEK_PROXY_KEY",
		ReasoningProtocol: "none",
		SupportedEfforts:  []string{"high", "max"},
		DefaultEffort:     "max",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("deepseek-proxy")
	if !ok {
		t.Fatal("saved provider not found")
	}
	if got.ReasoningProtocol != "none" || got.DefaultEffort != "max" {
		t.Fatalf("saved provider = %+v, want reasoning_protocol none and default_effort max", got)
	}

	view := app.Settings()
	for _, p := range view.Providers {
		if p.Name == "deepseek-proxy" {
			if p.ReasoningProtocol != "none" {
				t.Fatalf("settings reasoningProtocol = %q, want none", p.ReasoningProtocol)
			}
			return
		}
	}
	t.Fatalf("Settings() missing saved provider: %+v", view.Providers)
}

func TestConnectKeyRestoresDeepSeekProviderAccess(t *testing.T) {
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

	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "custom"}), "custom/custom-model")
	defer func() {
		if ctrl := app.activeCtrl(); ctrl != nil {
			ctrl.Close()
		}
	}()
	if _, err := app.ConnectKey("sk-test"); err != nil {
		t.Fatalf("ConnectKey: %v", err)
	}

	got := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if !providerAccessSet(got.Desktop.ProviderAccess)["deepseek"] {
		t.Fatalf("provider_access = %v, want DeepSeek restored", got.Desktop.ProviderAccess)
	}
	if _, ok := got.Provider("deepseek"); !ok {
		t.Fatal("DeepSeek provider template should be restored")
	}
	if app.NeedsOnboarding() {
		t.Fatal("restored DeepSeek access and saved key should satisfy onboarding")
	}
}

func TestConnectKeyFreshInstallUsesDeepSeekChatAndIndependentSearchDefaults(t *testing.T) {
	isolateDesktopUserDirs(t)
	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "fresh-install"}), "deepseek-flash/deepseek-flash")
	workspace := t.TempDir()
	app.tabs["test"].WorkspaceRoot = workspace
	defer func() {
		if ctrl := app.activeCtrl(); ctrl != nil {
			ctrl.Close()
		}
	}()

	if _, err := app.ConnectKey("sk-test"); err != nil {
		t.Fatalf("ConnectKey: %v", err)
	}
	cfg, err := config.LoadForRootReadOnly(workspace)
	if err != nil {
		t.Fatalf("load fresh-install config: %v", err)
	}
	entry, ok := cfg.ResolveModel(cfg.DefaultModel)
	if !ok {
		t.Fatalf("default model %q did not resolve", cfg.DefaultModel)
	}
	if entry.Kind != "openai" || entry.BaseURL != "https://api.deepseek.com" ||
		entry.Thinking != "enabled" || !config.EffectiveIndependentWebSearch(entry) || !config.EffectiveVision(entry) {
		t.Fatalf("fresh-install DeepSeek entry = %+v; want Chat Completions, thinking, independent search, and native image input", entry)
	}
	if app.NeedsOnboarding() {
		t.Fatal("fresh-install onboarding should close after the validated DeepSeek key is stored")
	}
}

func TestBalanceForTabUsesDesktopPricingCurrency(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Billing.DisplayCurrency = "USD"
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save USD desktop currency: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"70.16"},{"currency":"USD","total_balance":"9.82"}]}`)
	}))
	defer srv.Close()

	app := NewApp()
	app.ctx = context.Background()
	ctrl := control.New(control.Options{BalanceURL: srv.URL, BalanceClient: srv.Client()})
	t.Cleanup(ctrl.Close)
	app.setTestCtrl(ctrl, "deepseek/deepseek-v4-flash")

	got := app.BalanceForTab("test")
	// Prefer the matching USD wallet exactly; no FX approximation is used.
	if !got.Available || got.Err != "" {
		t.Fatalf("USD desktop balance = %+v, want available", got)
	}
	if !strings.Contains(got.Display, "9.82") && !strings.Contains(got.Display, "$9.82") {
		t.Fatalf("USD desktop balance display = %q, want USD 9.82", got.Display)
	}
}

func TestConnectKeyKeepsCurrentController(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv(onboardingKeyEnv, "")
	os.Unsetenv(onboardingKeyEnv)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	oldFetch := connectKeyBalanceFetch
	connectKeyBalanceFetch = func(context.Context, *http.Client, string, string) (*billing.Balance, error) {
		return &billing.Balance{Available: true}, nil
	}
	t.Cleanup(func() { connectKeyBalanceFetch = oldFetch })

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	tab := &WorkspaceTab{
		ID:          "tab_connect",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_connect", app: app},
		disabledMCP: map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, oldCtrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	warning, err := app.ConnectKey("sk-test")
	if err != nil {
		t.Fatalf("ConnectKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("ConnectKey warning = %q; saving does not acquire a runtime lease", warning)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after key connection")
	}
	if tab.StartupErr != "" {
		t.Fatalf("tab startup error = %q, want unchanged current session", tab.StartupErr)
	}
	p, ok := config.LoadForEdit(config.UserConfigPath()).Provider("deepseek")
	if !ok || !p.Configured() {
		t.Fatal("onboarding key should be persisted under the new connection reference")
	}
}
