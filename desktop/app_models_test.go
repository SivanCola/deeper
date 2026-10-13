package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestModelsForTabListsCustomMultiModelProviderWithoutMetadata(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "LOCAL_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "local/model-a"

[desktop]
provider_access = ["local"]

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a", "model-b"]
default = "model-a"
api_key_env = "LOCAL_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{"local/model-a", "local/model-b"} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
	if len(models) != 2 {
		t.Fatalf("Models() len = %d, want 2: %+v", len(models), models)
	}
}

func TestModelsForTabListsKeylessCustomMultiModelProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "local/model-a"

[desktop]
provider_access = ["local"]

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a", "model-b"]
default = "model-a"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{"local/model-a", "local/model-b"} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
}

func TestModelsForTabListsLoopbackCustomProviderWithMissingKeyEnv(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "local/model-a"

[desktop]
provider_access = ["local"]

[[providers]]
name = "local"
kind = "openai"
base_url = "http://127.0.0.1:23333/v1"
models = ["model-a", "model-b"]
default = "model-a"
api_key_env = "LOCAL_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{"local/model-a", "local/model-b"} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
}

func TestModelsForTabListsMimoAPIPaidAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	preset, ok := config.CuratedProviderPreset("mimo-api")
	if !ok || len(preset.Entries) == 0 {
		t.Fatal("mimo-api preset missing")
	}
	if err := cfg.UpsertProvider(preset.Entries[0]); err != nil {
		t.Fatalf("upsert mimo-api preset: %v", err)
	}
	cfg.DefaultModel = "mimo-api/mimo-v2.5-pro"
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	models := NewApp().Models()
	refs := modelRefsFromView(models)
	for _, want := range []string{
		"mimo-api/mimo-v2.6-pro",
		"mimo-api/mimo-v2.6-flash",
		"mimo-api/mimo-v2.5-pro",
		"mimo-api/mimo-v2.5",
	} {
		if !refs[want] {
			t.Fatalf("Models() refs = %+v, missing %s", models, want)
		}
	}
	if len(models) != 4 {
		t.Fatalf("Models() len = %d, want 4: %+v", len(models), models)
	}
}

func TestModelsForTabKeepsUserProvidersWithProjectConfig(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	userCfg := config.Default()
	userCfg.DefaultModel = "mimo-pro/mimo-v2.5-pro"
	userCfg.Desktop.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	userCfg.Providers = append(userCfg.Providers, config.ProviderEntry{Name: "mimo-pro", Kind: "openai", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Models: []string{"mimo-v2.5-pro"}, Default: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY"})
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save user config: %v", err)
	}

	projectRoot := t.TempDir()
	projectConfig := `default_model = "deepseek-flash/deepseek-flash"

[desktop]
provider_access = ["deepseek-flash"]

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash"]
api_key_env = "DEEPSEEK_API_KEY"
`
	if err := os.WriteFile(filepath.Join(projectRoot, "deepzora.toml"), []byte(projectConfig), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	approveWorkspace(t, projectRoot)

	app := NewApp()
	tab := &WorkspaceTab{ID: "project", WorkspaceRoot: projectRoot, Ready: true}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.activeTabID = tab.ID

	models := app.ModelsForTab(tab.ID)
	refs := modelRefsFromView(models)
	for _, want := range []string{
		"deepseek-flash/deepseek-flash",
		"mimo-pro/mimo-v2.5-pro",
	} {
		if !refs[want] {
			t.Fatalf("ModelsForTab refs = %+v, missing %s", models, want)
		}
	}
}

func TestSetModelForTabRejectsProviderOutsideAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek-flash/deepseek-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash"}
	cfg.Providers = append(cfg.Providers, config.ProviderEntry{Models: []string{"other-model"}, Default: "other-model", Name: "other", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "other-model"})
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{ID: "tab_a", Scope: "global", Ready: true, model: "deepseek-flash/deepseek-flash"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err := app.SetModelForTab(tab.ID, "other/other-model")
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("SetModelForTab hidden provider error = %v, want not available", err)
	}
}

func TestSetModelForTabReusesCurrentSessionLease(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Models: []string{"new-model"}, Default: "new-model", Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldPath := filepath.Join(dir, "leased-model-switch.jsonl")
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	if err := tab.ensureSessionLease(oldPath); err != nil {
		t.Fatalf("ensureSessionLease: %v", err)
	}
	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	if tab.Ctrl == nil || tab.Ctrl == oldCtrl {
		t.Fatalf("tab controller was not rebuilt")
	}
	if got := tab.model; got != "new/new-model" {
		t.Fatalf("tab model = %q, want new/new-model", got)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(oldPath) {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), oldPath)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 || history[1].Role != provider.RoleUser || history[1].Content != "hello" {
		t.Fatalf("carried history = %+v, want original user message", history)
	}
}

func TestSetModelForTabWaitsForConcurrentBlankSessionLease(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Models: []string{"new-model"}, Default: "new-model", Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := filepath.Join(dir, "blank-model-switch-race.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write blank session: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:            "tab_blank_race",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		SessionPath:   path,
		Ready:         true,
		model:         "old/old-model",
		sink:          &tabEventSink{tabID: "tab_blank_race", app: app},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
	})

	acquired := make(chan struct{})
	releaseHook := make(chan struct{})
	var once sync.Once
	sessionLeaseAcquireHookForTest = func() {
		once.Do(func() {
			close(acquired)
			<-releaseHook
		})
	}
	t.Cleanup(func() { sessionLeaseAcquireHookForTest = nil })

	buildErr := make(chan error, 1)
	go func() {
		buildErr <- tab.ensureSessionLease(path)
	}()

	select {
	case <-acquired:
	case err := <-buildErr:
		t.Fatalf("background lease acquire returned before hook: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("background lease acquire did not start")
	}

	switchErr := make(chan error, 1)
	go func() {
		switchErr <- app.SetModelForTab(tab.ID, "new/new-model")
	}()

	select {
	case err := <-switchErr:
		t.Fatalf("SetModelForTab returned before concurrent lease was bound: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseHook)
	if err := <-buildErr; err != nil {
		t.Fatalf("background ensureSessionLease: %v", err)
	}
	if err := <-switchErr; err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	if tab.Ctrl == nil {
		t.Fatal("model switch did not build a controller")
	}
	if got := tab.model; got != "new/new-model" {
		t.Fatalf("tab model = %q, want new/new-model", got)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != sessionRuntimeKey(path) {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), path)
	}
}

func TestSetModelForTabLeaseHeldKeepsCurrentController(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Models: []string{"new-model"}, Default: "new-model", Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldPath := filepath.Join(dir, "externally-leased-model-switch.jsonl")
	if err := os.WriteFile(oldPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(oldPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: oldPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err = app.SetModelForTab(tab.ID, "new/new-model")
	if !errors.Is(err, agent.ErrSessionLeaseHeld) {
		t.Fatalf("SetModelForTab err = %v, want ErrSessionLeaseHeld", err)
	}
	if strings.Contains(err.Error(), oldPath) || strings.Contains(err.Error(), "held by") {
		t.Fatalf("SetModelForTab surfaced raw lease details: %v", err)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after failed switch")
	}
	if got := tab.model; got != "old/old-model" {
		t.Fatalf("tab model = %q, want old/old-model", got)
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("session file size = %d, want unchanged empty file", info.Size())
	}
}

func TestSetModelForTabReattachesDetachedRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "NEW_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old", "new"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
		{Models: []string{"new-model"}, Default: "new-model", Name: "new", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "new-model", APIKeyEnv: "NEW_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := desktopSessionDir(globalTabWorkspaceRoot())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "detached-model-switch.jsonl")
	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello from detached"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: path, Label: "old", Sink: event.Discard})
	lease, err := agent.TryAcquireSessionLease(path)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	key := sessionRuntimeKey(path)
	detached := &WorkspaceTab{
		ID:             detachedRuntimeTabID(key),
		Scope:          "global",
		SessionPath:    path,
		Ctrl:           oldCtrl,
		Ready:          true,
		model:          "old/old-model",
		disabledMCP:    map[string]ServerView{},
		SharedHostKey:  "detached-host",
		ActivityStatus: "",
	}
	detached.adoptSessionLease(lease)
	tab := &WorkspaceTab{
		ID:          "tab_a",
		Scope:       "global",
		SessionPath: path,
		Ready:       true,
		model:       "old/old-model",
		sink:        &tabEventSink{tabID: "tab_a", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.detachedSessions = map[string]*WorkspaceTab{key: detached}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
		if detached.sessionLease != nil {
			detached.releaseSessionLease()
		}
	})

	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	if _, ok := app.detachedSessions[key]; ok {
		t.Fatal("detached runtime was not consumed")
	}
	if tab.Ctrl == nil || tab.Ctrl == oldCtrl {
		t.Fatalf("tab controller was not rebuilt from detached runtime")
	}
	if got := tab.model; got != "new/new-model" {
		t.Fatalf("tab model = %q, want new/new-model", got)
	}
	if tab.sessionLease == nil || sessionRuntimeKey(tab.sessionLease.Path()) != key {
		t.Fatalf("session lease path = %q, want %q", tab.currentSessionPath(), path)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 || history[1].Content != "hello from detached" {
		t.Fatalf("carried history = %+v, want detached user message", history)
	}
}

func TestSetDefaultModelRejectsProviderWithoutKey(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("MIMO_API_KEY", "")

	cfg := config.Default()
	cfg.Desktop.ProviderAccess = []string{"mimo-api"}
	cfg.Providers = append(cfg.Providers, config.ProviderEntry{Name: "mimo-api", Kind: "openai", BaseURL: "https://api.xiaomimimo.com/v1", Models: []string{"mimo-v2.5-pro"}, Default: "mimo-v2.5-pro", APIKeyEnv: "MIMO_API_KEY"})
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	tab := &WorkspaceTab{ID: "tab_a", Scope: "global", Ready: true, model: "deepseek-flash/deepseek-flash"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err := app.SetDefaultModel("mimo-api/mimo-v2.5-pro")
	if err == nil || !strings.Contains(err.Error(), "has no key") {
		t.Fatalf("SetDefaultModel no-key error = %v, want has no key", err)
	}
	if tab.model != "deepseek-flash/deepseek-flash" {
		t.Fatalf("tab model after failed default change = %q, want previous", tab.model)
	}
}
