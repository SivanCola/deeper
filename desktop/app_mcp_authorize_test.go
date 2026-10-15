package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

func TestSetMCPServerEnabledRestoresOnDemandWithoutConnecting(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("DEEPZORA_CACHE_HOME", t.TempDir())
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "offline"
type = "http"
url = "http://127.0.0.1:1/mcp"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	host := plugin.NewHost()
	defer host.Close()
	reg := tool.NewRegistry()
	ctrl := control.New(control.Options{Host: host, Registry: reg, PluginCtx: context.Background(), WorkspaceRoot: dir})
	app := NewApp()
	app.setTestCtrl(ctrl, "")
	app.tabs["test"].WorkspaceRoot = dir

	if err := app.SetMCPServerEnabled("offline", false); err != nil {
		t.Fatalf("SetMCPServerEnabled(false): %v", err)
	}
	if err := app.SetMCPServerEnabled("offline", true); err != nil {
		t.Fatalf("SetMCPServerEnabled(true) forced an unavailable connection: %v", err)
	}
	if host.HasClient("offline") {
		t.Fatal("durable enable started the disconnected MCP server")
	}
	if _, ok := reg.Get("mcp__offline__connect"); !ok {
		t.Fatalf("on-demand connect stub missing after enable; names=%v", reg.Names())
	}
}

func TestSetMCPServerEnabledSharedHostPreservesSiblingTabs(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sharedHost := plugin.NewHost()
	defer sharedHost.Close()
	tools, err := sharedHost.Add(ctx, plugin.Spec{Name: "h", Type: "http", URL: srv.URL})
	if err != nil {
		t.Fatalf("sharedHost.Add: %v", err)
	}

	activeRegistry := tool.NewRegistry()
	siblingRegistry := tool.NewRegistry()
	for _, mt := range tools {
		activeRegistry.Add(mt)
		siblingRegistry.Add(mt)
	}
	activeCtrl := control.New(control.Options{Host: sharedHost, Registry: activeRegistry, PluginCtx: context.Background()})
	siblingCtrl := control.New(control.Options{Host: sharedHost, Registry: siblingRegistry, PluginCtx: context.Background()})
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"active": {
			ID:            "active",
			Scope:         "global",
			WorkspaceRoot: dir,
			Ready:         true,
			Ctrl:          activeCtrl,
			SharedHostKey: dir,
			disabledMCP:   map[string]ServerView{},
		},
		"sibling": {
			ID:            "sibling",
			Scope:         "global",
			WorkspaceRoot: dir,
			Ready:         true,
			Ctrl:          siblingCtrl,
			SharedHostKey: dir,
			disabledMCP:   map[string]ServerView{},
		},
	}
	app.activeTabID = "active"

	if err := app.SetMCPServerEnabled("h", false); err != nil {
		t.Fatalf("SetMCPServerEnabled(h,false): %v", err)
	}
	if _, found := activeRegistry.Get("mcp__h__greet"); found {
		t.Fatal("active tab still has h tools after disabling the shared server")
	}
	if _, found := siblingRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("sibling tab lost h tools when active tab disabled the shared server")
	}
	if !sharedHost.HasClient("h") {
		t.Fatal("shared host client was removed by a per-tab disable")
	}
	view := app.Capabilities()
	if len(view.Servers) != 1 || view.Servers[0].Name != "h" || view.Servers[0].Status != "disabled" {
		t.Fatalf("Capabilities after disable = %+v, want h disabled for the active tab", view.Servers)
	}

	if err := app.SetMCPServerEnabled("h", true); err != nil {
		t.Fatalf("SetMCPServerEnabled(h,true): %v", err)
	}
	if _, found := activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active tab did not re-register h tools from the existing shared client")
	}
	view = app.Capabilities()
	if len(view.Servers) != 1 || view.Servers[0].Name != "h" || view.Servers[0].Status != "connected" {
		t.Fatalf("Capabilities after re-enable = %+v, want h connected for the active tab", view.Servers)
	}
}

func TestReconnectMCPServerStartsProjectOnlyOnce(t *testing.T) {
	gateAddr, attempts := newDesktopMCPStartGate(t, func(_ int, conn net.Conn) {
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1)
	oldSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found {
		t.Fatal("sibling registry missing initial h tool")
	}

	if err := fixture.app.ReconnectMCPServer("h"); err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	select {
	case attempt := <-attempts:
		t.Fatalf("project reconnect started a temporary connection process (unexpected attempt %d)", attempt)
	case <-time.After(250 * time.Millisecond):
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("project reconnect did not leave h connected")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active registry was not refreshed after project reconnect")
	}
	newSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found || newSiblingTool == oldSiblingTool {
		t.Fatal("sibling registry did not receive the single new project connection")
	}
	if _, found := fixture.disabledRegistry.Get("mcp__h__greet"); found {
		t.Fatal("project reconnect re-enabled h in a disabled sibling tab")
	}
}

func TestReconnectMCPServerRefreshesEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	oldSiblingTool, found := fixture.siblingRegistry.Get("mcp__h__greet")
	if !found {
		t.Fatal("sibling registry missing initial h tool")
	}
	if err := fixture.app.ReconnectMCPServer("h"); err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
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
		t.Fatal("reconnect re-enabled a tab where the server was disabled")
	}
}

func TestReconnectMCPServerUsesEffectiveProjectConfigWhenUserNameIsShadowed(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	userServer := desktopMCPHTTPServerWithTool(t, "user-shadow", "user_tool")
	defer userServer.Close()
	projectServer := desktopMCPHTTPServerWithTool(t, "project-effective", "project_tool")
	defer projectServer.Close()

	userCfg := config.LoadForEdit(config.UserConfigPath())
	userCfg.Plugins = []config.PluginEntry{{Name: "h", Type: "http", URL: userServer.URL}}
	if err := userCfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
type = "http"
url = %q
`, projectServer.URL), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	host := plugin.NewHost()
	t.Cleanup(host.Close)
	registry := tool.NewRegistry()
	ctrl := control.New(control.Options{
		Host: host, Registry: registry, PluginCtx: context.Background(), WorkspaceRoot: dir,
		MCPConfigureSpec: func(spec *plugin.Spec) {
			// This test isolates effective-source selection from the project launch
			// approval flow, which has its own end-to-end coverage.
			spec.RequireLaunchApproval = false
			spec.Authorized = true
		},
	})
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"active": {
			ID: "active", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: ctrl, disabledMCP: map[string]ServerView{},
		},
	}
	app.activeTabID = "active"

	if err := app.ReconnectMCPServer("h"); err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	if _, found := registry.Get("mcp__h__project_tool"); !found {
		t.Fatal("reconnect did not use the effective project MCP configuration")
	}
	if _, found := registry.Get("mcp__h__user_tool"); found {
		t.Fatal("reconnect used the shadowed user MCP configuration")
	}
}

func TestClearMCPServerAuthenticationClearsEverySharedHostRegistry(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	if err := fixture.app.ClearMCPServerAuthentication("h"); err != nil {
		t.Fatalf("ClearMCPServerAuthentication(h): %v", err)
	}
	if fixture.sharedHost.HasClient("h") {
		t.Fatal("shared host retained h after clearing authentication")
	}
	for label, registry := range map[string]*tool.Registry{
		"active": fixture.activeRegistry, "sibling": fixture.siblingRegistry, "disabled": fixture.disabledRegistry,
	} {
		if _, found := registry.Get("mcp__h__greet"); found {
			t.Fatalf("%s registry retained h after clearing authentication", label)
		}
	}
}

func TestReconnectMCPServerSerializesConcurrentDisable(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)

	disableEntered := make(chan struct{})
	var disableOnce sync.Once
	fixture.app.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "set-enabled" {
			disableOnce.Do(func() { close(disableEntered) })
		}
	}
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.ReconnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	disableDone := make(chan error, 1)
	go func() { disableDone <- fixture.app.SetMCPServerEnabled("h", false) }()
	select {
	case <-disableEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent disable did not reach the MCP lifecycle lock")
	}
	select {
	case err := <-disableDone:
		t.Fatalf("concurrent disable bypassed reconnect serialization: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	if err := <-disableDone; err != nil {
		t.Fatalf("SetMCPServerEnabled(h,false): %v", err)
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); found {
		t.Fatal("reconnect overrode the later per-tab disable")
	}
	if _, disabled := fixture.app.tabs["active"].disabledMCP["h"]; !disabled {
		t.Fatal("active tab did not retain the later disable decision")
	}
	if _, found := fixture.siblingRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("active-tab disable removed the shared MCP from its enabled sibling")
	}
}

// A turn start holds its tab's turn gate before the controller reports active
// work, so an idle check done without the gate can go stale immediately. The
// authorization must wait on the sibling's gate — never disconnect first — and must
// fail once the gated re-check sees the started work.
func TestReconnectMCPServerWaitsForSiblingTurnGate(t *testing.T) {
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1) // drain the fixture's initial connect
	sibling := fixture.app.tabs["sibling"]

	// Simulate the racing turn: it takes the gate first, and only transitions
	// its controller to busy while holding it.
	sibling.turnStartMu.Lock()
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.ReconnectMCPServer("h") }()
	select {
	case got := <-attempts:
		t.Fatalf("trust connection launched (attempt %d) while a sibling turn gate was held", got)
	case err := <-authorizeDone:
		t.Fatalf("ReconnectMCPServer returned %v without waiting for the sibling turn gate", err)
	case <-time.After(700 * time.Millisecond):
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("authorization disconnected the shared client while a sibling turn gate was held")
	}
	busy := newBackgroundJobController(t, "sibling-turn")
	fixture.app.mu.Lock()
	sibling.Ctrl = busy
	fixture.app.mu.Unlock()
	sibling.turnStartMu.Unlock()

	err := <-authorizeDone
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("ReconnectMCPServer after sibling turn start error = %v, want active-work guard", err)
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("failed authorization left the shared client disconnected")
	}
	if _, found := fixture.siblingRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("authorization stripped the busy sibling of its MCP tools")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("authorization stripped the active tab of its MCP tools")
	}
}

func TestReconnectMCPServerSerializesCloseOfCapturedRuntime(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1)

	dir := fixture.app.tabs["active"].WorkspaceRoot
	otherRoot := robustTempDir(t)
	otherHost := plugin.NewHost()
	t.Cleanup(otherHost.Close)
	otherCtrl := control.New(control.Options{Host: otherHost, WorkspaceRoot: otherRoot})
	t.Cleanup(otherCtrl.Close)
	fixture.app.tabs = map[string]*WorkspaceTab{
		"active": fixture.app.tabs["active"],
		"other": {
			ID: "other", Scope: "project", WorkspaceRoot: otherRoot, Ready: true,
			Ctrl: otherCtrl, disabledMCP: map[string]ServerView{},
		},
	}
	fixture.app.tabOrder = []string{"active", "other"}
	fixture.app.activeTabID = "active"
	fixture.app.sharedHosts = map[string]*sharedPluginHost{
		dir: {host: fixture.sharedHost, refs: 1},
	}

	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.ReconnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.app.CloseTab("active") }()
	select {
	case err := <-closeDone:
		t.Fatalf("CloseTab bypassed the MCP lifecycle barrier: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("CloseTab(active) after trust: %v", err)
	}
}

func TestReconnectMCPServerKeepsInvokingWorkspaceWhenActiveTabChanges(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	otherRoot := robustTempDir(t)
	otherCtrl := control.New(control.Options{Host: plugin.NewHost(), WorkspaceRoot: otherRoot})
	t.Cleanup(otherCtrl.Close)
	fixture.app.tabs["other"] = &WorkspaceTab{
		ID: "other", Scope: "project", WorkspaceRoot: otherRoot, Ready: true,
		Ctrl: otherCtrl, disabledMCP: map[string]ServerView{},
	}
	fixture.app.tabOrder = []string{"active", "sibling", "disabled", "other"}

	sibling := fixture.app.tabs["sibling"]
	sibling.turnStartMu.Lock()
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.ReconnectMCPServer("h") }()
	waitForRuntimeAdmissionBarrier(t, fixture.app)
	if err := fixture.app.SetActiveTab("other"); err != nil {
		sibling.turnStartMu.Unlock()
		t.Fatalf("SetActiveTab(other): %v", err)
	}
	sibling.turnStartMu.Unlock()

	if err := <-authorizeDone; err != nil {
		t.Fatalf("authorization operation drifted from its invoking workspace: %v", err)
	}
}

// A tab created during the 30s trust connection must not complete its async
// controller build — attaching to the shared Host mid-connection can relaunch a
// single-instance server or leave a registry the authorization never saw. The build
// goes through the real startTabControllerBuild path and must only run after
// the authorization releases the barrier.
func TestReconnectMCPServerBlocksLateControllerBuild(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	waitForDesktopMCPStartAttempt(t, attempts, 1) // drain the fixture's initial connect
	dir := fixture.app.tabs["active"].WorkspaceRoot

	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.ReconnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2) // connection launched: gates held

	late := &WorkspaceTab{
		ID: "late", Scope: "global", WorkspaceRoot: dir,
		disabledMCP: map[string]ServerView{},
	}
	late.sink = &tabEventSink{tabID: "late", app: fixture.app}
	fixture.app.mu.Lock()
	fixture.app.tabs["late"] = late
	fixture.app.mu.Unlock()
	buildDone := make(chan struct{})
	go func() {
		// a.ctx is nil in this fixture, so the build runs synchronously on
		// this goroutine — through the real buildTabControllerWithContext.
		fixture.app.startTabControllerBuild(late)
		close(buildDone)
	}()
	select {
	case <-buildDone:
		t.Fatal("late controller build completed while the trust connection held the admission barrier")
	case <-time.After(400 * time.Millisecond):
	}

	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("ReconnectMCPServer(h): %v", err)
	}
	select {
	case <-buildDone:
	case <-time.After(10 * time.Second):
		t.Fatal("late controller build never ran after the authorization released the barrier")
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("authorization did not leave the shared client reconnected")
	}
}

func TestSetMCPServerEnabledRejectsBackgroundJobs(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	app.setTestCtrl(newBackgroundJobController(t, "mcp-enabled-job"), "")

	err := app.SetMCPServerEnabled("time", false)
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("SetMCPServerEnabled with background job error = %v, want active-work guard", err)
	}
	if tab := app.activeTab(); tab == nil || len(tab.disabledMCP) != 0 {
		t.Fatalf("disabled MCP state changed after rejected toggle: %+v", tab)
	}
}

func TestClearMCPServerAuthenticationClearsConfigAndFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "figma"
type = "http"
url = "https://mcp.figma.com/mcp?access_token=abc&workspace=main"
headers = { Authorization = "Bearer ${FIGMA_TOKEN}", "X-Org" = "team" }
env = { FIGMA_TOKEN = "${FIGMA_TOKEN}", DEBUG = "1" }
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

	if err := app.ClearMCPServerAuthentication("figma"); err != nil {
		t.Fatalf("ClearMCPServerAuthentication: %v", err)
	}
	if failures := host.Failures(); len(failures) != 0 {
		t.Fatalf("failure should be cleared: %+v", failures)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Plugins[0]
	if p.URL != "https://mcp.figma.com/mcp?workspace=main" {
		t.Fatalf("url = %q", p.URL)
	}
	if _, ok := p.Headers["Authorization"]; ok {
		t.Fatalf("auth header should be removed: %v", p.Headers)
	}
	if p.Headers["X-Org"] != "team" {
		t.Fatalf("ordinary header should be preserved: %v", p.Headers)
	}
	if _, ok := p.Env["FIGMA_TOKEN"]; ok {
		t.Fatalf("auth env should be removed: %v", p.Env)
	}
	if p.Env["DEBUG"] != "1" {
		t.Fatalf("ordinary env should be preserved: %v", p.Env)
	}
	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "figma" {
			if s.Status != "deferred" || !s.Enabled || s.RuntimeState != "idle" || s.AuthStatus != "possible" {
				t.Fatalf("figma should return to background possible auth: %+v", s)
			}
			return
		}
	}
	t.Fatalf("figma MCP missing from Capabilities: %+v", view.Servers)
}

func TestReconnectMCPServerClearsInitializingPlaceholderAndRecordsFailure(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "codegraph"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)
	enableProjectMCPForWorkspace(t, dir, "")

	reg := tool.NewRegistry()
	reg.Add(desktopFakeTool{name: "mcp__codegraph__connect"})
	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost(), Registry: reg}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	foundIdle := false
	for _, s := range view.Servers {
		if s.Name == "codegraph" {
			foundIdle = true
			if s.Status != "deferred" || !s.Enabled || s.RuntimeState != "idle" {
				t.Fatalf("initial codegraph server = %+v, want automatic idle background state", s)
			}
		}
	}
	if !foundIdle {
		t.Fatalf("codegraph missing before reconnect: %+v", view.Servers)
	}
	if _, ok := reg.Get("mcp__codegraph__connect"); !ok {
		t.Fatal("test setup expected stale codegraph connect placeholder")
	}

	if err := app.ReconnectMCPServer("codegraph"); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("ReconnectMCPServer error = %v, want missing command", err)
	}
	if _, ok := reg.Get("mcp__codegraph__connect"); ok {
		t.Fatalf("stale codegraph placeholder still registered after reconnect failure; names=%v", reg.Names())
	}
	if !mcpFailed(app.activeCtrl(), "codegraph") {
		t.Fatalf("Host.Failures() = %+v, want codegraph failure recorded", app.activeCtrl().Host().Failures())
	}

	view = app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "codegraph" {
			if s.Status != "failed" || s.Error == "" {
				t.Fatalf("codegraph after failed reconnect = %+v, want failed with error", s)
			}
			return
		}
	}
	t.Fatalf("codegraph missing after reconnect: %+v", view.Servers)
}
