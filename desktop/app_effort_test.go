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

func TestCommandsIncludesDocsAndEffortNotThinking(t *testing.T) {
	app := NewApp()
	cmds := app.Commands()
	if !hasCommand(cmds, "docs") {
		t.Fatalf("Commands() should include docs: %+v", cmds)
	}
	if !hasCommand(cmds, "effort") {
		t.Fatalf("Commands() should include effort: %+v", cmds)
	}
	if !hasCommand(cmds, "reload") {
		t.Fatalf("Commands() should include reload: %+v", cmds)
	}
	if hasCommand(cmds, "thinking") {
		t.Fatalf("Commands() should not include thinking: %+v", cmds)
	}
}

func TestEffortDefaultsBeforeStartup(t *testing.T) {
	isolateDesktopUserDirs(t)

	got := NewApp().Effort()
	if !got.Supported || got.Current != "auto" || got.Default != "high" || !hasLevel(got.Levels, "auto") {
		t.Fatalf("pre-startup Effort() = %+v, want auto with DeepSeek default high", got)
	}
}

func TestSetEffortForTabPersistsAndAutoClearsWithoutTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	if err := app.SetEffortForTab("", "max"); err != nil {
		t.Fatalf("SetEffortForTab(max): %v", err)
	}
	if got := app.Effort().Current; got != "max" {
		t.Fatalf("Effort current = %q, want max", got)
	}
	if err := app.SetEffortForTab("", "auto"); err != nil {
		t.Fatalf("SetEffortForTab(auto): %v", err)
	}
	if got := app.Effort().Current; got != "auto" {
		t.Fatalf("Effort current = %q, want auto", got)
	}
	body, err := os.ReadFile(config.UserConfigPath())
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if strings.Contains(string(body), `effort      = "max"`) {
		t.Fatalf("auto should clear explicit max effort:\n%s", body)
	}
}

func TestDeferredRebuildRetryAppliesAfterCurrentTurnStops(t *testing.T) {
	testDeferredCurrentRebuild(t, false)
}

func TestDeferredRebuildScheduleAfterStopIsNoop(t *testing.T) {
	app := NewApp()
	app.stopDeferredRebuildRetry()
	app.scheduleDeferredRebuild("tab_x", "settings")
	if app.deferredRebuildPending("tab_x") {
		t.Fatal("schedule after stop should not register pending work")
	}
}

func TestDeferredRebuildAppliesToItsInactiveTarget(t *testing.T) {
	testDeferredCurrentRebuild(t, true)
}

func testDeferredCurrentRebuild(t *testing.T, inactive bool) {
	t.Helper()
	prov := &blockingPinnedProvider{started: make(chan struct{}), release: make(chan struct{})}
	app, tab, oldCtrl, _ := currentClearFixture(t, prov, nil)
	before, bound := oldCtrl.SessionRef()
	if !bound {
		t.Fatal("missing current identity before deferred rebuild")
	}
	model, _ := configureSwitchableDefaultModels(t)
	tab.model = model
	app.ctx = t.Context()
	app.readyHook = func() {}
	installNoopRuntimeEvents(app, tab.sink)
	t.Cleanup(func() {
		if current := app.controllerForTab(tab); current != nil && current != oldCtrl {
			current.Close()
		}
	})
	var once sync.Once
	release := func() { once.Do(func() { close(prov.release) }) }
	defer release()
	go oldCtrl.Submit("finish before rebuilding")
	select {
	case <-prov.started:
	case <-time.After(5 * time.Second):
		t.Fatal("current turn did not start")
	}
	// Saved settings can arrive while work is active; the retry retains the
	// captured target rather than rebuilding whichever tab is focused later.
	app.scheduleDeferredRebuild(tab.ID, "settings")
	app.deferredRebuildTick(false)
	if !app.deferredRebuildPending(tab.ID) || app.controllerForTab(tab) != oldCtrl {
		t.Fatal("deferred rebuild replaced a running current session")
	}
	other := &WorkspaceTab{ID: "other", Scope: "global", Ready: true, Ctrl: &control.Controller{}}
	if inactive {
		app.mu.Lock()
		app.tabs[other.ID] = other
		app.tabOrder = append(app.tabOrder, other.ID)
		app.activeTabID = other.ID
		app.mu.Unlock()
	}
	originalOther := other.Ctrl
	release()
	waitNotRunning(t, oldCtrl)
	app.deferredRebuildTick(false)
	if app.deferredRebuildPending(tab.ID) || app.controllerForTab(tab) == oldCtrl {
		t.Fatal("idle current session did not apply its queued rebuild")
	}
	if inactive && app.controllerForTab(other) != originalOther {
		t.Fatal("deferred retry rebuilt the focused sibling")
	}
	if after, bound := tab.Ctrl.(control.IdentityLifecycle).SessionRef(); !bound || after != before {
		t.Fatal("deferred rebuild changed the session identity")
	}
}

func TestSetEffortForTabFailedPublicationKeepsOldControllerAlive(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{{Models: []string{"old-model"}, Default: "old-model",
		Name:             "old",
		Kind:             "openai",
		BaseURL:          "https://example.invalid/v1",
		Model:            "old-model",
		APIKeyEnv:        "OLD_MODEL_KEY",
		SupportedEfforts: []string{"low", "max"},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	app := NewApp()
	oldCtrl := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, SessionDir: dir, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:    "tab_effort",
		Scope: "global",

		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_effort", app: app},
		disabledMCP: map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, oldCtrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if c := app.controllerForTab(tab); c != nil && c != oldCtrl {
			c.Close()
		}
	})

	wantErr := errors.New("injected candidate publication failure")
	app.rebindCandidateHook = func(string) error { return wantErr }
	err := app.SetEffortForTab(tab.ID, "max")
	if !errors.Is(err, wantErr) {
		t.Fatalf("SetEffortForTab err = %v, want publication failure", err)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatal("tab controller changed after failed effort switch")
	}

	app.rebindCandidateHook = nil
	if err := app.SetEffortForTab(tab.ID, "max"); err != nil {
		t.Fatalf("SetEffortForTab retry after candidate failure: %v", err)
	}
	if tab.Ctrl == oldCtrl {
		t.Fatal("retry did not rebuild the controller")
	}
}

func TestSetModelForTabPreservesCurrentSystemPromptWithoutChangingDefaults(t *testing.T) {
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
	if err := os.MkdirAll(config.MemoryUserDir(), 0o755); err != nil {
		t.Fatalf("mkdir memory dir: %v", err)
	}
	const freshRule = "Fresh global AGENTS rule for model switch"
	if err := os.WriteFile(filepath.Join(config.MemoryUserDir(), "AGENTS.md"), []byte(freshRule), 0o644); err != nil {
		t.Fatalf("write global AGENTS.md: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldSession := agent.NewSession("old system prompt without memory")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "hello"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	app := NewApp()
	oldCtrl := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, SessionDir: dir, Label: "old", Sink: event.Discard})

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
	sibling := &WorkspaceTab{
		ID:          "tab_b",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		disabledMCP: map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, oldCtrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab, sibling.ID: sibling}
	app.tabOrder = []string{tab.ID, sibling.ID}
	app.activeTabID = tab.ID
	var switchTiming modelSwitchTiming
	app.modelSwitchTimingHook = func(timing modelSwitchTiming) { switchTiming = timing }
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.SetModelForTab(tab.ID, "new/new-model"); err != nil {
		t.Fatalf("SetModelForTab: %v", err)
	}
	history := tab.Ctrl.History()
	if len(history) < 2 {
		t.Fatalf("history length = %d, want system + user", len(history))
	}
	if history[0].Role != provider.RoleSystem {
		t.Fatalf("first message role = %s, want system", history[0].Role)
	}
	if history[0].Content != "old system prompt without memory" {
		t.Fatalf("current persisted system prompt changed:\n%s", history[0].Content)
	}
	if history[1].Role != provider.RoleUser || history[1].Content != "hello" {
		t.Fatalf("carried user message changed: %+v", history[1])
	}
	if got := config.LoadForEdit(config.UserConfigPath()).DefaultModel; got != "old/old-model" {
		t.Fatalf("default model after session switch = %q, want old/old-model", got)
	}
	if sibling.model != "old/old-model" {
		t.Fatalf("sibling tab model after session switch = %q, want old/old-model", sibling.model)
	}
	if switchTiming.Outcome != "ok" || switchTiming.Total <= 0 {
		t.Fatalf("model switch timing = %+v, want successful non-zero observation", switchTiming)
	}
	if switchTiming.Build < 0 || switchTiming.LeaseAndResume < 0 || switchTiming.SwapAndPersist < 0 {
		t.Fatalf("model switch stage timing incomplete: %+v", switchTiming)
	}
}

// TestSetModelForTabRestoresSessionAuthorizations pins the fix for a model
// switch dropping same-session "Allow for this session" tool grants and
// Plan-mode read-only command trust, forcing the user to re-approve
// something already granted this session after every model/effort/token-mode
// switch.
// TestRebuildSettingLockedRestoresSessionAuthorizations covers the same
// dropped-session-authorization bug for the settings-change rebuild path
// (also used by the deferred-rebuild retry loop), independent from
// SetModelForTab's own rebuild.
func TestRebuildSettingLockedRestoresSessionAuthorizations(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OLD_MODEL_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "old/old-model"
	cfg.Desktop.ProviderAccess = []string{"old"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"old-model"}, Default: "old-model", Name: "old", Kind: "openai", BaseURL: "https://example.invalid/v1", Model: "old-model", APIKeyEnv: "OLD_MODEL_KEY"},
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	app := NewApp()
	oldCtrl := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, SessionDir: dir, Label: "old", Sink: event.Discard})
	oldCtrl.RestoreSessionAuthorizations(control.SessionAuthorizations{
		Grants: []string{"bash|go test ./..."},
	})

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
	setCurrentRebuildTabIdentity(t, app, tab, oldCtrl)
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.readyHook = func() {}
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	if err := app.rebuildSetting("settings"); err != nil {
		t.Fatalf("rebuildSetting: %v", err)
	}

	newCtrl, ok := tab.Ctrl.(*control.Controller)
	if !ok {
		t.Fatalf("tab.Ctrl = %T, want *control.Controller", tab.Ctrl)
	}
	got := newCtrl.SessionAuthorizations()
	if len(got.Grants) != 1 || got.Grants[0] != "bash|go test ./..." {
		t.Fatalf("restored grants = %+v, want [\"bash|go test ./...\"]", got.Grants)
	}

}

func TestEffortCommandUsesCurrentSessionProviderAfterNavigation(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "OWNER_MODEL_KEY", "sk-test")
	setDesktopTestCredential(t, "STALE_MODEL_KEY", "sk-test")

	projectA := t.TempDir()
	projectB := t.TempDir()
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}
	ownerConfig := `default_model = "owner/owner-model"
[[providers]]
name = "owner"
kind = "openai"
base_url = "https://owner.example.invalid/v1"
models = ["owner-model"]
api_key_env = "OWNER_MODEL_KEY"
supported_efforts = ["max"]
default_effort = "max"
`
	if err := os.WriteFile(filepath.Join(projectA, "deepzora.toml"), []byte(ownerConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, projectA)
	staleConfig := `default_model = "stale/stale-model"
[[providers]]
name = "stale"
kind = "openai"
base_url = "https://stale.example.invalid/v1"
models = ["stale-model"]
api_key_env = "STALE_MODEL_KEY"
reasoning_protocol = "none"
`
	if err := os.WriteFile(filepath.Join(projectB, "deepzora.toml"), []byte(staleConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, projectB)

	app := NewApp()
	app.readyHook = func() {}
	t.Cleanup(app.closeSessionServices)
	ref, _ := createCurrentRegistrySession(t, app, projectA, "effort-owner", true)
	tab := &WorkspaceTab{ID: "effort-navigation", Scope: "project", WorkspaceRoot: projectB,
		disabledMCP: map[string]ServerView{}}
	app.tabs[tab.ID], app.tabOrder, app.activeTabID = tab, []string{tab.ID}, tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
		if tab.SharedHostKey != "" {
			app.releaseSharedHost(tab.SharedHostKey)
		}
	})
	if _, err := app.resumeCanonicalSessionForTranscript(tab, nil, sessionRoute(ref.SessionID), 10, false); err != nil {
		t.Fatal(err)
	}

	if err := app.SubmitToTabWithID(tab.ID, "/effort max", newSessionRuntimeID("test-submit")); err != nil {
		t.Fatalf("SubmitToTab(/effort max): %v", err)
	}
	waitNotRunning(t, tab.Ctrl)
	if tab.effort == nil || *tab.effort != "max" {
		t.Fatalf("tab effort = %#v, want max from current project A provider", tab.effort)
	}
	if got := normalizeProjectRoot(tab.WorkspaceRoot); got != normalizeProjectRoot(projectA) {
		t.Fatalf("tab workspace root = %q, want project A %q", got, normalizeProjectRoot(projectA))
	}
	if got := normalizeProjectRoot(tab.Ctrl.WorkspaceRoot()); got != normalizeProjectRoot(projectA) {
		t.Fatalf("controller workspace root = %q, want project A %q", got, normalizeProjectRoot(projectA))
	}
}

func TestSetEffortForTabRebuildsController(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	old := newCurrentRebuildController(t, app, control.Options{Label: "old-controller"})
	app.setTestCtrl(old, "deepseek-flash/deepseek-flash")
	setCurrentRebuildTabIdentity(t, app, app.activeTab(), old)
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	if err := app.SetEffortForTab("", "max"); err != nil {
		t.Fatalf("SetEffortForTab(max): %v", err)
	}
	if c := app.activeCtrl(); c == nil {
		t.Fatal("SetEffortForTab should leave a rebuilt controller")
	}
	if c := app.activeCtrl(); c == old {
		t.Fatal("SetEffortForTab should rebuild the active controller so the provider sees the new effort")
	}
	if got := app.Effort().Current; got != "max" {
		t.Fatalf("Effort current = %q, want max", got)
	}
}

func TestSetEffortForTabRejectsRunningTurn(t *testing.T) {
	isolateDesktopUserDirs(t)

	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Runner: runner}), "")
	app.activeCtrl().Submit("work")
	<-runner.started

	err := app.SetEffortForTab("", "max")
	if err == nil || !strings.Contains(err.Error(), "finish or cancel") {
		t.Fatalf("SetEffortForTab while running error = %v, want finish/cancel guard", err)
	}

	close(runner.release)
	waitNotRunning(t, app.activeCtrl())
}
