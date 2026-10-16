package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/evidence"
)

func TestCanonicalTodosMetaWireContract(t *testing.T) {
	if got := ctrlTodos(nil); got != nil {
		t.Fatalf("nil controller todos = %+v, want unavailable", *got)
	}

	empty := Meta{CanonicalTodos: ctrlTodos(&todoMetaController{})}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty canonical todos: %v", err)
	}
	if !strings.Contains(string(raw), `"canonicalTodos":[]`) {
		t.Fatalf("empty canonical todos must encode as an authoritative empty array: %s", raw)
	}

	ctrl := &todoMetaController{todos: []evidence.TodoItem{{Content: "Ship", Status: "completed"}}}
	got := ctrlTodos(ctrl)
	if got == nil || len(*got) != 1 || (*got)[0].Status != "completed" {
		t.Fatalf("canonical todos = %+v, want completed task", got)
	}

	unavailable, err := json.Marshal(Meta{CanonicalTodos: ctrlTodos(nil)})
	if err != nil {
		t.Fatalf("marshal unavailable canonical todos: %v", err)
	}
	if strings.Contains(string(unavailable), "canonicalTodos") {
		t.Fatalf("unavailable canonical todos should preserve the legacy fallback contract: %s", unavailable)
	}
}

func TestMetaForTabIncludesWorkspaceContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolateDesktopUserDirs(t)
	resetWorkspaceGitBranchMetaCacheForTest(t)

	repo := t.TempDir()
	configuredSandboxRoot := filepath.Join(t.TempDir(), "sandbox")
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Sandbox.WorkspaceRoot = configuredSandboxRoot
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	runGit(t, "init")
	runGit(t, "checkout", "-b", "feature/meta")

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"tab-1": {
		ID:            "tab-1",
		Scope:         "project",
		WorkspaceRoot: repo,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}}
	app.activeTabID = "tab-1"

	got := app.MetaForTab("tab-1")
	if got.Cwd != repo || got.WorkspaceRoot != repo || got.WorkspacePath != repo {
		t.Fatalf("workspace fields = cwd:%q root:%q path:%q, want %q", got.Cwd, got.WorkspaceRoot, got.WorkspacePath, repo)
	}
	if got.WorkspaceName != filepath.Base(repo) {
		t.Fatalf("workspaceName = %q, want %q", got.WorkspaceName, filepath.Base(repo))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if strings.Contains(string(raw), "sandboxPath") || strings.Contains(string(raw), configuredSandboxRoot) {
		t.Fatalf("meta should not expose configured sandbox root as sandboxPath: %s", raw)
	}
	// The first git process launch can be noticeably slower on Windows runners
	// while MetaForTab intentionally keeps the caller path non-blocking.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got = app.MetaForTab("tab-1"); got.GitBranch == "feature/meta" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gitBranch = %q, want feature/meta after async refresh", got.GitBranch)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestListTabsDoesNotExposeConfiguredSandboxPath(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	configuredSandboxRoot := filepath.Join(t.TempDir(), "sandbox")
	cfg := config.LoadForEdit(config.UserConfigPath())
	cfg.Sandbox.WorkspaceRoot = configuredSandboxRoot
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"tab-1": {
		ID:            "tab-1",
		Scope:         "project",
		WorkspaceRoot: workspace,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}}
	app.activeTabID = "tab-1"
	app.tabOrder = []string{"tab-1"}

	raw, err := json.Marshal(app.ListTabs())
	if err != nil {
		t.Fatalf("marshal tabs: %v", err)
	}
	if strings.Contains(string(raw), "sandboxPath") || strings.Contains(string(raw), configuredSandboxRoot) {
		t.Fatalf("tab metadata should not expose configured sandbox root as sandboxPath: %s", raw)
	}
}

func TestListTabsExposesStructuredRuntimeStatus(t *testing.T) {
	asks := make(chan event.Ask, 1)
	done := make(chan event.Event, 1)
	runner := &desktopAskRuntimeRunner{}
	ctrl := control.New(control.Options{
		Runner: runner,
		Sink: event.FuncSink(func(e event.Event) {
			switch e.Kind {
			case event.AskRequest:
				asks <- e.Ask
			case event.TurnDone:
				done <- e
			}
		}),
	})
	runner.ask = func(ctx context.Context) error {
		_, err := ctrl.Ask(ctx, []event.AskQuestion{{
			ID:      "choice",
			Prompt:  "Pick one",
			Options: []event.AskOption{{Label: "A"}, {Label: "B"}},
		}})
		return err
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "prov/model")
	app.tabOrder = []string{"test"}
	ctrl.Send("ask user")
	select {
	case <-asks:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ask request")
	}

	tabs := app.ListTabs()
	if len(tabs) != 1 {
		t.Fatalf("tabs = %d, want 1", len(tabs))
	}
	if !tabs[0].Running || !tabs[0].PendingPrompt || !tabs[0].Cancellable || tabs[0].CancelRequested {
		t.Fatalf("tab runtime = running:%v pending:%v cancellable:%v cancel:%v", tabs[0].Running, tabs[0].PendingPrompt, tabs[0].Cancellable, tabs[0].CancelRequested)
	}

	app.CancelTab("test")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for turn_done")
	}
}

func TestMetaForTabLeavesGitBranchEmptyOutsideGit(t *testing.T) {
	isolateDesktopUserDirs(t)
	workspace := t.TempDir()
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{"tab-1": {
		ID:            "tab-1",
		Scope:         "project",
		WorkspaceRoot: workspace,
		Ready:         true,
		disabledMCP:   map[string]ServerView{},
	}}
	app.activeTabID = "tab-1"

	if got := app.MetaForTab("tab-1"); got.GitBranch != "" {
		t.Fatalf("gitBranch = %q, want empty", got.GitBranch)
	}
}

// A config file written before the classic style was removed still holds the
// retired value. It must read as workbench — not fail, and not resurrect the
// multi-tab model that value used to select, which the UI can no longer show.
func TestRetiredClassicLayoutReadsAsWorkbench(t *testing.T) {
	isolateDesktopUserDirs(t)
	if err := editUserConfig(func(c *config.Config) error {
		c.Desktop.LayoutStyle = "workbench"
		return nil
	}); err != nil {
		t.Fatalf("write the retired layout style: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.DesktopLayoutStyle(); got != "workbench" {
		t.Fatalf("retired classic reads as %q, want workbench", got)
	}
}

// assertTabBuildSuperseded checks that the startup build registered before the
// mutation (generation) can no longer install its controller and that its
// build context was cancelled.
func assertTabBuildSuperseded(t *testing.T, app *App, tab *WorkspaceTab, generation uint64, buildCtx context.Context) {
	t.Helper()
	app.mu.Lock()
	superseded := app.tabBuildSupersededLocked(tab, generation)
	app.mu.Unlock()
	if !superseded {
		t.Fatal("in-flight startup build was not superseded; finishing it would reinstall a stale controller")
	}
	select {
	case <-buildCtx.Done():
	default:
		t.Fatal("in-flight startup build context was not cancelled")
	}
	if tab.buildCancel != nil {
		t.Fatal("build cancel was not cleared")
	}
}

func TestMetaForTabReportsImageInputCapability(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "CUSTOM_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "custom/text-only"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{
		Name:           "custom",
		Kind:           "openai",
		BaseURL:        "https://example.invalid/v1",
		APIKeyEnv:      "CUSTOM_KEY",
		Models:         []string{"text-only", "vision-pro"},
		ModelOverrides: map[string]config.ProviderModelOverride{"vision-pro": {Vision: new(true)}, "text-only": {Vision: new(false)}},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	exec := agent.New(nil, nil, agent.NewSession("system"), agent.Options{}, event.Discard)
	ctrl := newCurrentRebuildController(t, app, control.Options{Executor: exec, Label: "custom/text-only", ModelRef: "custom/text-only"})
	app.setTestCtrl(ctrl, "custom/text-only")
	setCurrentRebuildTabIdentity(t, app, app.activeTab(), ctrl)
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	if got := app.Meta().ImageInputEnabled; got {
		t.Fatal("text-only meta should disable image input")
	}
	if err := app.SetModel("custom/vision-pro"); err != nil {
		t.Fatalf("SetModel(custom/vision-pro): %v", err)
	}
	// ImageInputEnabled is served from the per-tab cache; the model change
	// invalidates it and a background refresh repopulates it (tab:meta).
	waitForMetaImageInput(t, app, true)
}

// waitForMetaImageInput polls until the cached image-input capability reaches
// the expected value. MetaForTab serves the background-refreshed cache, so the
// value flips asynchronously after a model/settings change.
func waitForMetaImageInput(t *testing.T, app *App, want bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if app.Meta().ImageInputEnabled == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Meta().ImageInputEnabled did not become %v", want)
}

func TestMetaForTabImageInputCapabilityUsesCurrentRef(t *testing.T) {
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "CUSTOM_KEY", "sk-test")

	cfg := config.Default()
	cfg.DefaultModel = "custom/vision-pro"
	cfg.Desktop.ProviderAccess = []string{"custom"}
	cfg.Providers = []config.ProviderEntry{{
		Name:           "custom",
		Kind:           "openai",
		BaseURL:        "https://example.invalid/v1",
		APIKeyEnv:      "CUSTOM_KEY",
		Models:         []string{"text-only", "vision-pro"},
		ModelOverrides: map[string]config.ProviderModelOverride{"vision-pro": {Vision: new(true)}, "text-only": {Vision: new(false)}},
	}}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	app := NewApp()
	app.ctx = context.Background()
	app.readyHook = func() {}
	app.setTestCtrl(control.New(control.Options{Label: "deleted/model"}), "deleted/model")
	defer func() {
		if c := app.activeCtrl(); c != nil {
			c.Close()
		}
	}()

	if got := app.Meta().ImageInputEnabled; got {
		t.Fatal("unknown model ref should not inherit image input from the default fallback model")
	}
}

func TestBeginTabTurnRejectsWorkspaceMismatchWithoutReplacingRuntime(t *testing.T) {
	isolateDesktopUserDirs(t)
	app := NewApp()
	root := t.TempDir()
	ctrl := newCurrentRebuildController(t, app, control.Options{WorkspaceRoot: root, Sink: event.Discard})
	tab := &WorkspaceTab{ID: "workspace-guard", Scope: "project", WorkspaceRoot: root, Ctrl: ctrl, Ready: true}
	setCurrentRebuildTabIdentity(t, app, tab, ctrl)
	app.tabs[tab.ID] = tab
	ref, _ := ctrl.SessionRef()
	tab.WorkspaceRoot = t.TempDir()
	admission, _, err := app.beginTabTurn(tab.ID, false)
	if admission != nil {
		admission.abort()
		t.Fatal("mismatched workspace admitted a turn")
	}
	if !errors.Is(err, errSessionWorkspaceConflict) {
		t.Fatalf("workspace mismatch error=%v", err)
	}
	if tab.Ctrl != ctrl || !tab.Ready || ctrl.WorkspaceRoot() != root {
		t.Fatal("validation rewrote the current runtime")
	}
	if actual, _ := ctrl.SessionRef(); actual != ref {
		t.Fatal("validation changed session identity")
	}
	if !app.runtimeRebuildMu.TryLock() {
		t.Fatal("validation retained rebuild lock")
	}
	defer app.runtimeRebuildMu.Unlock()
	if !app.runtimeAdmissionMu.TryLock() {
		t.Fatal("validation retained admission lock")
	}
	app.runtimeAdmissionMu.Unlock()
}

func TestCloseTabWaitsForPendingTurnAdmission(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	admission, _, err := fixture.app.beginTabTurn("active", false)
	if err != nil {
		t.Fatalf("beginTabTurn(active): %v", err)
	}
	released := false
	defer func() {
		if !released {
			admission.abort()
		}
	}()

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.app.CloseTab("active") }()
	select {
	case err := <-closeDone:
		admission.abort()
		released = true
		t.Fatalf("CloseTab bypassed a pending turn admission and closed its controller: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	admission.abort()
	released = true
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseTab(active) after turn admission release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloseTab did not resume after the pending turn admission was released")
	}
}

type recordingTabShellController struct {
	*control.Controller
	commands []string
	cancels  int
}

func (c *recordingTabShellController) RunShell(command string) {
	c.commands = append(c.commands, command)
}

func (c *recordingTabShellController) Cancel() { c.cancels++ }

func TestRunShellForTabRoutesToRequestedTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	// Routing is a port contract, independent of host shell startup/completion.
	// Real execution and cancellation remain covered by shell integration tests.
	activeCtrl := &recordingTabShellController{Controller: control.New(control.Options{})}
	inactiveCtrl := &recordingTabShellController{Controller: control.New(control.Options{})}
	defer activeCtrl.Close()
	defer inactiveCtrl.Close()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"active":   {ID: "active", Scope: "global", Ctrl: activeCtrl, Ready: true},
			"inactive": {ID: "inactive", Scope: "global", Ctrl: inactiveCtrl, Ready: true},
		},
		tabOrder:    []string{"active", "inactive"},
		activeTabID: "active",
	}

	if err := app.RunShellForTab("inactive", "echo route-test"); err != nil {
		t.Fatalf("RunShellForTab: %v", err)
	}
	if len(inactiveCtrl.commands) != 1 || inactiveCtrl.commands[0] != "echo route-test" {
		t.Fatalf("inactive tab commands = %q, want the exact shell command once", inactiveCtrl.commands)
	}
	if len(activeCtrl.commands) != 0 {
		t.Fatalf("active tab received inactive shell commands: %q", activeCtrl.commands)
	}
}

func TestRunShellForTabStaysBoundDuringRapidProjectTabSwitching(t *testing.T) {
	isolateDesktopUserDirs(t)

	projectA := t.TempDir()
	projectB := t.TempDir()
	globalRoot := t.TempDir()
	// Exercise tab ownership independently of host shell startup and filesystem
	// latency. Real shell execution/cancellation is covered in internal/control.
	shellCtrl := &recordingTabShellController{Controller: control.New(control.Options{WorkspaceRoot: projectA})}
	projectCtrl := &recordingTabShellController{Controller: control.New(control.Options{WorkspaceRoot: projectB})}
	globalCtrl := &recordingTabShellController{Controller: control.New(control.Options{WorkspaceRoot: globalRoot})}
	defer shellCtrl.Close()
	defer projectCtrl.Close()
	defer globalCtrl.Close()

	app := &App{
		tabs: map[string]*WorkspaceTab{
			"shell":     {ID: "shell", Scope: "project", WorkspaceRoot: projectA, Ctrl: shellCtrl, Ready: true},
			"project-b": {ID: "project-b", Scope: "project", WorkspaceRoot: projectB, Ctrl: projectCtrl, Ready: true},
			"global":    {ID: "global", Scope: "global", WorkspaceRoot: globalRoot, Ctrl: globalCtrl, Ready: true},
		},
		tabOrder:    []string{"shell", "project-b", "global"},
		activeTabID: "shell",
	}

	command := "echo shell-route-test"
	if err := app.RunShellForTab("shell", command); err != nil {
		t.Fatalf("RunShellForTab: %v", err)
	}

	for range 8 {
		if err := app.SetActiveTab("project-b"); err != nil {
			t.Fatalf("SetActiveTab(project-b): %v", err)
		}
		if err := app.SetActiveTab("global"); err != nil {
			t.Fatalf("SetActiveTab(global): %v", err)
		}
		if err := app.SetActiveTab("shell"); err != nil {
			t.Fatalf("SetActiveTab(shell): %v", err)
		}
	}
	if err := app.SetActiveTab("project-b"); err != nil {
		t.Fatalf("SetActiveTab(project-b final): %v", err)
	}
	app.CancelTab("shell")

	if len(shellCtrl.commands) != 1 || shellCtrl.commands[0] != command || shellCtrl.cancels != 1 {
		t.Fatalf("shell tab commands = %q, cancels = %d, want the exact command and one cancellation", shellCtrl.commands, shellCtrl.cancels)
	}
	if len(projectCtrl.commands) != 0 || projectCtrl.cancels != 0 || len(globalCtrl.commands) != 0 || globalCtrl.cancels != 0 {
		t.Fatal("shell execution or cancellation was routed to another tab")
	}
	if got := activeTabIDForTest(app); got != "project-b" {
		t.Fatalf("active tab = %q, want project-b after background shell cancel", got)
	}
}

func activeTabIDForTest(app *App) string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.activeTabID
}
