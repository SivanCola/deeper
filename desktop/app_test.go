package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deepzora/internal/agent"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/event"
	"deepzora/internal/evidence"
	"deepzora/internal/history"
	"deepzora/internal/plugin"
	"deepzora/internal/session"
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

func TestConfigChangeWriterOwnedPersistsAndDefersRefresh(t *testing.T) {
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

	oldExec := agent.New(nil, nil, agent.NewSession("old system prompt"), agent.Options{}, event.Discard)
	base := newCurrentRebuildController(t, app, control.Options{Executor: oldExec, Label: "old", Sink: event.Discard})
	oldCtrl := &snapshotErrorSessionController{SessionAPI: base, err: session.ErrWriterOwned}

	tab := &WorkspaceTab{
		ID:          "tab_settings",
		Scope:       "global",
		Ready:       true,
		model:       "old/old-model",
		Ctrl:        oldCtrl,
		sink:        &tabEventSink{tabID: "tab_settings", app: app},
		disabledMCP: map[string]ServerView{},
	}
	setCurrentRebuildTabIdentity(t, app, tab, base)
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
