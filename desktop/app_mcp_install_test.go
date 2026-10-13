package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

func TestDesktopSharedHostProjectMCPConnectsWithoutLaunchApproval(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping background MCP boot integration test in short mode")
	}

	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
type = "http"
url = %q
`, srv.URL), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)
	enableProjectMCPForTest(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sharedHost := plugin.NewHost()
	defer sharedHost.Close()
	ctrl, err := boot.Build(ctx, boot.Options{
		WorkspaceRoot: dir,
		SessionDir:    filepath.Join(dir, "sessions"),
		SharedHost:    sharedHost,
		Stderr:        io.Discard,
	})
	if err != nil {
		t.Fatalf("boot.Build: %v", err)
	}
	defer ctrl.Close()

	deadline := time.Now().Add(3 * time.Second)
	for !sharedHost.HasClient("h") && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if !sharedHost.HasClient("h") {
		t.Fatalf("project MCP did not connect automatically; failures=%+v", sharedHost.Failures())
	}
	for _, failure := range sharedHost.Failures() {
		if failure.Name == "h" && failure.RequiresLaunchApproval {
			t.Fatalf("project MCP unexpectedly requested launch approval: %+v", failure)
		}
	}

	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"test": {
			ID:            "test",
			Scope:         "global",
			WorkspaceRoot: dir,
			Ready:         true,
			Ctrl:          ctrl,
			SharedHostKey: dir,
			disabledMCP:   map[string]ServerView{},
		},
	}
	app.activeTabID = "test"

	view := app.MCPServers()
	if len(view) != 1 || view[0].Name != "h" || view[0].Status != "connected" || view[0].RuntimeState != "ready" || view[0].RequiresLaunchApproval {
		t.Fatalf("MCPServers() = %+v, want trusted connected project h", view)
	}
}

func TestProjectMCPViewIsTrustedAndKeepsProjectSource(t *testing.T) {
	entry := config.PluginEntry{Name: "project", Source: config.MCPSourceProjectConfig}
	connected := withPluginConfig(ServerView{Name: entry.Name, Status: "connected"}, entry)
	if connected.RequiresLaunchApproval {
		t.Fatalf("connected project MCP still requires launch approval: %+v", connected)
	}
	blocked := withPluginConfig(ServerView{
		Name: entry.Name, Status: "failed", RequiresLaunchApproval: true,
	}, entry)
	if blocked.RequiresLaunchApproval {
		t.Fatalf("project MCP exposed obsolete launch approval action: %+v", blocked)
	}
	if blocked.Source != "project" || blocked.ConfigSource != "deepzora.toml" {
		t.Fatalf("blocked project MCP source = %q/%q, want project/deepzora.toml", blocked.Source, blocked.ConfigSource)
	}

	user := withPluginConfig(ServerView{Name: "user", Status: "connected"},
		config.PluginEntry{Name: "user", Source: config.MCPSourceUserConfig})
	if user.RequiresLaunchApproval {
		t.Fatalf("user-config MCP must not be launch-gate governed: %+v", user)
	}
	if user.Source != "user" || user.ConfigSource != "config.toml" {
		t.Fatalf("user MCP source = %q/%q, want user/config.toml", user.Source, user.ConfigSource)
	}
}

func TestMCPServersMatchesCapabilitiesServerProjection(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "playwright"
command = "npx"
args = ["-y", "@playwright/mcp"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	if got, want := app.MCPServers(), app.Capabilities().Servers; !reflect.DeepEqual(got, want) {
		t.Fatalf("MCPServers() = %+v, want Capabilities().Servers %+v", got, want)
	}
}

func TestConfiguredMCPWithFormerBuiltInNameIsUserServer(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "time"
command = "custom-time"
args = ["serve"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	found := false
	for _, s := range view.Servers {
		if s.Name != "time" {
			continue
		}
		found = true
		if s.BuiltIn || !s.Configured || s.Command != "custom-time" || !reflect.DeepEqual(s.Args, []string{"serve"}) {
			t.Fatalf("configured time view = %+v, want ordinary user MCP config", s)
		}
	}
	if !found {
		t.Fatalf("configured time server missing from Capabilities: %+v", view.Servers)
	}

	if err := app.SetMCPServerEnabled("time", false); err != nil {
		t.Fatalf("SetMCPServerEnabled(time,false): %v", err)
	}
	view = app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "time" {
			if s.Status != "disabled" || s.BuiltIn || s.Command != "custom-time" {
				t.Fatalf("disabled configured time view = %+v, want disabled external config", s)
			}
			return
		}
	}
	t.Fatalf("time missing after disable: %+v", view.Servers)
}

func TestEditAndRemoveConfiguredMCPWithBuiltInName(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "time"
command = "custom-time"
args = ["serve"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	app.activeTab().disabledMCP["time"] = ServerView{Name: "time", Status: "disabled", Enabled: false}

	if err := app.UpdateMCPServer("time", MCPServerInput{
		Name:      "time",
		Transport: "stdio",
		Command:   "updated-time",
		Args:      []string{"run"},
	}); err != nil {
		t.Fatalf("UpdateMCPServer(time): %v", err)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := findPluginEntry(cfg.Plugins, "time")
	if !ok || updated.Command != "updated-time" || !reflect.DeepEqual(updated.Args, []string{"run"}) {
		t.Fatalf("updated time plugin = %+v, found=%v", updated, ok)
	}

	if err := app.RemoveMCPServer("time"); err != nil {
		t.Fatalf("RemoveMCPServer(time): %v", err)
	}
	cfg, err = config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "time"); ok {
		t.Fatalf("time plugin still configured after remove: %+v", cfg.Plugins)
	}
}

func TestRemoveProjectMCPRevealsAndRegistersGlobalFallback(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Plugins = []config.PluginEntry{{Name: "docs", Command: "global-docs"}}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(dir, "deepzora.toml")
	if err := os.WriteFile(projectPath, []byte(`
[[plugins]]
name = "docs"
command = "project-docs"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tool.NewRegistry()
	var configured []plugin.Spec
	ctrl := control.New(control.Options{
		Host:          plugin.NewHost(),
		Registry:      reg,
		WorkspaceRoot: dir,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			configured = append(configured, *spec)
		},
	})
	defer ctrl.Close()
	projectEntry, found, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !found {
		t.Fatalf("load project docs: entry=%+v found=%v err=%v", projectEntry, found, err)
	}
	if _, err := ctrl.RegisterMCPServerOnDemand(projectEntry); err != nil {
		t.Fatalf("register project docs: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.activeTab().WorkspaceRoot = dir
	if err := app.RemoveMCPServer("docs"); err != nil {
		t.Fatalf("RemoveMCPServer(docs): %v", err)
	}

	projectCfg := config.LoadForEdit(projectPath)
	if _, found := findPluginEntry(projectCfg.Plugins, "docs"); found {
		t.Fatalf("project docs still configured after removal: %+v", projectCfg.Plugins)
	}
	globalCfg := config.LoadForEdit(config.UserConfigPath())
	globalEntry, found := findPluginEntry(globalCfg.Plugins, "docs")
	if !found || globalEntry.Command != "global-docs" {
		t.Fatalf("global docs fallback = %+v, found=%v", globalEntry, found)
	}
	effective, found, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !found || effective.Source != config.MCPSourceUserConfig || effective.Command != "global-docs" {
		t.Fatalf("effective docs fallback = %+v, found=%v err=%v", effective, found, err)
	}
	if len(configured) < 2 || configured[len(configured)-1].Command != "global-docs" {
		t.Fatalf("registered specs = %+v, want global fallback registered last", configured)
	}
	if _, found := reg.Get("mcp__docs__connect"); !found {
		t.Fatalf("global fallback connect surface missing; names=%v", reg.Names())
	}
}

func TestRemoveMCPServerDeletesProjectMCPJSONEntry(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{
  "mcpServers": {
    "codegraph": { "command": "codegraph", "args": ["serve", "--mcp"] },
    "keep": { "command": "keep-mcp" }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	if err := app.RemoveMCPServer("codegraph"); err != nil {
		t.Fatalf("RemoveMCPServer(.mcp.json codegraph): %v", err)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "codegraph"); ok {
		t.Fatalf("codegraph still merged after remove: %+v", cfg.Plugins)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "keep"); !ok {
		t.Fatalf("unrelated .mcp.json server should be preserved: %+v", cfg.Plugins)
	}
}

func TestUpdateMCPServerEditsProjectMCPJSONEntry(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{
  "mcpServers": {
    "codegraph": { "command": "codegraph", "args": ["serve", "--mcp"] }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	entry, ok, err := desktopEffectiveMCPServer(dir, "codegraph")
	if err != nil || !ok {
		t.Fatalf("load codegraph entry: found=%v err=%v", ok, err)
	}
	if err := config.DefaultMCPActivationStore().SetServerEnabled(entry, dir, false); err != nil {
		t.Fatal(err)
	}
	app.activeTab().disabledMCP["codegraph"] = ServerView{}

	if err := app.UpdateMCPServer("codegraph", MCPServerInput{
		Name:      "codegraph",
		Transport: "stdio",
		Command:   "deepzora-missing-mcp-binary",
		Args:      []string{"serve", "--mcp"},
		Env:       map[string]string{"CODEGRAPH_LOG": "debug"},
	}); err != nil {
		t.Fatalf("UpdateMCPServer(.mcp.json codegraph): %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.MCPServers["codegraph"]
	if got.Command != "deepzora-missing-mcp-binary" || !reflect.DeepEqual(got.Args, []string{"serve", "--mcp"}) || got.Env["CODEGRAPH_LOG"] != "debug" {
		t.Fatalf(".mcp.json codegraph = %+v, want updated command/args/env", got)
	}
	if _, ok := findPluginEntry(config.LoadForEdit(config.UserConfigPath()).Plugins, "codegraph"); ok {
		t.Fatalf(".mcp.json update should not create a user config shadow entry")
	}
}

func TestAddMCPServerPersistsRemoteHeaders(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	t.Setenv("STRIPE_TOKEN", "stripe-test-token")
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	tools, err := app.AddMCPServer(MCPServerInput{
		Name:      "stripe",
		Transport: "http",
		URL:       srv.URL,
		Headers: map[string]string{
			"Authorization": "Bearer ${STRIPE_TOKEN}",
			"X-Org":         "team",
		},
	})
	if err != nil {
		t.Fatalf("AddMCPServer(stripe): %v", err)
	}
	if tools != 1 {
		t.Fatalf("tools = %d, want 1", tools)
	}

	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPluginEntry(cfg.Plugins, "stripe")
	if !ok {
		t.Fatalf("stripe plugin missing from config: %+v", cfg.Plugins)
	}
	if p.Type != "http" || p.URL != srv.URL {
		t.Fatalf("stripe plugin transport = %q url = %q", p.Type, p.URL)
	}
	if p.Headers["Authorization"] != "Bearer ${STRIPE_TOKEN}" || p.Headers["X-Org"] != "team" {
		t.Fatalf("stripe headers = %+v", p.Headers)
	}

	view := app.MCPServers()
	for _, s := range view {
		if s.Name == "stripe" {
			if !reflect.DeepEqual(s.HeaderKeys, []string{"Authorization", "X-Org"}) {
				t.Fatalf("stripe header keys = %+v", s.HeaderKeys)
			}
			return
		}
	}
	t.Fatalf("stripe MCP missing from view: %+v", view)
}

func TestInstallMCPServerHandshakeFailureDoesNotPersist(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	result, err := app.InstallMCPServer(MCPServerInput{
		Name: "broken", Transport: "stdio", Command: "deepzora-missing-mcp-binary",
	})
	if err != nil {
		t.Fatalf("InstallMCPServer returned transport error instead of structured issue: %v", err)
	}
	if result.State != "issue" || result.Action != "retry" {
		t.Fatalf("install result = %+v, want retryable issue", result)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "broken"); ok {
		t.Fatalf("failed candidate was persisted: %+v", cfg.Plugins)
	}
	for _, server := range app.MCPServers() {
		if server.Name == "broken" {
			t.Fatalf("failed candidate leaked into the installed server list: %+v", server)
		}
	}
}

func TestInstallMCPServerAuthenticationRequiredPersistsForResume(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	result, err := app.InstallMCPServer(MCPServerInput{Name: "oauth", Transport: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("InstallMCPServer auth result: %v", err)
	}
	if result.State != "action_required" || result.Action != "authenticate" {
		t.Fatalf("install result = %+v, want authentication action", result)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findPluginEntry(cfg.Plugins, "oauth"); !ok {
		t.Fatalf("auth-pending candidate must persist for resume: %+v", cfg.Plugins)
	}
}

func TestAddMCPServerPersistsConnectionConfiguration(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	autoStart := false
	callTimeout := 45
	_, err := app.AddMCPServer(MCPServerInput{
		Name:               "admin",
		Transport:          "streamable-http",
		URL:                srv.URL,
		AutoStart:          &autoStart,
		CallTimeoutSeconds: &callTimeout,
		ToolTimeoutSeconds: map[string]int{
			"wipe": 120,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "admin")
	if !ok || entry.Type != "http" || entry.AutoStart == nil || *entry.AutoStart ||
		entry.CallTimeoutSeconds != 45 || entry.ToolTimeoutSeconds["wipe"] != 120 {
		t.Fatalf("persisted advanced MCP entry = %+v, found=%v", entry, ok)
	}

	views := app.MCPServers()
	if len(views) != 1 || views[0].Transport != "http" || views[0].AutoStart ||
		views[0].CallTimeoutSeconds != 45 || views[0].ToolTimeoutSeconds["wipe"] != 120 {
		t.Fatalf("advanced MCP ServerView = %+v", views)
	}
}
