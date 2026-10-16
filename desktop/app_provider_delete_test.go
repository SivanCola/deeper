package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/control"
)

func TestRemoveBuiltInProviderAccessRetargetsDefaultToRemainingAccess(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")
	if err := os.MkdirAll(filepath.Dir(config.UserConfigPath()), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(config.UserConfigPath(), []byte(`
default_model = "deepseek-flash/deepseek-v4-pro"

[desktop]
provider_access = ["deepseek-flash", "mimo-pro"]

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
models = ["deepseek-v4-flash", "deepseek-v4-pro"]
default = "deepseek-v4-flash"
api_key_env = "DEEPSEEK_API_KEY"

[[providers]]
name = "mimo-pro"
kind = "openai"
base_url = "https://token-plan-cn.xiaomimimo.com/v1"
models = ["mimo-v2.5-pro"]
api_key_env = "MIMO_API_KEY"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := NewApp().RemoveProviderAccess("deepseek-flash"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	cfg := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(cfg.Desktop.ProviderAccess)
	if access["deepseek"] || !access["mimo-pro"] {
		t.Fatalf("provider_access = %+v, want only mimo-pro", cfg.Desktop.ProviderAccess)
	}
	if cfg.DefaultModel != "mimo-pro/mimo-v2.5-pro" {
		t.Fatalf("default_model = %q, want mimo-pro/mimo-v2.5-pro", cfg.DefaultModel)
	}
}

func TestDeleteProviderMigratesConfigAndPreservesOpenTabs(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a2"
	cfg.Providers = []config.ProviderEntry{
		{Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", Models: []string{"model-a1", "model-a2"}, APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	cfg.Agent.PlannerModel = "prov-a"
	cfg.Desktop.ProviderAccess = []string{"prov-a", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	ctrl := control.New(control.Options{Label: "old"})
	defer ctrl.Close()
	app := NewApp()
	tab := &WorkspaceTab{ID: "tab_a", Scope: "global", Ctrl: ctrl, Label: "prov-a/model-a1", Ready: true, model: "prov-a/model-a1"}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	if _, ok := got.Provider("prov-a"); ok {
		t.Fatal("prov-a should be removed")
	}
	if got.DefaultModel != "prov-b/model-b1" || got.Agent.PlannerModel != "prov-b/model-b1" {
		t.Fatalf("model refs after delete = default:%q planner:%q, want prov-b", got.DefaultModel, got.Agent.PlannerModel)
	}
	if providerAccessSet(got.Desktop.ProviderAccess)["prov-a"] {
		t.Fatalf("provider access still contains prov-a: %+v", got.Desktop.ProviderAccess)
	}
	if tab.model != "prov-a/model-a1" || tab.Label != "prov-a/model-a1" {
		t.Fatalf("saving deletion changed current identity: model:%q label:%q", tab.model, tab.Label)
	}
	if tab.Ctrl != ctrl {
		t.Fatal("saving deletion closed the current controller")
	}
}

func TestDeleteProviderLeavesStartupPublicationToVersionFence(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-b/model-b1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a1"}, Default: "model-a1", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	cfg.Desktop.ProviderAccess = []string{"prov-a", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	// Model the async startup build still being in flight for the affected
	// tab: no controller yet, a live generation, a cancellable build context.
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab := &WorkspaceTab{
		ID:              "tab_a",
		Scope:           "global",
		model:           "prov-a/model-a1",
		buildGeneration: 1,
		buildCancel:     buildCancel,
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if tab.buildGeneration != 1 || buildCtx.Err() != nil || tab.model != "prov-a/model-a1" {
		t.Fatal("saving deletion changed an in-flight startup before its publication fence")
	}
	buildCancel()
}

func TestRemoveBuiltInProviderAccessLeavesStartupPublicationToVersionFence(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-b/model-b1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"deepseek-chat"}, Default: "deepseek-chat", Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	cfg.Desktop.ProviderAccess = []string{"deepseek", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	buildCtx, buildCancel := context.WithCancel(context.Background())
	tab := &WorkspaceTab{
		ID:              "tab_ds",
		Scope:           "global",
		model:           "deepseek/deepseek-chat",
		buildGeneration: 1,
		buildCancel:     buildCancel,
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.RemoveProviderAccess("deepseek"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	if tab.buildGeneration != 1 || buildCtx.Err() != nil || tab.model != "deepseek/deepseek-chat" {
		t.Fatal("saving access removal changed an in-flight startup before its publication fence")
	}
	buildCancel()
}

func TestDeleteProviderPreservesRunningAffectedTab(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a1"}, Default: "model-a1", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Runner: runner}), "prov-a/model-a1")
	ctrl := app.activeCtrl()
	ctrl.Submit("work")
	<-runner.started

	err := app.DeleteProvider("prov-a")
	if err != nil || app.activeCtrl() != ctrl || !ctrl.RuntimeStateSnapshot().Running {
		t.Fatalf("DeleteProvider interrupted accepted work: %v", err)
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("provider deletion was not persisted during the run")
	}

	close(runner.release)
	waitNotRunning(t, ctrl)
	ctrl.Close()
}

func TestDeleteProviderDoesNotWaitForRuntimeReconstruction(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a1"}, Default: "model-a1", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Runner: runner}), "prov-a/model-a1")
	ctrl := app.activeCtrl()
	app.runtimeRebuildMu.Lock()
	defer app.runtimeRebuildMu.Unlock()
	ctrl.Submit("work")
	<-runner.started
	done := make(chan error, 1)
	go func() { done <- app.DeleteProvider("prov-a") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("saving provider deletion waited for runtime reconstruction")
	}
	if app.activeCtrl() != ctrl || !ctrl.RuntimeStateSnapshot().Running {
		t.Fatal("saving deletion interrupted accepted work")
	}
	if _, ok := config.LoadForEdit(config.UserConfigPath()).Provider("prov-a"); ok {
		t.Fatal("deletion was not committed")
	}
	close(runner.release)
	waitNotRunning(t, ctrl)
	ctrl.Close()
}

func TestDeleteProviderPreservesAffectedTabSharedHostReference(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a1"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a1"}, Default: "model-a1", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.com", Model: "model-a1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	hostKey := "provider-shared-host"
	host := app.acquireSharedHost(hostKey)
	ctrl := control.New(control.Options{Host: host})
	tab := &WorkspaceTab{
		ID: "affected", Scope: "global", Ready: true, Ctrl: ctrl,
		model: "prov-a/model-a1", SharedHostKey: hostKey, disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if tab.SharedHostKey != hostKey || tab.Ctrl != ctrl {
		t.Fatal("saving deletion released the current runtime's shared host")
	}
	app.sharedHostsMu.Lock()
	_, retained := app.sharedHosts[hostKey]
	app.sharedHostsMu.Unlock()
	if !retained {
		t.Fatal("provider deletion released an owned shared host reference")
	}
	ctrl.Close()
	app.releaseSharedHost(hostKey)
}

func TestRemoveBuiltInProviderAccessPreservesAffectedTabSharedHostReference(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")
	cfg := config.Default()
	cfg.DefaultModel = "deepseek/deepseek-chat"
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"deepseek-chat"}, Default: "deepseek-chat", Name: "deepseek", Kind: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-chat", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b1"}, Default: "model-b1", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.com", Model: "model-b1", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	cfg.Desktop.ProviderAccess = []string{"deepseek", "prov-b"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	hostKey := "provider-access-shared-host"
	host := app.acquireSharedHost(hostKey)
	ctrl := control.New(control.Options{Host: host})
	tab := &WorkspaceTab{
		ID: "affected", Scope: "global", Ready: true, Ctrl: ctrl,
		model: "deepseek/deepseek-chat", SharedHostKey: hostKey, disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.RemoveProviderAccess("deepseek"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	if tab.SharedHostKey != hostKey || tab.Ctrl != ctrl {
		t.Fatal("saving access removal released the current runtime's shared host")
	}
	app.sharedHostsMu.Lock()
	_, retained := app.sharedHosts[hostKey]
	app.sharedHostsMu.Unlock()
	if !retained {
		t.Fatal("provider access removal released an owned shared host reference")
	}
	ctrl.Close()
	app.releaseSharedHost(hostKey)
}
