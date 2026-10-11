package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/provider"
)

func TestDeleteProviderSavesWithoutBuildingInvalidFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a"
	cfg.Desktop.ProviderAccess = []string{"prov-a", "broken"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a"}, Default: "model-a", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.invalid/v1", Model: "model-a", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b"}, Default: "model-b", Name: "broken", Kind: "missing-provider-kind", BaseURL: "https://broken.example.invalid", Model: "model-b", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	base := control.New(control.Options{Label: "prov-a/model-a"})
	ctrl := newBlockingSnapshotCtrl(base)
	close(ctrl.releaseSnapshot)
	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{ID: "active", Scope: "global", Ready: true, Ctrl: ctrl, model: cfg.DefaultModel, Label: cfg.DefaultModel}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	err := app.DeleteProvider("prov-a")
	if err != nil {
		t.Fatalf("DeleteProvider tried to apply the fallback during save: %v", err)
	}
	if tab.Ctrl != ctrl || ctrl.closeCount.Load() != 0 {
		t.Fatalf("failed replacement closed or replaced the old controller: ctrl=%T closes=%d", tab.Ctrl, ctrl.closeCount.Load())
	}
	if tab.model != cfg.DefaultModel || tab.Label != cfg.DefaultModel {
		t.Fatalf("failed replacement changed live tab identity: model=%q label=%q", tab.model, tab.Label)
	}
}

func TestRemoveOfficialProviderAccessPreservesLiveTabUntilNextRun(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "GOOD_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek-flash/deepseek-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "good"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"deepseek-flash"}, Default: "deepseek-flash",
			Name: "deepseek-flash", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic",
			Model: "deepseek-flash", APIKeyEnv: "DEEPSEEK_API_KEY",
		},
		{Models: []string{"good-model"}, Default: "good-model", Name: "good", Kind: "openai", BaseURL: "https://good.example.invalid/v1", Model: "good-model", APIKeyEnv: "GOOD_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	old := newBlockingSnapshotCtrl(control.New(control.Options{Label: cfg.DefaultModel, Sink: event.Discard}))
	close(old.releaseSnapshot)
	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID: "active", Scope: "global", Ready: true, Ctrl: old,
		model: cfg.DefaultModel, Label: cfg.DefaultModel,
		sink: &tabEventSink{tabID: "active", app: app}, disabledMCP: map[string]ServerView{},
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

	if err := app.RemoveProviderAccess("deepseek-flash"); err != nil {
		t.Fatalf("RemoveProviderAccess: %v", err)
	}
	if tab.model != cfg.DefaultModel || tab.Ctrl != old || old.closeCount.Load() != 0 {
		t.Fatalf("saving removal changed the live tab: model=%q ctrl=%T old closes=%d", tab.model, tab.Ctrl, old.closeCount.Load())
	}
	got := config.LoadForEdit(config.UserConfigPath())
	if providerAccessSet(got.Desktop.ProviderAccess)["deepseek-flash"] {
		t.Fatalf("provider_access still contains DeepSeek: %v", got.Desktop.ProviderAccess)
	}
}

func TestDeleteProviderPreservesEveryVisibleRuntimeUsingAuxiliaryProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REMOVED_KEY", "sk-test")
	setDesktopTestCredential(t, "GOOD_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "good/good-model"
	cfg.Agent.SubagentModel = "removed/vision-model"
	cfg.Desktop.ProviderAccess = []string{"removed", "good"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"vision-model"}, Default: "vision-model", Name: "removed", Kind: "openai", BaseURL: "https://removed.example.invalid/v1", Model: "vision-model", APIKeyEnv: "REMOVED_KEY", Vision: true},
		{Models: []string{"good-model"}, Default: "good-model", Name: "good", Kind: "openai", BaseURL: "https://good.example.invalid/v1", Model: "good-model", APIKeyEnv: "GOOD_KEY"},
	}
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
			tab.releaseSessionLease()
		}
	})

	if err := app.DeleteProvider("removed"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if first.Ctrl != oldFirst || second.Ctrl != oldSecond || oldFirst.closeCount.Load() != 0 || oldSecond.closeCount.Load() != 0 {
		t.Fatalf("saving auxiliary-provider removal replaced a runtime: first=%T/%d second=%T/%d", first.Ctrl, oldFirst.closeCount.Load(), second.Ctrl, oldSecond.closeCount.Load())
	}
	if first.model != cfg.DefaultModel || second.model != cfg.DefaultModel {
		t.Fatalf("unaffected chat models changed: first=%q second=%q", first.model, second.model)
	}
	got := config.LoadForEdit(config.UserConfigPath())
	if _, ok := got.Provider("removed"); ok {
		t.Fatal("removed auxiliary provider still exists")
	}
	if got.Agent.SubagentModel != "good/good-model" {
		t.Fatalf("subagent_model = %q, want persisted visible fallback", got.Agent.SubagentModel)
	}
}

func TestDeleteProviderPreservesNonActiveWorkspaceAndProjectAuxiliaryReference(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REMOVED_KEY", "sk-test")
	setDesktopTestCredential(t, "GOOD_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "good/good-model"
	cfg.Desktop.ProviderAccess = []string{"removed", "good"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"vision-model"}, Default: "vision-model", Name: "removed", Kind: "openai", BaseURL: "https://removed.example.invalid/v1", Model: "vision-model", APIKeyEnv: "REMOVED_KEY", Vision: true},
		{Models: []string{"good-model"}, Default: "good-model", Name: "good", Kind: "openai", BaseURL: "https://good.example.invalid/v1", Model: "good-model", APIKeyEnv: "GOOD_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	activeRoot := t.TempDir()
	backgroundRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(backgroundRoot, "deepzora.toml"), []byte("[agent]\nsubagent_model = \"removed/vision-model\"\n"), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	newTab := func(id, root string) (*WorkspaceTab, *blockingSnapshotCtrl) {
		old := newBlockingSnapshotCtrl(control.New(control.Options{Label: cfg.DefaultModel, Sink: event.Discard}))
		close(old.releaseSnapshot)
		tab := &WorkspaceTab{
			ID: id, Scope: "project", WorkspaceRoot: root, Ready: true, Ctrl: old,
			model: cfg.DefaultModel, Label: cfg.DefaultModel,
			sink: &tabEventSink{tabID: id, app: app}, disabledMCP: map[string]ServerView{},
		}
		return tab, old
	}
	active, oldActive := newTab("active", activeRoot)
	background, oldBackground := newTab("background", backgroundRoot)
	app.tabs = map[string]*WorkspaceTab{active.ID: active, background.ID: background}
	app.tabOrder = []string{active.ID, background.ID}
	app.activeTabID = active.ID
	t.Cleanup(func() {
		for _, tab := range []*WorkspaceTab{active, background} {
			if tab.Ctrl != nil {
				tab.Ctrl.Close()
			}
			tab.releaseSessionLease()
		}
	})

	if err := app.DeleteProvider("removed"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if active.Ctrl != oldActive || background.Ctrl != oldBackground || oldActive.closeCount.Load() != 0 || oldBackground.closeCount.Load() != 0 {
		t.Fatalf("saving provider removal replaced a workspace runtime: active=%T/%d background=%T/%d", active.Ctrl, oldActive.closeCount.Load(), background.Ctrl, oldBackground.closeCount.Load())
	}
	projectRaw, err := os.ReadFile(filepath.Join(backgroundRoot, "deepzora.toml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if !strings.Contains(string(projectRaw), "removed/vision-model") {
		t.Fatalf("global provider deletion rewrote project-owned model reference: %s", projectRaw)
	}
}

func TestDeleteProviderPreservesDetachedRuntimeUsingAuxiliaryProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "REMOVED_KEY", "sk-test")
	setDesktopTestCredential(t, "GOOD_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "good/good-model"
	cfg.Agent.SubagentModel = "removed/vision-model"
	cfg.Desktop.ProviderAccess = []string{"removed", "good"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"vision-model"}, Default: "vision-model", Name: "removed", Kind: "openai", BaseURL: "https://removed.example.invalid/v1", Model: "vision-model", APIKeyEnv: "REMOVED_KEY", Vision: true},
		{Models: []string{"good-model"}, Default: "good-model", Name: "good", Kind: "openai", BaseURL: "https://good.example.invalid/v1", Model: "good-model", APIKeyEnv: "GOOD_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	detachedCtrl := control.New(control.Options{Label: cfg.DefaultModel, Sink: event.Discard})
	detached := &WorkspaceTab{ID: "detached", Scope: "global", Ctrl: detachedCtrl, model: cfg.DefaultModel}
	app.detachedSessions = map[string]*WorkspaceTab{detached.ID: detached}
	t.Cleanup(detachedCtrl.Close)

	err := app.DeleteProvider("removed")
	if err != nil || detached.Ctrl != detachedCtrl {
		t.Fatalf("DeleteProvider interrupted detached runtime: %v", err)
	}
	got := config.LoadForEdit(config.UserConfigPath())
	if _, ok := got.Provider("removed"); ok || got.Agent.SubagentModel != "good/good-model" {
		t.Fatalf("removal was not committed: provider=%v subagent_model=%q", ok, got.Agent.SubagentModel)
	}
}

func TestDeleteProviderPreservesLiveHistoryAndSharedHost(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPZORA_TEST_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "prov-a/model-a"
	cfg.Desktop.ProviderAccess = []string{"prov-a", "prov-b"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"model-a"}, Default: "model-a", Name: "prov-a", Kind: "openai", BaseURL: "https://a.example.invalid/v1", Model: "model-a", APIKeyEnv: "DEEPZORA_TEST_KEY"},
		{Models: []string{"model-b"}, Default: "model-b", Name: "prov-b", Kind: "openai", BaseURL: "https://b.example.invalid/v1", Model: "model-b", APIKeyEnv: "DEEPZORA_TEST_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	path := filepath.Join(dir, "provider-removal-success.jsonl")
	session := agent.NewSession("system")
	session.Add(provider.Message{Role: provider.RoleUser, Content: "preserve this history"})
	exec := agent.New(nil, nil, session, agent.Options{}, event.Discard)

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	hostKey := "provider-removal-success-host"
	host := app.acquireSharedHost(hostKey)
	old := newBlockingSnapshotCtrl(control.New(control.Options{
		Executor: exec, SessionDir: dir, SessionPath: path, Label: cfg.DefaultModel, Host: host, Sink: event.Discard,
	}))
	close(old.releaseSnapshot)
	tab := &WorkspaceTab{
		ID: "active", Scope: "global", Ready: true, Ctrl: old, SessionPath: path,
		model: cfg.DefaultModel, Label: cfg.DefaultModel, SharedHostKey: hostKey, disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		tab.releaseSessionLease()
		app.releaseSharedHost(hostKey)
	})

	if err := app.DeleteProvider("prov-a"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	if tab.Ctrl != old || old.closeCount.Load() != 0 {
		t.Fatalf("saving removal replaced current controller = %T, old closes = %d", tab.Ctrl, old.closeCount.Load())
	}
	if tab.model != cfg.DefaultModel || tab.Label != cfg.DefaultModel {
		t.Fatalf("saving removal changed current identity = model:%q label:%q", tab.model, tab.Label)
	}
	if !sameDesktopPath(tab.Ctrl.SessionPath(), path) || !sameDesktopPath(tab.SessionPath, path) {
		t.Fatalf("replacement session path = ctrl:%q tab:%q, want %q", tab.Ctrl.SessionPath(), tab.SessionPath, path)
	}
	if tab.Ctrl.Host() != host || tab.SharedHostKey != hostKey {
		t.Fatalf("replacement did not reuse shared host: host=%p want=%p key=%q", tab.Ctrl.Host(), host, tab.SharedHostKey)
	}
	history := tab.Ctrl.History()
	preserved := false
	for _, message := range history {
		if message.Content == "preserve this history" {
			preserved = true
			break
		}
	}
	if !preserved {
		t.Fatalf("replacement history = %+v, want preserved user message", history)
	}
}

func TestRemoveProviderAccessSavesWhilePreservingDetachedRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "mimo-pro/mimo-v2.5-pro"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	detachedCtrl := control.New(control.Options{Label: "deepseek-flash"})
	detached := &WorkspaceTab{
		ID: detachedRuntimeTabID("detached-provider-removal"), Scope: "global", Ready: true,
		Ctrl: detachedCtrl, model: "deepseek-flash/deepseek-flash", Label: "deepseek-flash",
	}
	app.detachedSessions = map[string]*WorkspaceTab{"detached-provider-removal": detached}
	t.Cleanup(detachedCtrl.Close)

	err := app.RemoveProviderAccess("deepseek-flash")
	if err != nil {
		t.Fatalf("RemoveProviderAccess rejected detached runtime: %v", err)
	}
	got := config.LoadForEdit(config.UserConfigPath())
	if providerAccessSet(got.Desktop.ProviderAccess)["deepseek-flash"] {
		t.Fatalf("provider access removal was not committed: %+v", got.Desktop.ProviderAccess)
	}
	if detached.Ctrl != detachedCtrl || detached.model != "deepseek-flash/deepseek-flash" {
		t.Fatalf("detached runtime changed after rejection: ctrl=%T model=%q", detached.Ctrl, detached.model)
	}
}

func TestRemoveProviderAccessesRejectsMixedGroupBeforeMutation(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "custom"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"deepseek-flash"}, Default: "deepseek-flash", Name: "deepseek-flash", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic", Model: "deepseek-flash"},
		{Models: []string{"custom-model"}, Default: "custom-model", Name: "custom", Kind: "openai", BaseURL: "https://proxy.example/v1", Model: "custom-model"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := NewApp().RemoveProviderAccesses([]string{"deepseek-flash", "custom"}); err == nil {
		t.Fatal("RemoveProviderAccesses accepted mixed official and custom providers")
	}
	got := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(got.Desktop.ProviderAccess)
	if !access["deepseek-flash"] || !access["custom"] {
		t.Fatalf("provider access was partially mutated after rejection: %+v", got.Desktop.ProviderAccess)
	}
}

func TestProviderAccessFallbackSkipsUnconfiguredProviders(t *testing.T) {
	cfg := &config.Config{
		Desktop: config.DesktopConfig{ProviderAccess: []string{"deepseek-flash", "unconfigured", "local"}},
		Providers: []config.ProviderEntry{
			{Models: []string{"deepseek-flash"}, Default: "deepseek-flash", Name: "deepseek-flash", Kind: "anthropic", BaseURL: "https://api.deepseek.com/anthropic", Model: "deepseek-flash"},
			{Models: []string{"remote"}, Default: "remote", Name: "unconfigured", Kind: "openai", BaseURL: "https://api.example.invalid/v1", Model: "remote", APIKeyEnv: "MISSING_API_KEY"},
			{Models: []string{"local-model"}, Default: "local-model", Name: "local", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "local-model"},
		},
	}

	if got := providerAccessFallbackRef(cfg, []string{"deepseek-flash"}); got != "local/local-model" {
		t.Fatalf("fallback = %q, want configured local provider", got)
	}
}

func TestProviderRemovalStateFingerprintCoversConfigAndCredentialRevision(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderEntry{{Models: []string{"model-a"}, Default: "model-a",
		Name: "candidate", Kind: "openai", BaseURL: "https://example.invalid/v1",
		Model: "model-a", APIKeyEnv: "CANDIDATE_API_KEY",
	}}

	base := providerRemovalStateFingerprint(cfg, "credential-revision-a")
	if got := providerRemovalStateFingerprint(cfg, "credential-revision-a"); got != base {
		t.Fatal("unchanged provider removal state produced an unstable fingerprint")
	}
	if strings.Contains(base, "CANDIDATE_API_KEY") {
		t.Fatal("provider removal fingerprint exposed the credential environment name")
	}
	if got := providerRemovalStateFingerprint(cfg, "credential-revision-b"); got == base {
		t.Fatal("credential revision change did not invalidate provider removal fingerprint")
	}
	cfg.Providers[0].APIKeyEnv = "ROTATED_API_KEY"
	if got := providerRemovalStateFingerprint(cfg, "credential-revision-a"); got == base {
		t.Fatal("provider configuration change did not invalidate provider removal fingerprint")
	}
}

func TestProviderAccessFallbackSkipsConfiguredProvidersOutsideAccessList(t *testing.T) {
	cfg := &config.Config{
		Desktop: config.DesktopConfig{ProviderAccess: []string{"removed", "visible"}},
		Providers: []config.ProviderEntry{
			{Models: []string{"removed-model"}, Default: "removed-model", Name: "removed", Kind: "openai", BaseURL: "https://removed.example/v1", Model: "removed-model"},
			{Models: []string{"hidden-model"}, Default: "hidden-model", Name: "hidden", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "hidden-model"},
			{Models: []string{"visible-model"}, Default: "visible-model", Name: "visible", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "visible-model"},
		},
	}

	if got := providerAccessFallbackRef(cfg, []string{"removed"}); got != "visible/visible-model" {
		t.Fatalf("fallback = %q, want remaining accessed provider", got)
	}
}

func TestDeleteProviderPersistsVisibleFallbackInsteadOfHiddenConfiguredProvider(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "removed/removed-model"
	cfg.Agent.PlannerModel = "removed"
	cfg.Agent.SubagentModel = "removed/removed-model"
	cfg.Agent.SubagentModels = map[string]string{"review": "removed/removed-model"}
	cfg.Desktop.ProviderAccess = []string{"removed", "visible"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"removed-model"}, Default: "removed-model", Name: "removed", Kind: "openai", BaseURL: "https://removed.example/v1", Model: "removed-model"},
		{Models: []string{"hidden-model"}, Default: "hidden-model", Name: "hidden", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "hidden-model"},
		{Models: []string{"visible-model"}, Default: "visible-model", Name: "visible", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "visible-model"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := NewApp().DeleteProvider("removed"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	want := "visible/visible-model"
	if got.DefaultModel != want || got.Agent.PlannerModel != want || got.Agent.SubagentModel != want || got.Agent.SubagentModels["review"] != want {
		t.Fatalf("persisted refs used a hidden fallback: default=%q planner=%q subagent=%q skills=%+v", got.DefaultModel, got.Agent.PlannerModel, got.Agent.SubagentModel, got.Agent.SubagentModels)
	}
	if _, ok := got.Provider("hidden"); !ok {
		t.Fatal("hidden provider should remain configured even though it is not a removal fallback")
	}
}

func TestDeleteProviderSavesAndBlocksNewRunWhenOnlyHiddenProviderRemains(t *testing.T) {
	isolateDesktopUserDirs(t)
	cfg := config.Default()
	cfg.DefaultModel = "removed/removed-model"
	cfg.Desktop.ProviderAccess = []string{"removed"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"removed-model"}, Default: "removed-model", Name: "removed", Kind: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "removed-model"},
		{Models: []string{"hidden-model"}, Default: "hidden-model", Name: "hidden", Kind: "openai", BaseURL: "http://127.0.0.1:11435/v1", Model: "hidden-model"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	err := NewApp().DeleteProvider("removed")
	if err != nil {
		t.Fatalf("DeleteProvider should save even without a visible fallback: %v", err)
	}

	got := config.LoadForEdit(config.UserConfigPath())
	if got.DefaultModel != "" {
		t.Fatalf("default model = %q, want no hidden fallback", got.DefaultModel)
	}
	if len(got.Desktop.ProviderAccess) != 0 {
		t.Fatalf("provider access = %+v, want empty", got.Desktop.ProviderAccess)
	}
	if _, ok := got.Provider("removed"); ok {
		t.Fatal("provider was not deleted")
	}
	if _, ok := got.Provider("hidden"); !ok {
		t.Fatal("hidden provider changed despite the rejected operation")
	}
	if _, err := resolveModelSettingsRuntime(got, cfg.DefaultModel); err == nil {
		t.Fatal("new run was allowed to use a hidden fallback")
	}
}

func TestRemoveProviderAccessesSavesAndBlocksNewRunWithoutConfiguredFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek-flash/deepseek-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	tab := &WorkspaceTab{ID: "deepseek-flash", Scope: "global", model: cfg.DefaultModel}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	err := app.RemoveProviderAccess("deepseek-flash")
	if err != nil {
		t.Fatalf("RemoveProviderAccess should save without a fallback: %v", err)
	}
	got := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(got.Desktop.ProviderAccess)
	if access["deepseek-flash"] || !access["mimo-pro"] {
		t.Fatalf("provider access was not committed: %+v", got.Desktop.ProviderAccess)
	}
	if got.DefaultModel != "" || tab.model != cfg.DefaultModel {
		t.Fatalf("saved/current models are incorrect: config=%q tab=%q", got.DefaultModel, tab.model)
	}
	if _, err := resolveModelSettingsRuntime(got, tab.model); err == nil {
		t.Fatal("new run was allowed without a configured fallback")
	}
}

func TestRemoveProviderAccessesRejectsOfficialProviderChangedBeforeCommit(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek-flash/deepseek-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	ctrl := newBlockingSnapshotCtrl(control.New(control.Options{Label: "deepseek-flash"}))
	tab := &WorkspaceTab{ID: "deepseek-flash", Scope: "global", model: cfg.DefaultModel, Ctrl: ctrl}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	fingerprint := app.Settings().ModelSettingsFingerprint

	unlock := config.LockUserConfigEdits()
	changed := config.LoadForEdit(config.UserConfigPath())
	provider, ok := changed.Provider("deepseek-flash")
	if !ok {
		unlock()
		t.Fatal("deepseek provider missing")
	}
	provider.BaseURL = "https://proxy.example/v1"
	if err := changed.SaveTo(config.UserConfigPath()); err != nil {
		unlock()
		t.Fatalf("save overlapping config edit: %v", err)
	}
	unlock()
	result := app.ApplyModelSettings(ModelSettingsChange{Kind: "access_remove", Names: []string{"deepseek-flash"}, RequestID: "remove", ExpectedFingerprint: fingerprint})
	if result.Persisted || len(result.Issues) == 0 {
		t.Fatal("RemoveProviderAccess accepted an official provider changed before commit")
	}
	got := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(got.Desktop.ProviderAccess)
	if !access["deepseek-flash"] || !access["mimo-pro"] {
		t.Fatalf("provider access changed after rejected overlap: %+v", got.Desktop.ProviderAccess)
	}
	if ctrl.closeCount.Load() != 0 || tab.Ctrl != ctrl || tab.model != cfg.DefaultModel {
		t.Fatalf("runtime mutated after rejected overlap: closes=%d ctrl=%T model=%q", ctrl.closeCount.Load(), tab.Ctrl, tab.model)
	}
}

func TestRemoveProviderAccessesRejectsCredentialChangeBeforeCommit(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "DEEPSEEK_API_KEY", "sk-test")
	setDesktopTestCredential(t, "MIMO_API_KEY", "old-key")

	cfg := config.Default()
	cfg.DefaultModel = "deepseek-flash/deepseek-flash"
	cfg.Desktop.ProviderAccess = []string{"deepseek-flash", "mimo-pro"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	ctrl := newBlockingSnapshotCtrl(control.New(control.Options{Label: "deepseek-flash"}))
	tab := &WorkspaceTab{ID: "deepseek-flash", Scope: "global", model: cfg.DefaultModel, Ctrl: ctrl}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	fingerprint := app.Settings().ModelSettingsFingerprint
	setDesktopTestCredential(t, "MIMO_API_KEY", "new-key")
	result := app.ApplyModelSettings(ModelSettingsChange{Kind: "access_remove", Names: []string{"deepseek-flash"}, RequestID: "remove", ExpectedFingerprint: fingerprint})
	if result.Persisted || len(result.Issues) == 0 {
		t.Fatal("RemoveProviderAccess accepted credentials changed before commit")
	}
	got := config.LoadForEdit(config.UserConfigPath())
	access := providerAccessSet(got.Desktop.ProviderAccess)
	if !access["deepseek-flash"] || !access["mimo-pro"] {
		t.Fatalf("provider access changed after rejected credential overlap: %+v", got.Desktop.ProviderAccess)
	}
	if ctrl.closeCount.Load() != 0 || tab.Ctrl != ctrl || tab.model != cfg.DefaultModel {
		t.Fatalf("runtime mutated after rejected credential overlap: closes=%d ctrl=%T model=%q", ctrl.closeCount.Load(), tab.Ctrl, tab.model)
	}
}
