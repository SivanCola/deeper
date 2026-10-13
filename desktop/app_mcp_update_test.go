package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/plugin"
	"deepzora/internal/pluginpkg"
	"deepzora/internal/tool"
)

func TestUpdateMCPServerRefreshesEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	oldSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found {
		t.Fatal("sibling registry missing initial h tool")
	}
	root := fixture.app.tabs["active"].WorkspaceRoot
	cfg, err := config.LoadForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	entry, found := findPluginEntry(cfg.Plugins, "h")
	if !found {
		t.Fatal("fixture config missing h")
	}
	if err := fixture.app.UpdateMCPServer("h", MCPServerInput{
		Name: "h", Transport: entry.Type, Command: entry.Command, Args: entry.Args,
	}); err != nil {
		t.Fatalf("UpdateMCPServer(h): %v", err)
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host did not reconnect h")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active registry was not refreshed")
	}
	newSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found || newSiblingTool == oldSiblingTool {
		t.Fatal("sibling registry retained the tool backed by the disconnected client")
	}
	if _, found := fixture.disabledRegistry.Get("mcp__h__greet"); found {
		t.Fatal("update re-enabled a tab where the server was disabled")
	}
}

func TestRemoveMCPServerClearsEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	if err := fixture.app.RemoveMCPServer("h"); err != nil {
		t.Fatalf("RemoveMCPServer(h): %v", err)
	}
	if fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host retained the removed server")
	}
	for label, registry := range map[string]*tool.Registry{
		"active": fixture.activeRegistry, "sibling": fixture.siblingRegistry, "disabled": fixture.disabledRegistry,
	} {
		if _, found := registry.Get("mcp__h__greet"); found {
			t.Fatalf("%s registry retained the removed server tool", label)
		}
	}
	for id, tab := range fixture.app.tabs {
		if _, disabled := tab.disabledMCP["h"]; disabled {
			t.Fatalf("tab %s retained removed-server disabled state", id)
		}
	}
}

func TestRemoveMCPServerClearsRecordedStartupFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "broken"
command = "deepzora-missing-mcp-binary"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)
	enableProjectMCPForWorkspace(t, dir, "")

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	recordMCPFailure(app.activeCtrl(), config.PluginEntry{
		Name:    "broken",
		Command: "deepzora-missing-mcp-binary",
	}, errors.New("connect: missing binary"))

	view := app.Capabilities()
	if len(view.Servers) != 1 || view.Servers[0].Name != "broken" || view.Servers[0].Status != "failed" {
		t.Fatalf("Capabilities before remove = %+v, want broken failed", view.Servers)
	}

	if err := app.RemoveMCPServer("broken"); err != nil {
		t.Fatalf("RemoveMCPServer(broken): %v", err)
	}
	if mcpFailed(app.activeCtrl(), "broken") {
		t.Fatalf("Host.Failures() still contains broken after remove: %+v", app.activeCtrl().Host().Failures())
	}
	view = app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "broken" {
			t.Fatalf("Capabilities after remove still contains broken: %+v", view.Servers)
		}
	}
}

func TestRemoveMCPServerRejectsPluginManagedServerWithoutDisconnecting(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	deepzoraHome := config.DeepzoraHomeDir()
	root := filepath.Join(deepzoraHome, "plugins", "superpowers")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginpkg.NativeManifest), fmt.Appendf(nil, `{"apiVersion": "deepzora.com/plugin/v2",
  "name": "superpowers",
  "version": "1.0.0",
  "contributes": {"mcpServers": {
    "helper": { "type": "http", "url": %q }
  }}
}`, srv.URL), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(deepzoraHome, pluginpkg.InstalledPlugin{
		Name:         "superpowers",
		Root:         "plugins/superpowers",
		Version:      "1.0.0",
		ManifestKind: "deepzora",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "helper")
	if !ok {
		t.Fatalf("plugin-managed MCP missing from config: %+v", cfg.Plugins)
	}
	ctrl := control.New(control.Options{Host: plugin.NewHost()})
	defer ctrl.Close()
	if _, err := ctrl.ConnectMCPServer(entry); err != nil {
		t.Fatalf("connect plugin-managed MCP: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.activeTab().WorkspaceRoot = dir
	err = app.RemoveMCPServer("helper")
	if err == nil || !strings.Contains(err.Error(), "managed by plugin") || !strings.Contains(err.Error(), "superpowers") {
		t.Fatalf("RemoveMCPServer(plugin-managed) error = %v", err)
	}
	if !mcpConnected(ctrl, "helper") {
		t.Fatal("plugin-managed MCP was disconnected despite rejected removal")
	}
	for action, actionErr := range map[string]error{
		"clear auth": app.ClearMCPServerAuthentication("helper"),
		"update":     app.UpdateMCPServer("helper", MCPServerInput{Name: "helper", Transport: "http", URL: srv.URL}),
	} {
		if actionErr == nil || !strings.Contains(actionErr.Error(), "managed by plugin") {
			t.Fatalf("%s plugin-managed MCP error = %v", action, actionErr)
		}
	}
	if _, found := findPluginEntry(config.LoadForEdit(config.UserConfigPath()).Plugins, "helper"); found {
		t.Fatal("plugin-managed MCP mutation created a user-config shadow")
	}
	servers := app.MCPServers()
	if len(servers) != 1 || servers[0].Name != "helper" || servers[0].ManagedByPlugin != "superpowers" {
		t.Fatalf("MCPServers() = %+v, want helper managed by superpowers", servers)
	}
}

func TestRemoveMCPServerRejectsRuntimeOnlyServerWithoutDisconnecting(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	srv := desktopMCPHTTPServer(t)
	defer srv.Close()
	ctrl := control.New(control.Options{Host: plugin.NewHost()})
	defer ctrl.Close()
	if _, err := ctrl.ConnectMCPServer(config.PluginEntry{Name: "runtime-only", Type: "http", URL: srv.URL}); err != nil {
		t.Fatalf("connect runtime-only MCP: %v", err)
	}

	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.activeTab().WorkspaceRoot = dir
	err := app.RemoveMCPServer("runtime-only")
	if err == nil || !strings.Contains(err.Error(), "no removable MCP server") {
		t.Fatalf("RemoveMCPServer(runtime-only) error = %v", err)
	}
	if !mcpConnected(ctrl, "runtime-only") {
		t.Fatal("runtime-only MCP was disconnected despite failed persistence removal")
	}
}

func TestUpdateMCPServerPreservesProjectTOMLSourceAndGlobalShadow(t *testing.T) {
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

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost(), WorkspaceRoot: dir}), "")
	defer app.activeCtrl().Close()
	app.activeTab().WorkspaceRoot = dir
	entry, ok, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !ok || entry.Source != config.MCPSourceProjectConfig {
		t.Fatalf("load project docs entry: entry=%+v found=%v err=%v", entry, ok, err)
	}
	if err := config.DefaultMCPActivationStore().SetServerEnabled(entry, dir, false); err != nil {
		t.Fatal(err)
	}
	app.activeTab().disabledMCP["docs"] = ServerView{}

	if err := app.UpdateMCPServer("docs", MCPServerInput{
		Name: "docs", Transport: "stdio", Command: "project-docs-updated",
	}); err != nil {
		t.Fatalf("UpdateMCPServer(project deepzora.toml docs): %v", err)
	}

	projectCfg := config.LoadForEdit(projectPath)
	projectEntry, found := findPluginEntry(projectCfg.Plugins, "docs")
	if !found || projectEntry.Command != "project-docs-updated" {
		t.Fatalf("project docs entry = %+v, found=%v", projectEntry, found)
	}
	globalCfg := config.LoadForEdit(config.UserConfigPath())
	globalEntry, found := findPluginEntry(globalCfg.Plugins, "docs")
	if !found || globalEntry.Command != "global-docs" {
		t.Fatalf("global shadow changed while editing project entry: %+v, found=%v", globalEntry, found)
	}
	effective, found, err := desktopEffectiveMCPServer(dir, "docs")
	if err != nil || !found || effective.Source != config.MCPSourceProjectConfig || effective.Command != "project-docs-updated" {
		t.Fatalf("effective docs after edit = %+v, found=%v err=%v", effective, found, err)
	}
}

func TestUpdateMCPServerPreservesAbsentFieldsAndClearsExplicitOnes(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	callTimeout := 45
	if _, err := app.AddMCPServer(MCPServerInput{
		Name:               "admin",
		Transport:          "http",
		URL:                srv.URL,
		CallTimeoutSeconds: &callTimeout,
		ToolTimeoutSeconds: map[string]int{"wipe": 120},
	}); err != nil {
		t.Fatal(err)
	}

	// An old frontend (or a partial payload) omits optional timeout fields.
	if err := app.UpdateMCPServer("admin", MCPServerInput{Name: "admin", Transport: "http", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "admin")
	if !ok || entry.CallTimeoutSeconds != 45 || entry.ToolTimeoutSeconds["wipe"] != 120 {
		t.Fatalf("absent input fields must preserve persisted values, entry = %+v, found=%v", entry, ok)
	}

	// Explicit zero values are the editor's clear semantics.
	cleared := 0
	if err := app.UpdateMCPServer("admin", MCPServerInput{
		Name:               "admin",
		Transport:          "http",
		URL:                srv.URL,
		CallTimeoutSeconds: &cleared,
		ToolTimeoutSeconds: map[string]int{},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok = findPluginEntry(cfg.Plugins, "admin")
	if !ok || entry.CallTimeoutSeconds != 0 || len(entry.ToolTimeoutSeconds) != 0 {
		t.Fatalf("explicit empty fields must clear persisted values, entry = %+v, found=%v", entry, ok)
	}
}

func TestUpdateMCPServerFailedCandidateRollsBackConfigAndConnection(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	srv := desktopMCPHTTPServer(t)
	defer srv.Close()

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()
	if _, err := app.AddMCPServer(MCPServerInput{Name: "stable", Transport: "http", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	err := app.UpdateMCPServer("stable", MCPServerInput{
		Name: "stable", Transport: "stdio", Command: "deepzora-missing-mcp-binary",
	})
	if err == nil {
		t.Fatal("broken update candidate should fail")
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := findPluginEntry(cfg.Plugins, "stable")
	if !ok || entry.Type != "http" || entry.URL != srv.URL {
		t.Fatalf("failed update changed durable config: %+v, found=%v", entry, ok)
	}
	if !app.activeCtrl().Host().HasClient("stable") {
		t.Fatal("previous MCP connection was not restored after failed update")
	}
}

func TestUpdateMCPServerSplitsPastedCommandLine(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := t.TempDir()
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
	app.activeTab().disabledMCP["playwright"] = ServerView{}

	if err := app.UpdateMCPServer("playwright", MCPServerInput{
		Name:      "playwright",
		Transport: "stdio",
		Command:   "npx -y @modelcontextprotocol/server-filesystem .",
	}); err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Plugins[0]
	if p.Command != "npx" {
		t.Fatalf("command = %q, want npx", p.Command)
	}
	if got := strings.Join(p.Args, "\x00"); got != strings.Join([]string{"-y", "@modelcontextprotocol/server-filesystem", "."}, "\x00") {
		t.Fatalf("args = %v", p.Args)
	}
}

func TestUpdateMCPServerRejectsReconnectFailureWithoutPersisting(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "broken"
command = "deepzora-old-missing-mcp-binary"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)
	enableProjectMCPForWorkspace(t, dir, "")

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	if err := app.UpdateMCPServer("broken", MCPServerInput{
		Name:      "broken",
		Transport: "stdio",
		Command:   "deepzora-missing-mcp-binary",
	}); err == nil {
		t.Fatal("UpdateMCPServer should reject an unusable candidate")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Plugins[0].Command; got != "deepzora-old-missing-mcp-binary" {
		t.Fatalf("failed update command = %q, want original command", got)
	}

	if !mcpFailed(app.activeCtrl(), "broken") {
		t.Fatalf("Host.Failures() = %+v, want broken failure recorded", app.activeCtrl().Host().Failures())
	}
	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "broken" {
			if s.Status != "failed" {
				t.Fatalf("server status = %q, want failed; server = %+v", s.Status, s)
			}
			if s.Command != "deepzora-old-missing-mcp-binary" {
				t.Fatalf("failed candidate leaked into server config: %+v", s)
			}
			return
		}
	}
	t.Fatalf("broken MCP missing from Capabilities: %+v", view.Servers)
}
