package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/bot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/evidence"
	"deepzora/internal/history"
	"deepzora/internal/plugin"
	"deepzora/internal/provider"
	"deepzora/internal/sandbox"
	"deepzora/internal/stats"
	"deepzora/internal/taskcatalog"
	"deepzora/internal/tool"
)

type todoMetaController struct {
	stubSessionAPI
	todos []evidence.TodoItem
}

func (c *todoMetaController) Todos() []evidence.TodoItem {
	return append([]evidence.TodoItem(nil), c.todos...)
}

// setTestCtrl creates a minimal workspace tab (if needed) and sets its
// controller, so tests don't depend on the old App.ctrl field.
func (a *App) setTestCtrl(ctrl control.SessionAPI, model string) {
	if len(a.tabs) == 0 {
		tab := &WorkspaceTab{
			ID:          "test",
			Scope:       "global",
			Ready:       true,
			disabledMCP: map[string]ServerView{},
		}
		a.tabs = map[string]*WorkspaceTab{"test": tab}
		a.activeTabID = "test"
	}
	tab := a.tabs["test"]
	tab.Ctrl = ctrl
	a.bindControllerDisplayRecorder(ctrl)
	tab.model = model
}

func isolateDesktopUserDirs(t *testing.T) string {
	t.Helper()
	home := robustTempDir(t)
	xdg := filepath.Join(home, ".config")
	appData := filepath.Join(home, "AppData")
	for _, dir := range []string{xdg, appData} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("DEEPZORA_CREDENTIALS_STORE", "file")
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("DEEPZORA_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("DEEPZORA_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("AppData", appData)
	// Close process-local SQLite handles before TempDir cleanup for Windows.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		desktopTopicState.close()
		_ = history.CloseSharedCatalog(ctx)
		_ = stats.CloseUsageCatalogs(ctx)
		_ = taskcatalog.ShutdownShared(ctx)
	})
	return home
}

func providerNamesFromView(providers []ProviderView) []string {
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		out = append(out, p.Name)
	}
	return out
}

func modelRefsFromView(models []ModelInfo) map[string]bool {
	out := map[string]bool{}
	for _, m := range models {
		out[m.Ref] = true
	}
	return out
}

type desktopFakeTool struct {
	name string
}

func (t desktopFakeTool) Name() string { return t.name }

func (desktopFakeTool) Description() string { return "fake desktop tool" }

func (desktopFakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (desktopFakeTool) Execute(context.Context, json.RawMessage) (string, error) { return "", nil }

func (desktopFakeTool) ReadOnly() bool { return true }

type desktopAskRuntimeRunner struct {
	ask func(context.Context) error
}

func (r *desktopAskRuntimeRunner) Run(ctx context.Context, _ string) error {
	if r.ask == nil {
		return nil
	}
	return r.ask(ctx)
}

func commandInfoByName(commands []CommandInfo, name string) (CommandInfo, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return CommandInfo{}, false
}

func TestConfigChangeLeaseHeldPersistsAndDefersRefresh(t *testing.T) {
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

	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	sessionPath := filepath.Join(dir, "externally-leased-settings.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatalf("write placeholder session: %v", err)
	}
	externalLease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("TryAcquireSessionLease: %v", err)
	}
	defer externalLease.Release()

	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{Executor: oldExec, SessionDir: dir, SessionPath: sessionPath, Label: "old", Sink: event.Discard})
	defer oldCtrl.Close()

	app := NewApp()
	app.ctx = context.Background()
	tab := &WorkspaceTab{
		ID:          "tab_settings",
		Scope:       "global",
		SessionPath: sessionPath,
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_settings", app: app},
		disabledMCP: map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID

	if err := app.SetMaxSubagentDepth(1); err != nil {
		t.Fatalf("SetMaxSubagentDepth should defer lease-held refresh instead of failing: %v", err)
	}
	if tab.Ctrl != oldCtrl {
		t.Fatalf("tab controller changed after deferred settings rebuild")
	}
	got := config.LoadForEditWithoutCredentials(config.UserConfigPath())
	if got.Agent.MaxSubagentDepth != 1 {
		t.Fatalf("saved max_subagent_depth = %d, want 1", got.Agent.MaxSubagentDepth)
	}
}

type staleWorkspaceBindingFixture struct {
	app          *App
	tab          *WorkspaceTab
	oldCtrl      control.SessionAPI
	projectA     string
	sessionDirA  string
	sessionPathA string
}

func newStaleWorkspaceBindingFixture(t *testing.T, suffix string) staleWorkspaceBindingFixture {
	return newStaleWorkspaceBindingFixtureWithLayout(t, suffix, "")
}

func newStaleWorkspaceBindingFixtureWithLayout(t *testing.T, suffix, layoutStyle string) staleWorkspaceBindingFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	setDesktopTestCredential(t, "TEST_MODEL_KEY", "sk-test")

	// Submitted turns use a real provider, so the fixture must complete them
	// instantly instead of pointing at an unreachable host.
	providerStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(providerStub.Close)

	cfg := config.Default()
	cfg.DefaultModel = "test/test-model"
	cfg.Desktop.ProviderAccess = []string{"test"}
	cfg.Providers = []config.ProviderEntry{
		{Models: []string{"test-model"}, Default: "test-model", Name: "test", Kind: "openai", BaseURL: providerStub.URL, Model: "test-model", APIKeyEnv: "TEST_MODEL_KEY"},
	}
	if strings.TrimSpace(layoutStyle) != "" {
		if err := cfg.SetDesktopLayoutStyle(layoutStyle); err != nil {
			t.Fatalf("set desktop layout style: %v", err)
		}
	}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	projectA := t.TempDir()
	projectB := t.TempDir()
	if err := addProject(projectA, "Project A"); err != nil {
		t.Fatalf("add project A: %v", err)
	}
	if err := addProject(projectB, "Project B"); err != nil {
		t.Fatalf("add project B: %v", err)
	}

	topicID := "topic_" + suffix
	topicTitle := "Rebuild workspace " + suffix
	sessionDirA := desktopSessionDir(projectA)
	sessionDirB := desktopSessionDir(projectB)
	if err := os.MkdirAll(sessionDirA, 0o755); err != nil {
		t.Fatalf("mkdir project A sessions: %v", err)
	}
	if err := os.MkdirAll(sessionDirB, 0o755); err != nil {
		t.Fatalf("mkdir project B sessions: %v", err)
	}
	sessionPathA := writeTopicSessionWithPrompt(t, sessionDirA, "project-a.jsonl", topicID, topicTitle, projectA, "project A prompt", time.Now())
	sessionPathB := filepath.Join(sessionDirB, "wrong.jsonl")

	oldSession := agent.NewSession("old system prompt")
	oldSession.Add(provider.Message{Role: provider.RoleUser, Content: "carry me"})
	oldExec := agent.New(nil, nil, oldSession, agent.Options{}, event.Discard)
	oldCtrl := control.New(control.Options{
		Executor:      oldExec,
		SessionDir:    sessionDirB,
		SessionPath:   sessionPathB,
		Label:         "test/test-model",
		ModelRef:      "test/test-model",
		WorkspaceRoot: projectB,
		Sink:          event.Discard,
	})

	app := NewApp()
	app.readyHook = func() {}
	tab := &WorkspaceTab{
		ID:            "tab_stale_workspace_" + suffix,
		Scope:         "project",
		WorkspaceRoot: projectB,
		TopicID:       topicID,
		TopicTitle:    topicTitle,
		SessionPath:   sessionPathA,
		Ready:         true,
		model:         "test/test-model",
		Ctrl:          oldCtrl,
		sink:          &tabEventSink{tabID: "tab_stale_workspace_" + suffix, app: app},
		disabledMCP:   map[string]ServerView{},
	}
	app.tabs = map[string]*WorkspaceTab{tab.ID: tab}
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	t.Cleanup(func() {
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
		}
	})

	return staleWorkspaceBindingFixture{
		app:          app,
		tab:          tab,
		oldCtrl:      oldCtrl,
		projectA:     projectA,
		sessionDirA:  sessionDirA,
		sessionPathA: sessionPathA,
	}
}

type blockingSnapshotCtrl struct {
	control.SessionAPI

	firstSnapshotStarted  chan struct{}
	secondSnapshotStarted chan struct{}
	releaseSnapshot       chan struct{}
	firstOnce             sync.Once
	secondOnce            sync.Once
	snapshotCount         atomic.Int32
	closeCount            atomic.Int32
}

func newBlockingSnapshotCtrl(ctrl control.SessionAPI) *blockingSnapshotCtrl {
	return &blockingSnapshotCtrl{
		SessionAPI:            ctrl,
		firstSnapshotStarted:  make(chan struct{}),
		secondSnapshotStarted: make(chan struct{}),
		releaseSnapshot:       make(chan struct{}),
	}
}

func (c *blockingSnapshotCtrl) Snapshot() error {
	count := c.snapshotCount.Add(1)
	switch count {
	case 1:
		c.firstOnce.Do(func() { close(c.firstSnapshotStarted) })
	case 2:
		c.secondOnce.Do(func() { close(c.secondSnapshotStarted) })
	}
	<-c.releaseSnapshot
	if c.SessionAPI == nil {
		return nil
	}
	return c.SessionAPI.Snapshot()
}

func (c *blockingSnapshotCtrl) Close() {
	c.closeCount.Add(1)
	if c.SessionAPI != nil {
		c.SessionAPI.Close()
	}
}

type appendingDesktopRunner struct {
	session *agent.Session
	started chan string
}

func (r *appendingDesktopRunner) Run(_ context.Context, input string) error {
	r.started <- input
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	r.session.Add(provider.Message{Role: provider.RoleAssistant, Content: "ok"})
	return nil
}

type gatedDesktopMCPLaunchFixture struct {
	app              *App
	sharedHost       *plugin.Host
	activeRegistry   *tool.Registry
	siblingRegistry  *tool.Registry
	disabledRegistry *tool.Registry
}

// waitForRuntimeAdmissionBarrier polls until the work-admission write lock is
// held, marking the point where a lifecycle mutation froze new admissions.
func waitForRuntimeAdmissionBarrier(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if app.runtimeAdmissionMu.TryRLock() {
			app.runtimeAdmissionMu.RUnlock()
			time.Sleep(2 * time.Millisecond)
			continue
		}
		return
	}
	t.Fatal("lifecycle mutation never acquired the work-admission barrier")
}

func TestBridgeDriveReleasesRuntimeAdmissionWhenTakeoverWasReclaimed(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	fixture.app.tabs["active"].sink = &tabEventSink{tabID: "active", app: fixture.app}
	fixture.app.botBridge = &botBridgeHub{
		takeovers:    make(map[string]bot.DesktopWatchRoute),
		takeoverTabs: make(map[string]string),
	}

	err := fixture.app.bridgeDrive("active", "hello", bot.DesktopWatchRoute{})
	if err == nil || !strings.Contains(err.Error(), "接管已解除") {
		t.Fatalf("bridgeDrive error = %v, want lost-takeover error", err)
	}
	if !fixture.app.runtimeAdmissionMu.TryLock() {
		t.Fatal("bridgeDrive leaked the runtime-admission read lock")
	}
	fixture.app.runtimeAdmissionMu.Unlock()
}

func TestCapabilitiesMarksAuthFailureRequired(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "figma"
type = "http"
url = "https://mcp.figma.com/mcp"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)
	enableProjectMCPForWorkspace(t, dir, "")

	host := plugin.NewHost()
	host.RecordFailure(plugin.Spec{Name: "figma", Type: "http", URL: "https://mcp.figma.com/mcp"}, errors.New("connect: 401 unauthorized"))
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: host}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "figma" {
			if s.Status != "failed" || s.AuthStatus != "required" || s.AuthURL != "https://mcp.figma.com/mcp" {
				t.Fatalf("figma auth diagnosis = %+v", s)
			}
			return
		}
	}
	t.Fatalf("figma MCP missing from Capabilities: %+v", view.Servers)
}

func longRunningMarkerCommand(marker string) string {
	if sandbox.ResolveShell("", "", nil).Kind == sandbox.ShellPowerShell {
		return fmt.Sprintf("Set-Content -LiteralPath %s -Value shell; Start-Sleep -Seconds 30", marker)
	}
	return fmt.Sprintf("printf shell > %s; sleep 30", marker)
}

func waitForShellDispatch(t *testing.T, ch <-chan event.Event, marker string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == event.ToolDispatch && strings.Contains(e.Tool.Args, marker) {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for shell dispatch")
		}
	}
}

func assertNoEvents(t *testing.T, ch <-chan event.Event, name string) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("%s received event while shell ran in another tab: %+v", name, e)
	default:
	}
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, _ string) error {
	close(r.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.release:
		return nil
	}
}

func hasLevel(levels []string, want string) bool {
	return slices.Contains(levels, want)
}

func hasCommand(cmds []CommandInfo, name string) bool {
	for _, cmd := range cmds {
		if cmd.Name == name {
			return true
		}
	}
	return false
}

func hasDirEntry(entries []DirEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

// Prompt history scanning tests

func identityPromptDisplay(text string) string { return text }

func resolveTestModelCapability(resolver *config.ModelCapabilityResolver, entry config.ProviderEntry, model string) config.ResolvedModelCapability {
	entry.Model = model
	return resolver.Resolve(&entry)
}
