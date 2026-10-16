package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/hook"
	"deepzora/internal/provider"
	"deepzora/internal/provider/openai"
	"deepzora/internal/sandbox"
)

func TestProviderViewFromEntry_FiltersNonChatModels(t *testing.T) {
	p := config.ProviderEntry{
		Name: "mimo-api",
		Models: []string{
			"mimo-v2", "mimo-v2-pro",
			"mimo-v2-asr", "mimo-v2-tts",
			"mimo-v2-tts-voiceclone", "mimo-v2-tts-voicedesign",
		},
		ModelOverrides: map[string]config.ProviderModelOverride{"mimo-v2": {Vision: new(true)}, "mimo-v2-asr": {Vision: new(true)}, "mimo-v2-omni": {Vision: new(true)}},
	}
	view := providerViewFromEntry(p, true, false)
	want := []string{"mimo-v2", "mimo-v2-pro"}
	if !reflect.DeepEqual(view.Models, want) {
		t.Errorf("ProviderView.Models = %v, want %v", view.Models, want)
	}
	if got, want := view.VisionModels, []string{"mimo-v2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ProviderView.VisionModels = %v, want %v", got, want)
	}
}

func TestProviderModelOverridesPreservePerModelContextWindow(t *testing.T) {
	overrides := map[string]config.ProviderModelOverride{
		"short-model": {ContextWindow: 32_768},
		"long-model":  {ContextWindow: 1_000_000},
		"removed":     {ContextWindow: 8_192},
	}
	models := []string{"short-model", "long-model"}

	view := providerModelOverridesForView(overrides, models)
	if len(view) != 2 || view[0].Model != "long-model" || view[0].ContextWindow != 1_000_000 || view[1].Model != "short-model" || view[1].ContextWindow != 32_768 {
		t.Fatalf("provider model override view = %+v", view)
	}

	view[0].ContextWindow = -1
	saved := providerModelOverridesForSave(view, models)
	if _, ok := saved["long-model"]; ok {
		t.Fatalf("non-positive context-only override should be removed: %+v", saved)
	}
	if got := saved["short-model"].ContextWindow; got != 32_768 {
		t.Fatalf("saved short-model context window = %d, want 32768", got)
	}
}

func TestProviderViewFromEntryIncludesThinking(t *testing.T) {
	view := providerViewFromEntry(config.ProviderEntry{
		Name:     "anthropic",
		Thinking: "ADAPTIVE",
	}, false, true)
	if view.Thinking != "adaptive" {
		t.Fatalf("ProviderView.Thinking = %q, want adaptive", view.Thinking)
	}
}

func TestProviderViewFromEntryShowsKeySource(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("TEST_PROVIDER_KEY_SOURCE", "")
	os.Unsetenv("TEST_PROVIDER_KEY_SOURCE")
	if _, err := config.SetCredential("TEST_PROVIDER_KEY_SOURCE", "sk-test"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}

	view := providerViewFromEntry(config.ProviderEntry{
		Name:      "custom",
		APIKeyEnv: "TEST_PROVIDER_KEY_SOURCE",
	}, false, true)
	if !view.KeySet {
		t.Fatal("KeySet = false, want true")
	}
	if !view.Configured {
		t.Fatal("Configured = false, want true from resolved credentials")
	}
	if view.KeySource == "" || !strings.Contains(view.KeySource, "credentials") {
		t.Fatalf("KeySource = %q, want credentials source", view.KeySource)
	}
}

func TestSettingsExposesEffectiveSandboxWriteRoots(t *testing.T) {
	home := isolateDesktopUserDirs(t)
	project := robustTempDir(t)
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Sandbox.AllowWrite = []string{
		"${HOME}/.m2",
		"${HOME}/.m2/repository",
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"project": {ID: "project", Scope: "project", WorkspaceRoot: project, Ready: true},
	}
	app.activeTabID = "project"

	got := app.Settings().Sandbox
	if got.EffectiveWorkspaceRoot != project {
		t.Fatalf("EffectiveWorkspaceRoot = %q, want %q", got.EffectiveWorkspaceRoot, project)
	}
	// Settings expose expanded configured roots; the writer confiner normalizes
	// separators later when enforcing them.
	want := []string{
		project,
		home + "/.m2",
		home + "/.m2/repository",
	}
	if !reflect.DeepEqual(got.EffectiveWriteRoots, want) {
		t.Fatalf("EffectiveWriteRoots = %v, want %v", got.EffectiveWriteRoots, want)
	}
	if !reflect.DeepEqual(got.AllowWrite, cfg.Sandbox.AllowWrite) {
		t.Fatalf("AllowWrite = %v, want raw configured paths %v", got.AllowWrite, cfg.Sandbox.AllowWrite)
	}
	if got.EffectiveShell == "" {
		t.Fatal("EffectiveShell is empty")
	}
}

func TestSandboxEffectiveShellViewLabels(t *testing.T) {
	cases := []struct {
		name  string
		shell sandbox.Shell
		want  string
	}{
		{"bash", sandbox.Shell{Kind: sandbox.ShellBash, Path: "bash"}, "bash"},
		{"git bash", sandbox.Shell{Kind: sandbox.ShellBash, Path: `C:\Program Files\Git\bin\bash.exe`}, "git-bash"},
		{"windows powershell", sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: "powershell"}, "powershell"},
		{"pwsh", sandbox.Shell{Kind: sandbox.ShellPowerShell, Path: "pwsh"}, "pwsh"},
	}
	for _, tc := range cases {
		if got := sandboxEffectiveShellView(tc.shell); got != tc.want {
			t.Errorf("%s: sandboxEffectiveShellView() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProviderViewFromEntryExposesNoAuthAvailability(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("LOCAL_API_KEY", "")
	os.Unsetenv("LOCAL_API_KEY")

	noAuth := providerViewFromEntry(config.ProviderEntry{
		Name:    "local",
		Kind:    "openai",
		BaseURL: "http://127.0.0.1:23333/v1",
		Models:  []string{"model-a"},
	}, false, true)
	if noAuth.RequiresKey {
		t.Fatal("no-auth provider RequiresKey = true, want false")
	}
	if !noAuth.Configured {
		t.Fatal("no-auth provider Configured = false, want true")
	}
	if noAuth.KeySet {
		t.Fatal("no-auth provider KeySet = true, want false")
	}

	legacyLoopback := providerViewFromEntry(config.ProviderEntry{
		Name:      "local",
		Kind:      "openai",
		BaseURL:   "http://127.0.0.1:23333/v1",
		Models:    []string{"model-a"},
		APIKeyEnv: "LOCAL_API_KEY",
	}, false, true)
	if legacyLoopback.RequiresKey {
		t.Fatal("loopback provider with missing legacy key env RequiresKey = true, want false")
	}
	if !legacyLoopback.Configured {
		t.Fatal("loopback provider with missing legacy key env Configured = false, want true")
	}

	official := providerViewFromEntry(config.ProviderEntry{
		Name:    "deepseek",
		Kind:    "openai",
		BaseURL: "https://api.deepseek.com",
		Models:  []string{"deepseek-v4-flash"},
	}, true, true)
	if !official.RequiresKey {
		t.Fatal("official provider RequiresKey = false, want true")
	}
	if official.Configured {
		t.Fatal("official provider without key Configured = true, want false")
	}
}

func TestSetProviderKeyDoesNotWarnWhenProjectEnvAlsoDefinesSavedKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("TEST_PROVIDER_SHADOW=old-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_PROVIDER_SHADOW", "")
	os.Unsetenv("TEST_PROVIDER_SHADOW")

	app := &App{
		tabs:        map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: project}},
		activeTabID: "project",
	}
	seedProviderCredentialReference(t, app, "TEST_PROVIDER_SHADOW")
	warning, err := app.SetProviderKey("TEST_PROVIDER_SHADOW", "new-key")
	if err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SetProviderKey warning = %q, want no warning because provider keys use global credentials only", warning)
	}
	data, readErr := os.ReadFile(config.UserCredentialsPath())
	if readErr != nil {
		t.Fatalf("read credentials: %v", readErr)
	}
	p, _ := config.LoadForEdit(config.UserConfigPath()).Provider("credential-test")
	if p.APIKeyEnv == "TEST_PROVIDER_SHADOW" || !strings.Contains(string(data), p.APIKeyEnv+"=new-key") {
		t.Fatal("saved credentials missing new isolated reference")
	}
}

func TestSetProviderKeyDoesNotWarnWhenEnvironmentAlsoDefinesSavedKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("TEST_PROVIDER_EMPTY_ENV", "")

	app := &App{}
	seedProviderCredentialReference(t, app, "TEST_PROVIDER_EMPTY_ENV")
	warning, err := app.SetProviderKey("TEST_PROVIDER_EMPTY_ENV", "new-key")
	if err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SetProviderKey warning = %q, want no warning because provider keys use global credentials only", warning)
	}
	data, readErr := os.ReadFile(config.UserCredentialsPath())
	if readErr != nil {
		t.Fatalf("read credentials: %v", readErr)
	}
	p, _ := config.LoadForEdit(config.UserConfigPath()).Provider("credential-test")
	if p.APIKeyEnv == "TEST_PROVIDER_EMPTY_ENV" || !strings.Contains(string(data), p.APIKeyEnv+"=new-key") {
		t.Fatal("saved credentials missing new isolated reference")
	}
}

func TestSetProviderKeyDoesNotWarnWhenEmptyProjectEnvAlsoDefinesSavedKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("TEST_PROVIDER_EMPTY_PROJECT=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_PROVIDER_EMPTY_PROJECT", "")
	os.Unsetenv("TEST_PROVIDER_EMPTY_PROJECT")

	app := &App{
		tabs:        map[string]*WorkspaceTab{"project": {ID: "project", WorkspaceRoot: project}},
		activeTabID: "project",
	}
	seedProviderCredentialReference(t, app, "TEST_PROVIDER_EMPTY_PROJECT")
	warning, err := app.SetProviderKey("TEST_PROVIDER_EMPTY_PROJECT", "new-key")
	if err != nil {
		t.Fatalf("SetProviderKey: %v", err)
	}
	if warning != "" {
		t.Fatalf("SetProviderKey warning = %q, want no warning because provider keys use global credentials only", warning)
	}
}

func seedProviderCredentialReference(t *testing.T, app *App, env string) {
	t.Helper()
	if err := app.SaveProvider(ProviderView{Name: "credential-test", Kind: "openai", BaseURL: "https://example.invalid/v1", Models: []string{"model"}, APIKeyEnv: env}); err != nil {
		t.Fatal(err)
	}
}

func TestFetchProviderModelCatalogFiltersNonChatModels(t *testing.T) {
	isolateDesktopUserDirs(t)
	if _, err := config.SetCredential("TEST_PROVIDER_KEY", "test-key"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]string{
				{"id": "mimo-v2.5-pro", "object": "model"},
				{"id": "mimo-v2.5-asr", "object": "model"},
				{"id": "mimo-v2.5-tts", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	catalog, err := NewApp().FetchProviderModelCatalog(ProviderView{
		Name:      "mimo-api",
		Kind:      "openai",
		BaseURL:   srv.URL,
		APIKeyEnv: "TEST_PROVIDER_KEY",
	})
	if err != nil {
		t.Fatalf("FetchProviderModelCatalog: %v", err)
	}
	got := make([]string, len(catalog))
	for i, item := range catalog {
		got[i] = item.Model
	}
	want := []string{"mimo-v2.5-pro"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FetchProviderModelCatalog = %v, want %v", got, want)
	}
}

func TestFetchProviderModelCatalogUsesSavedCredentialBeforeEnvironment(t *testing.T) {
	isolateDesktopUserDirs(t)
	const keyEnv = "TEST_PROVIDER_FETCH_KEY"
	if _, err := config.SetCredential(keyEnv, "saved-key"); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	t.Setenv(keyEnv, "stale-env-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer saved-key" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]string{
				{"id": "model-a", "object": "model"},
			},
		})
	}))
	defer srv.Close()

	catalog, err := NewApp().FetchProviderModelCatalog(ProviderView{
		Name:      "custom",
		BaseURL:   srv.URL,
		APIKeyEnv: keyEnv,
	})
	if err != nil {
		t.Fatalf("FetchProviderModelCatalog: %v", err)
	}
	got := make([]string, len(catalog))
	for i, item := range catalog {
		got[i] = item.Model
	}
	if want := []string{"model-a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FetchProviderModelCatalog = %v, want %v", got, want)
	}
	if got := os.Getenv(keyEnv); got != "stale-env-key" {
		t.Fatalf("process env = %q, want stale env left untouched", got)
	}
}

func TestFetchAllProviderModelCatalogsOmitsFailuresWithoutJSONNulls(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]string{{"id": "model-a", "object": "model"}},
		})
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"temporary"}`, http.StatusServiceUnavailable)
	}))
	defer bad.Close()

	got := NewApp().FetchAllProviderModelCatalogs([]ProviderView{
		{Name: "good", Kind: "openai", BaseURL: good.URL},
		{Name: "bad", Kind: "openai", BaseURL: bad.URL},
	})
	if len(got["good"]) != 1 || got["good"][0].Model != "model-a" {
		t.Fatalf("good provider catalog = %v", got["good"])
	}
	if _, ok := got["bad"]; ok {
		t.Fatalf("failed provider unexpectedly present: %#v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal batch result: %v", err)
	}
	if strings.Contains(string(raw), "null") {
		t.Fatalf("batch result contains JSON null: %s", raw)
	}
}

func TestSaveProviderFiltersNonChatModels(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name:           "mimo-api",
		Kind:           "openai",
		BaseURL:        "https://api.xiaomimimo.com/v1",
		Models:         []string{"mimo-v2.5-asr", "mimo-v2.5-pro", "mimo-v2.5-tts"},
		ModelOverrides: []ProviderModelOverrideView{{Model: "mimo-v2.5-asr", Vision: new(true)}, {Model: "mimo-v2.5-pro", Vision: new(true)}, {Model: "mimo-v2.5-tts", Vision: new(true)}},

		Default:   "mimo-v2.5-asr",
		APIKeyEnv: "MIMO_API_KEY",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("mimo-api")
	if !ok {
		t.Fatal("saved provider not found")
	}
	want := []string{"mimo-v2.5-pro"}
	if !reflect.DeepEqual(got.ModelList(), want) {
		t.Errorf("saved provider models = %v, want %v", got.ModelList(), want)
	}
	if got.DefaultModel() != "mimo-v2.5-pro" {
		t.Errorf("saved provider default = %q, want mimo-v2.5-pro", got.DefaultModel())
	}
	if ov := got.ModelOverrides["mimo-v2.5-pro"]; ov.Vision == nil || !*ov.Vision || len(got.ModelOverrides) != 1 {
		t.Fatalf("filtered per-model overrides = %+v", got.ModelOverrides)
	}
	raw, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	saved := string(raw)
	blockStart := strings.Index(saved, "\n[[providers]]\nname        = \"mimo-api\"")
	if blockStart < 0 {
		t.Fatalf("saved config missing mimo-api provider block:\n%s", raw)
	}
	block := saved[blockStart:]
	if next := strings.Index(block[len("\n[[providers]]"):], "\n[[providers]]"); next >= 0 {
		block = block[:len("\n[[providers]]")+next]
	}
	if !strings.Contains(block, `models      = ["mimo-v2.5-pro"]`) {
		t.Fatalf("saved provider block did not persist single selection as models array:\n%s", block)
	}
	if strings.Contains(block, `model       = "mimo-v2.5-pro"`) {
		t.Fatalf("saved provider block should not persist explicit single selection as legacy model:\n%s", block)
	}
	if strings.Contains(block, "vision_models") || !strings.Contains(block, "vision = true") {
		t.Fatalf("saved provider block did not persist current per-model capability:\n%s", block)
	}
}

func TestSaveProviderModelCatalogsPersistsFreshBatchAtomically(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	providers := []ProviderView{
		{Name: "batch-a", Kind: "openai", BaseURL: "https://a.example.com/v1", Models: []string{"model-a"}, APIKeyEnv: "BATCH_A_API_KEY", Headers: map[string]string{"X-Tenant": "a"}},
		{Name: "batch-b", Kind: "openai", BaseURL: "https://b.example.com/v1", Models: []string{"model-b"}, APIKeyEnv: "BATCH_B_API_KEY"},
	}
	for _, provider := range providers {
		if err := app.SaveProvider(provider); err != nil {
			t.Fatalf("SaveProvider(%s): %v", provider.Name, err)
		}
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	a, _ := cfg.Provider("batch-a")
	b, _ := cfg.Provider("batch-b")
	updates := []ProviderModelCatalogUpdate{
		{Name: "batch-a", ExpectedFingerprint: providerModelCatalogFingerprint(*a), Models: []string{"model-a", "model-a-new"}, Default: "model-a-new"},
		{Name: "batch-b", ExpectedFingerprint: providerModelCatalogFingerprint(*b), Models: []string{"model-b", "model-b-new"}, Default: "model-b-new"},
	}
	applied, err := app.SaveProviderModelCatalogs(updates)
	if err != nil {
		t.Fatalf("SaveProviderModelCatalogs: %v", err)
	}
	if !reflect.DeepEqual(applied, []string{"batch-a", "batch-b"}) {
		t.Fatalf("applied = %v, want both providers", applied)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	a, _ = cfg.Provider("batch-a")
	b, _ = cfg.Provider("batch-b")
	if a.DefaultModel() != "model-a-new" || b.DefaultModel() != "model-b-new" {
		t.Fatalf("catalog defaults = %q/%q, want model-a-new/model-b-new", a.DefaultModel(), b.DefaultModel())
	}
	if a.BaseURL != providers[0].BaseURL || a.APIKeyEnv != providers[0].APIKeyEnv || a.Headers["X-Tenant"] != "a" {
		t.Fatalf("narrow catalog update changed provider identity: %+v", *a)
	}

	aFingerprint := providerModelCatalogFingerprint(*a)
	bFingerprint := providerModelCatalogFingerprint(*b)
	if _, err := app.SaveProviderModelCatalogs([]ProviderModelCatalogUpdate{
		{Name: "batch-a", ExpectedFingerprint: aFingerprint, Models: []string{"must-not-persist"}},
		{Name: "batch-b", ExpectedFingerprint: bFingerprint, Models: []string{"text-embedding-3-small"}},
	}); err == nil {
		t.Fatal("SaveProviderModelCatalogs invalid batch returned nil error")
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	a, _ = cfg.Provider("batch-a")
	if a.DefaultModel() == "must-not-persist" {
		t.Fatal("SaveProviderModelCatalogs persisted a partial invalid batch")
	}
}

func TestSaveProviderModelCatalogsRejectsStaleCompletion(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name: "race-provider", Kind: "openai", BaseURL: "https://old.example.com/v1",
		Models: []string{"old-model"}, Default: "old-model", APIKeyEnv: "OLD_API_KEY",
		Headers: map[string]string{"X-Version": "old"},
	}); err != nil {
		t.Fatalf("SaveProvider(old): %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	old, _ := cfg.Provider("race-provider")
	oldFingerprint := providerModelCatalogFingerprint(*old)

	started := make(chan struct{})
	release := make(chan struct{})
	type result struct {
		applied []string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		close(started)
		<-release
		applied, err := app.SaveProviderModelCatalogs([]ProviderModelCatalogUpdate{{
			Name: "race-provider", ExpectedFingerprint: oldFingerprint,
			Models: []string{"old-model", "stale-fetched-model"}, Default: "stale-fetched-model",
		}})
		done <- result{applied: applied, err: err}
	}()
	<-started

	if err := app.SaveProvider(ProviderView{
		Name: "race-provider", Kind: "openai", BaseURL: "https://new.example.com/v1",
		Models: []string{"new-model"}, Default: "new-model", APIKeyEnv: "NEW_API_KEY",
		Headers: map[string]string{"X-Version": "new"},
	}); err != nil {
		t.Fatalf("SaveProvider(new): %v", err)
	}
	close(release)
	gotResult := <-done
	if gotResult.err != nil {
		t.Fatalf("stale SaveProviderModelCatalogs: %v", gotResult.err)
	}
	if len(gotResult.applied) != 0 {
		t.Fatalf("stale update applied providers %v, want none", gotResult.applied)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	got, _ := cfg.Provider("race-provider")
	if got.BaseURL != "https://new.example.com/v1" || got.APIKeyEnv != "NEW_API_KEY" || got.Headers["X-Version"] != "new" {
		t.Fatalf("stale completion overwrote provider identity: %+v", *got)
	}
	if !reflect.DeepEqual(got.ChatModelList(), []string{"new-model"}) || got.DefaultModel() != "new-model" {
		t.Fatalf("stale completion overwrote model selection: models=%v default=%q", got.ChatModelList(), got.DefaultModel())
	}
}

func TestSaveProviderModelCatalogsRejectsStaleCredentialSnapshot(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name: "credential-race", Kind: "openai", BaseURL: "https://credential.example.com/v1",
		Models: []string{"current-model"}, APIKeyEnv: "CREDENTIAL_RACE_API_KEY",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if _, err := app.SaveProviderKey("CREDENTIAL_RACE_API_KEY", "old-key"); err != nil {
		t.Fatalf("SaveProviderKey(old): %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	provider, _ := cfg.Provider("credential-race")
	oldFingerprint := providerModelCatalogFingerprint(*provider)

	if _, err := app.SetConnectionKey("credential-race", "new-key-with-different-length"); err != nil {
		t.Fatalf("SaveProviderKey(new): %v", err)
	}
	applied, err := app.SaveProviderModelCatalogs([]ProviderModelCatalogUpdate{{
		Name: "credential-race", ExpectedFingerprint: oldFingerprint,
		Models: []string{"current-model", "stale-key-model"}, Default: "stale-key-model",
	}})
	if err != nil {
		t.Fatalf("SaveProviderModelCatalogs: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("credential-stale update applied providers %v, want none", applied)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	provider, _ = cfg.Provider("credential-race")
	if !reflect.DeepEqual(provider.ChatModelList(), []string{"current-model"}) {
		t.Fatalf("credential-stale update overwrote models: %v", provider.ChatModelList())
	}
}

func TestSaveProviderModelCatalogsRejectsOverlappingCredentialRotation(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	const keyEnv = "CREDENTIAL_OVERLAP_API_KEY"
	if err := app.SaveProvider(ProviderView{
		Name: "credential-overlap", Kind: "openai", BaseURL: "https://credential.example.com/v1",
		Models: []string{"current-model"}, APIKeyEnv: keyEnv,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}
	if _, err := app.SaveProviderKey(keyEnv, "old-key"); err != nil {
		t.Fatalf("SaveProviderKey(old): %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	provider, _ := cfg.Provider("credential-overlap")
	oldFingerprint := providerModelCatalogFingerprint(*provider)

	snapshotRead := make(chan struct{})
	releaseApply := make(chan struct{})
	app.providerCatalogBeforeCredentialLockHook = func(string) {
		close(snapshotRead)
		<-releaseApply
	}
	type result struct {
		applied []string
		err     error
	}
	catalogDone := make(chan result, 1)
	go func() {
		applied, err := app.SaveProviderModelCatalogs([]ProviderModelCatalogUpdate{{
			Name: "credential-overlap", ExpectedFingerprint: oldFingerprint,
			Models: []string{"current-model", "stale-key-model"}, Default: "stale-key-model",
		}})
		catalogDone <- result{applied: applied, err: err}
	}()
	<-snapshotRead

	// Keep the replacement the same length as the old value: revision safety
	// must come from credential contents and locking, not size or mtime luck.
	if _, err := config.SetCredential(provider.APIKeyEnv, "new-key"); err != nil {
		close(releaseApply)
		t.Fatalf("external credential rotation: %v", err)
	}
	close(releaseApply)
	gotResult := <-catalogDone
	if gotResult.err != nil {
		t.Fatalf("SaveProviderModelCatalogs: %v", gotResult.err)
	}
	if len(gotResult.applied) != 0 {
		t.Fatalf("credential-stale update applied providers %v, want none", gotResult.applied)
	}

	cfg = config.LoadForEdit(config.UserConfigPath())
	provider, _ = cfg.Provider("credential-overlap")
	if !reflect.DeepEqual(provider.ChatModelList(), []string{"current-model"}) {
		t.Fatalf("overlapping credential rotation persisted stale models: %v", provider.ChatModelList())
	}
}

func TestSaveProviderPersistsThinkingOverride(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name:      "glm-proxy",
		Kind:      "openai",
		BaseURL:   "https://proxy.example.com/v1",
		Models:    []string{"glm-4.5-air"},
		APIKeyEnv: "GLM_PROXY_API_KEY",
		Thinking:  "DISABLED",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("glm-proxy")
	if !ok {
		t.Fatal("saved provider not found")
	}
	if got.Thinking != "disabled" {
		t.Fatalf("saved provider thinking = %q, want disabled", got.Thinking)
	}
}

func TestSaveProviderPersistsAuthHeader(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name:       "minimax-global-anthropic",
		Kind:       "anthropic",
		BaseURL:    "https://api.minimax.io/anthropic",
		Models:     []string{"MiniMax-M3"},
		APIKeyEnv:  "MINIMAX_API_KEY",
		AuthHeader: true,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("minimax-global-anthropic")
	if !ok {
		t.Fatal("saved provider not found")
	}
	if !got.AuthHeader {
		t.Fatal("saved provider auth_header = false, want true")
	}
	view := providerViewFromEntry(*got, false, true)
	if !view.AuthHeader {
		t.Fatal("provider view authHeader = false, want true")
	}
}

func TestSaveProviderPersistsAndMirrorsCustomEndpointURLs(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SaveProvider(ProviderView{
		Name:       "sub2api",
		Kind:       "openai",
		BaseURL:    "https://proxy.example.com/v1",
		RequestURL: " https://proxy.example.com/custom/chat/completions/?token=1 ",
		ModelsURL:  " https://proxy.example.com/v1/models ",
		Models:     []string{"model-a"},
		Default:    "model-a",
		APIKeyEnv:  "SUB2API_KEY",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("sub2api")
	if !ok {
		t.Fatal("saved provider not found")
	}
	if got.RequestURL != "https://proxy.example.com/custom/chat/completions/?token=1" {
		t.Fatalf("saved chat_url = %q", got.RequestURL)
	}
	if got.RequestURL != "https://proxy.example.com/custom/chat/completions/?token=1" {
		t.Fatalf("saved request_url = %q", got.RequestURL)
	}
	if got.ModelsURL != "https://proxy.example.com/v1/models" {
		t.Fatalf("saved models_url = %q", got.ModelsURL)
	}

	view := app.Settings()
	for _, provider := range view.Providers {
		if provider.Name != "sub2api" {
			continue
		}
		if provider.RequestURL != "https://proxy.example.com/custom/chat/completions/?token=1" {
			t.Fatalf("Settings chatUrl = %q", provider.RequestURL)
		}
		if provider.RequestURL != "https://proxy.example.com/custom/chat/completions/?token=1" {
			t.Fatalf("Settings requestUrl = %q", provider.RequestURL)
		}
		if provider.ModelsURL != "https://proxy.example.com/v1/models" {
			t.Fatalf("Settings modelsUrl = %q", provider.ModelsURL)
		}
		return
	}
	t.Fatalf("Settings providers missing sub2api: %+v", view.Providers)
}

func TestSaveProviderPreservesHiddenProviderFields(t *testing.T) {
	isolateDesktopUserDirs(t)

	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Providers = []config.ProviderEntry{{
		Name:      "custom",
		Kind:      "openai",
		BaseURL:   "https://proxy.example.com/v1",
		Models:    []string{"model-a", "model-b"},
		Default:   "model-a",
		APIKeyEnv: "CUSTOM_API_KEY",

		Prices:        map[string]*provider.Pricing{"model-a": {Input: 1, Output: 2, Currency: "$"}, "model-b": {Input: 3, Output: 4, Currency: "$"}},
		Thinking:      "adaptive",
		DefaultEffort: "high",
		VisionDetail:  "low",
		ExtraBody:     map[string]any{"enable_thinking": true},
		NoProxy:       true,
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	app := NewApp()
	settings := app.Settings()
	var view ProviderView
	found := false
	for _, p := range settings.Providers {
		if p.Name == "custom" {
			view = p
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Settings providers missing custom: %+v", settings.Providers)
	}
	if view.ExtraBody["enable_thinking"] != true {
		t.Fatalf("settings extra_body = %+v, want enable_thinking=true", view.ExtraBody)
	}

	if err := app.SaveProvider(view); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	gotCfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := gotCfg.Provider("custom")
	if !ok {
		t.Fatal("saved provider not found")
	}
	if got.Prices["model-a"] == nil || got.Prices["model-a"].Input != 1 || got.Prices["model-a"].Output != 2 || got.Prices["model-a"].Currency != "$" {
		t.Fatalf("provider-wide price = %+v, want preserved", got.Prices["model-a"])
	}
	if got.Prices["model-b"] == nil || got.Prices["model-b"].Input != 3 || got.Prices["model-b"].Output != 4 || got.Prices["model-b"].Currency != "$" {
		t.Fatalf("per-model prices = %+v, want model-b price preserved", got.Prices)
	}
	if got.Thinking != "adaptive" || got.DefaultEffort != "high" {
		t.Fatalf("thinking/effort = %q/%q, want adaptive/high", got.Thinking, got.DefaultEffort)
	}
	if got.VisionDetail != "low" {
		t.Fatalf("vision_detail = %q, want low", got.VisionDetail)
	}
	if got.ExtraBody["enable_thinking"] != true {
		t.Fatalf("extra_body = %+v, want enable_thinking=true", got.ExtraBody)
	}
	if !got.NoProxy {
		t.Fatal("no_proxy = false, want preserved true")
	}
}

func TestSaveProviderPersistsWebSearchOn(t *testing.T) {
	isolateDesktopUserDirs(t)

	if err := NewApp().SaveProvider(ProviderView{
		Name:      "deepseek-responses",
		Kind:      "responses",
		BaseURL:   "https://api.deepseek.com",
		Models:    []string{"deepseek-v4-flash"},
		Default:   "deepseek-v4-flash",
		WebSearch: true,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("deepseek-responses")
	if !ok || got.WebSearch == nil || !*got.WebSearch {
		t.Fatalf("saved provider = %+v, found=%v; want web_search=true", got, ok)
	}
	raw, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(raw), "web_search  = true") {
		t.Fatalf("saved config did not persist web_search:\n%s", raw)
	}
}

func TestSaveProviderPersistsExplicitWebSearchOff(t *testing.T) {
	isolateDesktopUserDirs(t)

	if err := NewApp().SaveProvider(ProviderView{
		Name:      "deepseek-responses",
		Kind:      "responses",
		BaseURL:   "https://api.deepseek.com",
		Models:    []string{"deepseek-v4-flash"},
		Default:   "deepseek-v4-flash",
		WebSearch: false,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := cfg.Provider("deepseek-responses")
	if !ok || got.WebSearch == nil || *got.WebSearch || config.EffectiveWebSearch(got) {
		t.Fatalf("saved provider = %+v, found=%v; want explicit web_search=false", got, ok)
	}
	raw, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(raw), "web_search  = false") {
		t.Fatalf("saved config did not persist web_search=false:\n%s", raw)
	}
}

func TestSetProviderWebSearchUpdatesGroupedDeepSeekAliasesAtomically(t *testing.T) {
	isolateDesktopUserDirs(t)

	enabled := true
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "deepseek-pro"}
	cfg.Providers = []config.ProviderEntry{
		{
			Name: "deepseek-flash", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
			Models: []string{"deepseek-v4-flash"}, Headers: map[string]string{"X-Route": "flash"}, WebSearch: &enabled,
		},
		{
			Name: "deepseek-pro", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
			Models: []string{"deepseek-v4-pro"}, Headers: map[string]string{"X-Route": "pro"}, WebSearch: &enabled,
		},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	if err := NewApp().SetProviderWebSearch([]string{"deepseek-flash", "deepseek-pro", "deepseek-flash"}, false); err != nil {
		t.Fatalf("SetProviderWebSearch: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	for _, name := range []string{"deepseek-flash", "deepseek-pro"} {
		entry, ok := got.Provider(name)
		if !ok || entry.WebSearch == nil || *entry.WebSearch {
			t.Fatalf("provider %q = %+v, found=%v; want explicit web_search=false", name, entry, ok)
		}
	}
	if flash, _ := got.Provider("deepseek-flash"); flash.Headers["X-Route"] != "flash" {
		t.Fatalf("Flash custom transport fields changed: %+v", flash)
	}
	if pro, _ := got.Provider("deepseek-pro"); pro.Headers["X-Route"] != "pro" {
		t.Fatalf("Pro custom transport fields changed: %+v", pro)
	}
}

func TestSetProviderWebSearchRejectsWholeGroupBeforeWriting(t *testing.T) {
	isolateDesktopUserDirs(t)

	enabled := true
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Providers = []config.ProviderEntry{
		{Name: "deepseek", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic", Models: []string{"deepseek-v4-flash"}, WebSearch: &enabled},
		{Name: "proxy", Kind: "anthropic", BaseURL: "https://gateway.example/anthropic", Models: []string{"custom-model"}, WebSearch: &enabled},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	if err := NewApp().SetProviderWebSearch([]string{"deepseek", "proxy"}, false); err == nil {
		t.Fatal("SetProviderWebSearch accepted an unverified endpoint")
	}

	got := config.LoadForEdit(config.UserConfigPath())
	entry, ok := got.Provider("deepseek")
	if !ok || entry.WebSearch == nil || !*entry.WebSearch {
		t.Fatalf("official provider was partially updated after group rejection: %+v, found=%v", entry, ok)
	}
}

func TestSetProviderWebSearchPreservesEveryVisibleRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	enabled := true
	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-v4-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek"}
	cfg.Providers = []config.ProviderEntry{{
		Name: "deepseek", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
		Models: []string{"deepseek-v4-flash", "deepseek-v4-pro"}, Default: "deepseek-v4-flash",
		APIKeyEnv: "DEEPSEEK_API_KEY", WebSearch: &enabled,
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	newTab := func(id string) (*WorkspaceTab, *blockingSnapshotCtrl) {
		old := newBlockingSnapshotCtrl(control.New(control.Options{Label: cfg.DefaultModel, Sink: event.Discard}))
		close(old.releaseSnapshot)
		tab := &WorkspaceTab{
			ID: id, Scope: "global", Ready: true, Ctrl: old,
			model: cfg.DefaultModel, Label: cfg.DefaultModel,
			sink: &tabEventSink{tabID: id, app: app}, disabledMCP: map[string]ServerView{},
		}
		return tab, old
	}
	first, oldFirst := newTab("first")
	second, oldSecond := newTab("second")
	app.tabs = map[string]*WorkspaceTab{first.ID: first, second.ID: second}
	app.tabOrder = []string{first.ID, second.ID}
	app.activeTabID = first.ID
	t.Cleanup(func() {
		for _, tab := range []*WorkspaceTab{first, second} {
			if tab.Ctrl != nil {
				tab.Ctrl.Close()
			}
		}
	})

	if err := app.SetProviderWebSearch([]string{"deepseek"}, false); err != nil {
		t.Fatalf("SetProviderWebSearch: %v", err)
	}
	if first.Ctrl != oldFirst || second.Ctrl != oldSecond || oldFirst.closeCount.Load() != 0 || oldSecond.closeCount.Load() != 0 {
		t.Fatal("saving web-search capability replaced a visible runtime")
	}
	got := config.LoadForEdit(config.UserConfigPath())
	provider, ok := got.Provider("deepseek")
	if !ok || provider.WebSearch == nil || *provider.WebSearch {
		t.Fatalf("persisted DeepSeek web_search = %+v, want false", provider)
	}
}

func TestSetProviderWebSearchSavesWithDetachedRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	enabled := true
	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-v4-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"deepseek-v4-flash"}, Default: "deepseek-v4-flash",
		Name: "deepseek", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
		Model: "deepseek-v4-flash", APIKeyEnv: "DEEPSEEK_API_KEY", WebSearch: &enabled,
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	detachedCtrl := control.New(control.Options{Label: cfg.DefaultModel, Sink: event.Discard})
	detached := &WorkspaceTab{ID: "detached", Scope: "global", Ctrl: detachedCtrl, model: cfg.DefaultModel}
	app.detachedSessions = map[string]*WorkspaceTab{detached.ID: detached}
	t.Cleanup(detachedCtrl.Close)

	err := app.SetProviderWebSearch([]string{"deepseek"}, false)
	if err != nil || detached.Ctrl != detachedCtrl {
		t.Fatalf("SetProviderWebSearch must preserve detached runtime: %v", err)
	}
	got := config.LoadForEdit(config.UserConfigPath())
	provider, ok := got.Provider("deepseek")
	if !ok || provider.WebSearch == nil || *provider.WebSearch {
		t.Fatalf("web-search mutation was not saved: %+v", provider)
	}
}

func TestSaveProviderPreservesHiddenCustomWebSearchOverride(t *testing.T) {
	isolateDesktopUserDirs(t)

	enabled := true
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Providers = []config.ProviderEntry{{
		Name:      "custom-anthropic",
		Kind:      "anthropic",
		BaseURL:   "https://gateway.example/anthropic",
		Models:    []string{"custom-model"},
		WebSearch: &enabled,
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	if err := NewApp().SaveProvider(ProviderView{
		Name:          "custom-anthropic",
		Kind:          "anthropic",
		BaseURL:       "https://gateway.example/anthropic",
		Models:        []string{"custom-model"},
		Default:       "custom-model",
		ContextWindow: 200_000,
		WebSearch:     false, // The hidden Settings control must not overwrite advanced TOML.
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	gotCfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := gotCfg.Provider("custom-anthropic")
	if !ok || got.WebSearch == nil || !*got.WebSearch || !config.EffectiveWebSearch(got) {
		t.Fatalf("saved provider = %+v, found=%v; want preserved advanced web_search=true", got, ok)
	}
}

func TestSaveProviderDoesNotCarryOfficialWebSearchToCustomEndpoint(t *testing.T) {
	isolateDesktopUserDirs(t)

	enabled := true
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Providers = []config.ProviderEntry{{
		Name:      "deepseek-customized",
		Kind:      "anthropic",
		BaseURL:   "https://api.deepseek.com/anthropic",
		Models:    []string{"deepseek-v4-flash"},
		WebSearch: &enabled,
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	if err := NewApp().SaveProvider(ProviderView{
		Name:      "deepseek-customized",
		Kind:      "anthropic",
		BaseURL:   "https://gateway.example/anthropic",
		Models:    []string{"deepseek-v4-flash"},
		Default:   "deepseek-v4-flash",
		WebSearch: false,
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	gotCfg := config.LoadForEdit(config.UserConfigPath())
	got, ok := gotCfg.Provider("deepseek-customized")
	if !ok || got.WebSearch != nil || config.EffectiveWebSearch(got) {
		t.Fatalf("saved provider = %+v, found=%v; want official web search cleared after endpoint change", got, ok)
	}
}

func TestProviderModelOverrideViewPreservesMaxOutputTokens(t *testing.T) {
	input := map[string]config.ProviderModelOverride{
		"limited": {ContextWindow: 64_000, MaxOutputTokens: 8_192},
		"omitted": {MaxOutputTokens: -1},
	}
	views := providerModelOverridesForView(input, []string{"limited", "omitted"})
	if len(views) != 2 || views[0].MaxOutputTokens != 8_192 || views[1].MaxOutputTokens != -1 {
		t.Fatalf("model override views = %+v, want positive and negative output-token semantics preserved", views)
	}
	roundTrip := providerModelOverridesForSave(views, []string{"limited", "omitted"})
	if got := roundTrip["limited"]; got.ContextWindow != 64_000 || got.MaxOutputTokens != 8_192 {
		t.Fatalf("limited override = %+v, want context and output limits preserved", got)
	}
	if got := roundTrip["omitted"]; got.MaxOutputTokens != -1 {
		t.Fatalf("omitted override = %+v, want negative wire-omission marker preserved", got)
	}
}

func TestOfficialMimoAPITemplateRemoved(t *testing.T) {
	if entries, keyEnv, err := officialProviderTemplate("mimo-api"); err == nil {
		t.Fatalf("officialProviderTemplate(mimo-api) = entries=%v key=%q nil error, want unknown template", entries, keyEnv)
	}
}

func TestOfficialDeepSeekTemplateUsesRegionalPricing(t *testing.T) {
	// Language no longer selects list-price tables; templates freeze the default
	// USD official rates. Display currency is independent (billing.display_currency).
	for _, language := range []string{"en", "zh"} {
		entries, keyEnv, err := officialProviderTemplate("deepseek")
		if err != nil {
			t.Fatalf("officialProviderTemplate(%s): %v", language, err)
		}
		if keyEnv != "DEEPSEEK_API_KEY" || len(entries) != 1 {
			t.Fatalf("template = %v/%q, want one DEEPSEEK_API_KEY entry", entries, keyEnv)
		}
		got := entries[0]
		if got.Kind != "openai" || got.BaseURL != "https://api.deepseek.com" || !config.EffectiveIndependentWebSearch(&got) || got.Thinking != "enabled" {
			t.Fatalf("%s DeepSeek template = kind:%q base_url:%q web_search:%t thinking:%q, want Chat Completions with independent web search", language, got.Kind, got.BaseURL, config.EffectiveIndependentWebSearch(&got), got.Thinking)
		}
		if price := got.Prices["deepseek-v4-flash"]; price == nil || price.Currency != "$" || price.Output != 1.2 {
			t.Fatalf("%s deepseek-v4-flash price = %+v, want frozen USD table", language, price)
		}
		if price := got.Prices["deepseek-v4-pro"]; price == nil || price.Currency != "$" || price.Output != 3.96 {
			t.Fatalf("%s deepseek-v4-pro price = %+v, want frozen USD table", language, price)
		}
		if price := got.Prices[openai.OfficialDeepSeekVisionModel]; price == nil || price.Currency != "$" || price.Output != 1.2 {
			t.Fatalf("%s vision SKU price = %+v, want Flash USD table", language, price)
		}
	}
}

func TestSetAgentParamsPersistsCurrentParameters(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SetAgentParams(0.35, "custom system"); err != nil {
		t.Fatalf("SetAgentParams: %v", err)
	}

	view := app.Settings()
	if view.Agent.Temperature != 0.35 || view.Agent.SystemPrompt != "custom system" {
		t.Fatalf("Settings().Agent did not preserve other agent params: %+v", view.Agent)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.Temperature != 0.35 || cfg.Agent.SystemPrompt != "custom system" {
		t.Fatalf("saved config did not preserve other agent params: %+v", cfg.Agent)
	}
}

func TestSetReasoningLanguagePersistsToUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SetReasoningLanguage("zh"); err != nil {
		t.Fatalf("SetReasoningLanguage: %v", err)
	}

	view := app.Settings()
	if view.Agent.ReasoningLanguage != "zh" {
		t.Fatalf("Settings().Agent.ReasoningLanguage = %q, want zh", view.Agent.ReasoningLanguage)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.ReasoningLanguage != "zh" || cfg.ReasoningLanguage() != "zh" {
		t.Fatalf("saved reasoning language = %q/%q, want zh", cfg.Agent.ReasoningLanguage, cfg.ReasoningLanguage())
	}
}

func TestSetCompactRatioPersistsToUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	defaultView := app.Settings()
	if defaultView.Agent.CompactRatio != 0.80 || defaultView.Agent.EffectiveCompactRatio != 0.80 {
		t.Fatalf("default compact ratios = %v/%v, want 0.80/0.80", defaultView.Agent.CompactRatio, defaultView.Agent.EffectiveCompactRatio)
	}
	if err := app.SetCompactRatio(0.7); err != nil {
		t.Fatalf("SetCompactRatio: %v", err)
	}

	view := app.Settings()
	if view.Agent.CompactRatio != 0.7 {
		t.Fatalf("Settings().Agent.CompactRatio = %v, want 0.7", view.Agent.CompactRatio)
	}

	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.CompactRatio != 0.7 {
		t.Fatalf("saved compact ratio = %v, want 0.7", cfg.Agent.CompactRatio)
	}
	if err := app.SetCompactRatio(0.3); err != nil {
		t.Fatalf("SetCompactRatio lower bound: %v", err)
	}
	view = app.Settings()
	if view.Agent.CompactRatio != 0.3 {
		t.Fatalf("Settings().Agent.CompactRatio = %v, want 0.3", view.Agent.CompactRatio)
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.CompactRatio != 0.3 {
		t.Fatalf("saved compact ratio = %v, want 0.3", cfg.Agent.CompactRatio)
	}

	if err := app.SetCompactRatio(0.9); err == nil {
		t.Fatal("SetCompactRatio should reject values outside the Desktop safety range")
	}
	cfg = config.LoadForEdit(config.UserConfigPath())
	if cfg.Agent.CompactRatio != 0.3 {
		t.Fatalf("rejected update changed saved compact ratio to %v", cfg.Agent.CompactRatio)
	}
}

func TestSetCompactRatioRejectsActiveWorkBeforeSaving(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.setTestCtrl(newBackgroundJobController(t, "compact-ratio-job"), "")
	err := app.SetCompactRatio(0.7)
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetCompactRatio with background job error = %v, want active-work guard", err)
	}
	if got := config.LoadForEdit(config.UserConfigPath()).Agent.CompactRatio; got != 0.80 {
		t.Fatalf("compact ratio changed after rejected update: %v", got)
	}
}

func TestSetDesktopLanguagePersistsResponseLanguageAndUpdatesLiveTabs(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "deepzora.toml"), []byte("language = \"zh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, projectRoot)

	app := NewApp()
	userCtrl := control.New(control.Options{})
	projectCtrl := control.New(control.Options{})
	app.tabs = map[string]*WorkspaceTab{
		"user": {
			ID:          "user",
			Scope:       "global",
			Ctrl:        userCtrl,
			Ready:       true,
			disabledMCP: map[string]ServerView{},
		},
		"project": {
			ID:            "project",
			Scope:         "project",
			WorkspaceRoot: projectRoot,
			Ctrl:          projectCtrl,
			Ready:         true,
			disabledMCP:   map[string]ServerView{},
		},
	}
	app.activeTabID = "user"

	if err := app.SetDesktopLanguage("en"); err != nil {
		t.Fatalf("SetDesktopLanguage: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.DesktopLanguage() != "en" || cfg.Language != "en" {
		t.Fatalf("saved language prefs = desktop:%q response:%q, want en/en", cfg.DesktopLanguage(), cfg.Language)
	}
	got := userCtrl.Compose("解释这个函数")
	if !strings.Contains(got, "<response-language>") || !strings.Contains(got, "use English") {
		t.Fatalf("live controller Compose = %q, want English response language", got)
	}
	projectComposed := projectCtrl.Compose("explain this function")
	if !strings.Contains(projectComposed, "use Simplified Chinese") {
		t.Fatalf("project controller Compose = %q, want project zh response language", projectComposed)
	}
}

func TestSetDesktopCurrencyPersistsDisplayWithoutRewritingOfficialPricing(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SetDesktopCurrency("CNY"); err != nil {
		t.Fatalf("SetDesktopCurrency: %v", err)
	}

	view := app.Settings()
	if view.DesktopCurrency != "CNY" {
		t.Fatalf("Settings().DesktopCurrency = %q, want CNY", view.DesktopCurrency)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if got := cfg.DisplayCurrencyPref(); got != "CNY" {
		t.Fatalf("display pref = %q, want CNY", got)
	}
	flash, ok := cfg.Provider("deepseek-flash")
	// Display currency must not rewrite frozen list prices (default USD table).
	if !ok || flash.PriceForModel("deepseek-flash") == nil || flash.PriceForModel("deepseek-flash").Output != 1.2 || flash.PriceForModel("deepseek-flash").Currency != "$" {
		t.Fatalf("saved DeepSeek flash price = %+v, want frozen USD official price", flash)
	}
}

func TestSetReasoningLanguageUpdatesLiveTabControllers(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "deepzora.toml"), []byte("[agent]\nreasoning_language = \"en\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, projectRoot)

	app := NewApp()
	userCtrl := control.New(control.Options{ReasoningLanguage: "auto"})
	projectCtrl := control.New(control.Options{ReasoningLanguage: "auto"})
	app.tabs = map[string]*WorkspaceTab{
		"user": {
			ID:          "user",
			Scope:       "global",
			Ctrl:        userCtrl,
			Ready:       true,
			disabledMCP: map[string]ServerView{},
		},
		"project": {
			ID:            "project",
			Scope:         "project",
			WorkspaceRoot: projectRoot,
			Ctrl:          projectCtrl,
			Ready:         true,
			disabledMCP:   map[string]ServerView{},
		},
	}
	app.activeTabID = "user"

	if err := app.SetReasoningLanguage("zh"); err != nil {
		t.Fatalf("SetReasoningLanguage: %v", err)
	}

	userComposed := userCtrl.Compose("hi")
	if !strings.Contains(userComposed, "简体中文") {
		t.Fatalf("user-level tab Compose = %q, want zh reasoning language", userComposed)
	}
	projectComposed := projectCtrl.Compose("hi")
	if !strings.Contains(projectComposed, "use English") {
		t.Fatalf("project override tab Compose = %q, want en reasoning language", projectComposed)
	}
}

func TestSetReasoningLanguageRejectsBackgroundJobsBeforeSavingConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.setTestCtrl(newBackgroundJobController(t, "reasoning-language-job"), "")

	err := app.SetReasoningLanguage("zh")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetReasoningLanguage with background job error = %v, want active-work guard", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.ReasoningLanguage() != "auto" {
		t.Fatalf("reasoning language changed after rejected update: %q", cfg.ReasoningLanguage())
	}
}

func TestSetDesktopCheckUpdatesPersistsToUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if !app.Settings().CheckUpdates {
		t.Fatal("Settings().CheckUpdates default = false, want true")
	}
	if err := app.SetDesktopCheckUpdates(false); err != nil {
		t.Fatalf("SetDesktopCheckUpdates: %v", err)
	}
	view := app.Settings()
	if view.CheckUpdates {
		t.Fatal("Settings().CheckUpdates = true, want false")
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Desktop.CheckUpdates == nil || *cfg.Desktop.CheckUpdates {
		t.Fatalf("desktop.check_updates = %+v, want false", cfg.Desktop.CheckUpdates)
	}
	if cfg.DesktopCheckUpdates() {
		t.Fatal("DesktopCheckUpdates() = true, want false")
	}
}

func TestSetDesktopConversationWidthPersistsToUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if got := app.Settings().ConversationWidth; got != "standard" {
		t.Fatalf("Settings().ConversationWidth default = %q, want standard", got)
	}
	if got := app.DesktopStartupSettings().ConversationWidth; got != "standard" {
		t.Fatalf("DesktopStartupSettings().ConversationWidth default = %q, want standard", got)
	}
	if err := app.SetDesktopConversationWidth("full"); err != nil {
		t.Fatalf("SetDesktopConversationWidth: %v", err)
	}
	if got := app.Settings().ConversationWidth; got != "full" {
		t.Fatalf("Settings().ConversationWidth = %q, want full", got)
	}
	if got := app.DesktopStartupSettings().ConversationWidth; got != "full" {
		t.Fatalf("DesktopStartupSettings().ConversationWidth = %q, want full", got)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if got := cfg.DesktopConversationWidth(); got != "full" {
		t.Fatalf("persisted conversation width = %q, want full", got)
	}

	if err := app.SetDesktopConversationWidth("wide"); err == nil {
		t.Fatal("SetDesktopConversationWidth(wide) unexpectedly succeeded")
	}
	if got := config.LoadForEdit(config.UserConfigPath()).DesktopConversationWidth(); got != "full" {
		t.Fatalf("invalid update changed persisted conversation width to %q", got)
	}

	raw, err := json.Marshal(app.DesktopStartupSettings())
	if err != nil {
		t.Fatalf("marshal DesktopStartupSettings: %v", err)
	}
	if !strings.Contains(string(raw), `"conversationWidth":"full"`) {
		t.Fatalf("startup bridge payload omitted conversationWidth: %s", raw)
	}
}

func TestSetDefaultToolApprovalModePersistsToUserConfig(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if app.Settings().DefaultToolApprovalMode != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("Settings().DefaultToolApprovalMode = %q, want auto", app.Settings().DefaultToolApprovalMode)
	}
	if err := app.SetDefaultToolApprovalMode(control.ToolApprovalWorkspaceWrite); err != nil {
		t.Fatalf("SetDefaultToolApprovalMode: %v", err)
	}
	view := app.Settings()
	if view.DefaultToolApprovalMode != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("Settings().DefaultToolApprovalMode = %q, want auto", view.DefaultToolApprovalMode)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Desktop.DefaultToolApprovalMode != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("desktop.default_tool_approval_mode = %q, want auto", cfg.Desktop.DefaultToolApprovalMode)
	}
	if cfg.DesktopDefaultToolApprovalMode() != control.ToolApprovalWorkspaceWrite {
		t.Fatalf("DesktopDefaultToolApprovalMode() = %q, want auto", cfg.DesktopDefaultToolApprovalMode())
	}
}

func TestSetDesktopMetricsDefaultsOnAndPersistsOff(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if !app.Settings().Metrics {
		t.Fatal("Settings().Metrics default = false, want true")
	}
	if err := app.SetDesktopMetrics(false); err != nil {
		t.Fatalf("SetDesktopMetrics: %v", err)
	}
	view := app.Settings()
	if view.Metrics {
		t.Fatal("Settings().Metrics = true, want false")
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	if cfg.Desktop.Metrics == nil || *cfg.Desktop.Metrics {
		t.Fatalf("desktop.metrics = %+v, want false", cfg.Desktop.Metrics)
	}
	if cfg.DesktopMetrics() {
		t.Fatal("DesktopMetrics() = true, want false")
	}
}

func TestSaveHooksSettingsPreservesUnknownSettingsKeys(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := hook.GlobalSettingsPath("")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"theme":"dark","hooks":{"Stop":[{"command":"old"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.SaveHooksSettings("global", []HookConfigView{{
		Event:   string(hook.PreToolUse),
		Match:   "bash",
		Command: "echo guard",
	}}); err != nil {
		t.Fatalf("SaveHooksSettings: %v", err)
	}

	var raw map[string]json.RawMessage
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["theme"]) != `"dark"` {
		t.Fatalf("theme key was not preserved: %s", raw["theme"])
	}
	view := app.HooksSettings("global")
	if len(view.Hooks) != 1 || view.Hooks[0].Event != string(hook.PreToolUse) || view.Hooks[0].Command != "echo guard" {
		t.Fatalf("HooksSettings = %+v, want saved PreToolUse hook", view)
	}
}

func TestSaveHooksSettingsDecodesLegacyEncodedGlobalSettings(t *testing.T) {
	isolateDesktopUserDirs(t)
	path := hook.GlobalSettingsPath("")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"label":"中文","hooks":{"Stop":[{"command":"echo 旧"}]}}`
	if err := os.WriteFile(path, fileencoding.MustEncode(legacy, fileencoding.GB18030), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	before := app.HooksSettings("global")
	if len(before.Hooks) != 1 || before.Hooks[0].Command != "echo 旧" {
		t.Fatalf("HooksSettings before save = %+v, want decoded legacy hook", before.Hooks)
	}
	if err := app.SaveHooksSettings("global", []HookConfigView{{
		Event:   string(hook.PreToolUse),
		Command: "echo 新",
	}}); err != nil {
		t.Fatalf("SaveHooksSettings: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("saved settings should be valid UTF-8 JSON: %v", err)
	}
	if string(raw["label"]) != `"中文"` {
		t.Fatalf("label key was not preserved after decoding legacy settings: %s", raw["label"])
	}
	view := app.HooksSettings("global")
	if len(view.Hooks) != 1 || view.Hooks[0].Command != "echo 新" {
		t.Fatalf("HooksSettings after save = %+v, want new decoded hook", view.Hooks)
	}
}

func TestProjectHooksSettingsUseActiveWorkspaceRootAndLoadOnceSaved(t *testing.T) {
	isolateDesktopUserDirs(t)
	project := t.TempDir()
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"project": {ID: "project", Scope: "project", WorkspaceRoot: project, Ready: true},
	}
	app.activeTabID = "project"

	if err := app.SaveHooksSettings("project", []HookConfigView{{
		Event:       string(hook.Stop),
		Command:     "echo done",
		Description: "Turn done",
	}}); err != nil {
		t.Fatalf("SaveHooksSettings(project): %v", err)
	}
	view := app.HooksSettings("project")
	if view.Scope != "project" || view.ProjectRoot != project || !view.Trusted {
		t.Fatalf("project hook view metadata = %+v", view)
	}
	if len(view.Hooks) != 1 || view.Hooks[0].Event != string(hook.Stop) || view.Hooks[0].Description != "Turn done" {
		t.Fatalf("project hooks = %+v", view.Hooks)
	}
	if _, err := os.Stat(filepath.Join(project, ".deepzora", "settings.json")); err != nil {
		t.Fatalf("project hooks settings file missing: %v", err)
	}
	loaded := hook.Load(hook.LoadOptions{ProjectRoot: project})
	if len(loaded) != 1 || loaded[0].Scope != hook.ScopeProject || loaded[0].Event != hook.Stop {
		t.Fatalf("project hooks saved from the editor should load: %+v", loaded)
	}
}

func TestSaveHooksSettingsForRootUsesDisplayedProjectRoot(t *testing.T) {
	isolateDesktopUserDirs(t)
	projectA := t.TempDir()
	projectB := t.TempDir()
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"a": {ID: "a", Scope: "project", WorkspaceRoot: projectA, Ready: true},
		"b": {ID: "b", Scope: "project", WorkspaceRoot: projectB, Ready: true},
	}
	app.activeTabID = "b"

	if err := app.SaveHooksSettingsForRoot("project", projectA, []HookConfigView{{
		Event:   string(hook.Stop),
		Command: "echo done",
	}}); err != nil {
		t.Fatalf("SaveHooksSettingsForRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectA, ".deepzora", "settings.json")); err != nil {
		t.Fatalf("displayed project root settings missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projectB, ".deepzora", "settings.json")); err == nil {
		t.Fatal("active project root was written instead of displayed project root")
	}
}

// A checkout's deepzora.toml never becomes the user's config: not its sandbox,
// not its permissions, whether or not the user has a config yet.
func TestDesktopUserConfigNeverAdoptsTheWorkspaceFile(t *testing.T) {
	for name, userBody := range map[string]string{"no user config": "", "user config": "default_model = \"local/m1\"\n"} {
		t.Run(name, func(t *testing.T) {
			isolateDesktopUserDirs(t)
			userPath := config.UserConfigPath()
			if userBody != "" {
				if err := os.MkdirAll(filepath.Dir(userPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(userPath, []byte(userBody), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			// The marker must be host-independent: the renderer persists the
			// host-effective bash mode, and Windows resolves every value to "off".
			project := "[permissions]\nmode = \"allow\"\n[sandbox]\nnetwork = false\n"
			if err := os.WriteFile(filepath.Join(root, "deepzora.toml"), []byte(project), 0o644); err != nil {
				t.Fatal(err)
			}
			approveWorkspace(t, root)
			app := NewApp()
			app.tabs = map[string]*WorkspaceTab{"t": {ID: "t", Scope: "project", WorkspaceRoot: root, Ready: true}}
			app.activeTabID = "t"
			if cfg, _, err := app.loadDesktopUserConfigForView(); err != nil || cfg.Permissions.Mode == "allow" || !cfg.Sandbox.Network {
				t.Fatalf("view = %+v, %v; want the workspace's values left out", cfg.Permissions, err)
			}
			if err := app.applyConfigOnly(func(*config.Config) error { return nil }); err != nil {
				t.Fatalf("applyConfigOnly: %v", err)
			}
			saved := config.LoadForEditWithoutCredentials(userPath)
			if saved.Permissions.Mode == "allow" || !saved.Sandbox.Network {
				t.Fatalf("user config took the workspace's values: permissions = %+v sandbox = %+v", saved.Permissions, saved.Sandbox)
			}
		})
	}
}
