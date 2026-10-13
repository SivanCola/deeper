package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deepzora/internal/command"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/plugin"
	"deepzora/internal/pluginpkg"
	"deepzora/internal/skill"
	"deepzora/internal/tool"
)

func TestPluginToolsToViewPreservesSchemaError(t *testing.T) {
	got := pluginToolsToView([]plugin.ToolInfo{{
		Name: "generate_yso_bytes", Description: "Generate payload", ReadOnlyHint: true,
		SchemaError: "invalid input schema: bad nested type",
	}})
	if len(got) != 1 || got[0].SchemaError != "invalid input schema: bad nested type" {
		t.Fatalf("tool views = %+v", got)
	}
}

func TestCommandsDocsKeepsPluginNamesQualified(t *testing.T) {
	tests := []struct {
		name          string
		commands      []command.Command
		skills        []skill.Skill
		wantCanonical string
	}{
		{
			name: "qualified plugin command",
			commands: []command.Command{
				{Name: "manuals:docs", Plugin: "manuals"},
			},
			wantCanonical: "manuals:docs",
		},
		{
			name:          "compatible plugin skill alias",
			skills:        []skill.Skill{{Name: "docs", Plugin: "manuals"}},
			wantCanonical: "manuals:docs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := control.New(control.Options{Commands: tt.commands, Skills: tt.skills})
			defer ctrl.Close()
			app := NewApp()
			app.setTestCtrl(ctrl, "")
			commands := app.Commands()
			if docs, ok := commandInfoByName(commands, "docs"); !ok || docs.Kind != "builtin" {
				t.Fatalf("builtin docs was displaced by qualified plugin: %+v", commands)
			}
			for _, want := range []string{"docs", tt.wantCanonical} {
				if _, ok := commandInfoByName(commands, want); !ok {
					t.Fatalf("commands missing %q: %+v", want, commands)
				}
			}
		})
	}
}

func TestCapabilitiesIncludesInstalledPlugins(t *testing.T) {
	isolateDesktopUserDirs(t)
	deepzoraHome := config.DeepzoraHomeDir()
	root := filepath.Join(deepzoraHome, "plugins", "superpowers")
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "plan", "SKILL.md"), []byte("---\ndescription: Plan work\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(`{
  "name": "superpowers",
  "version": "6.1.0",
  "description": "Planning workflows",
  "skills": "./skills/"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(deepzoraHome, pluginpkg.InstalledPlugin{
		Name:         "superpowers",
		Root:         "plugins/superpowers",
		Version:      "6.1.0",
		Description:  "Planning workflows",
		ManifestKind: "codex",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	plugins := app.Capabilities().Plugins
	if len(plugins) != 1 || plugins[0].Name != "superpowers" || plugins[0].Skills != 1 {
		t.Fatalf("Capabilities().Plugins = %+v", plugins)
	}
	if len(plugins[0].SkillDetails) != 1 || plugins[0].SkillDetails[0].Invocation != "/superpowers:plan" {
		t.Fatalf("Capabilities().Plugins skill details = %+v", plugins[0].SkillDetails)
	}
}

// installGatedTestPluginPackage registers an installed plugin package whose
// manifest declares the gated fixture's MCP server, so RemovePlugin exercises
// the real uninstall and MCP disconnect flow. Returns the plugin root.
func installGatedTestPluginPackage(t *testing.T, mcpServerName string) string {
	t.Helper()
	deepzoraHome := config.DeepzoraHomeDir()
	root := filepath.Join(deepzoraHome, "plugins", "review-helper")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pluginpkg.NativeManifest), fmt.Appendf(nil, `{"apiVersion": "deepzora.com/plugin/v2",
  "name": "review-helper",
  "version": "1.0.0",
  "contributes": {"mcpServers": {
    %q: { "type": "stdio", "command": "helper" }
  }}
}`, mcpServerName), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pluginpkg.Upsert(deepzoraHome, pluginpkg.InstalledPlugin{
		Name:         "review-helper",
		Root:         "plugins/review-helper",
		Version:      "1.0.0",
		ManifestKind: "deepzora",
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
	return root
}

func installedPluginNamed(t *testing.T, name string) bool {
	t.Helper()
	st, err := pluginpkg.LoadState(config.DeepzoraHomeDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range st.Plugins {
		if p.Name == name {
			return true
		}
	}
	return false
}

// A global plugin uninstall must clean every runtime, not only the active tab:
// sibling registries on the shared Host would otherwise keep provider-visible
// tools backed by the closed client, and other workspaces would keep running
// the uninstalled server.
func TestRemovePluginDisconnectsEveryRuntime(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	pluginRoot := installGatedTestPluginPackage(t, "h")

	if err := fixture.app.RemovePlugin("review-helper"); err != nil {
		t.Fatalf("RemovePlugin(review-helper): %v", err)
	}
	if fixture.sharedHost.HasClient("h") {
		t.Fatal("uninstall left the shared MCP client connected")
	}
	for name, reg := range map[string]*tool.Registry{
		"active":   fixture.activeRegistry,
		"sibling":  fixture.siblingRegistry,
		"disabled": fixture.disabledRegistry,
	} {
		if _, found := reg.Get("mcp__h__greet"); found {
			t.Fatalf("%s registry still exposes the uninstalled MCP tool", name)
		}
	}
	if _, err := os.Stat(pluginRoot); !os.IsNotExist(err) {
		t.Fatalf("plugin root still present after uninstall (err=%v)", err)
	}
	if installedPluginNamed(t, "review-helper") {
		t.Fatal("plugin state still lists the uninstalled plugin")
	}
}

// The pre-lock active-work check can go stale during the lifecycle-lock wait.
// Work that starts mid-wait must fail the removal before anything is deleted;
// the old order deleted the plugin first and only then reported the failure.
func TestRemovePluginRechecksActiveWorkUnderLock(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)
	installGatedTestPluginPackage(t, "h")

	removeEntered := make(chan struct{})
	var removeOnce sync.Once
	fixture.app.runtimeMutationBeforeLockHook = func(operation string) {
		if operation == "remove-plugin" {
			removeOnce.Do(func() { close(removeEntered) })
		}
	}
	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForDesktopMCPStartAttempt(t, attempts, 2)
	removeDone := make(chan error, 1)
	go func() { removeDone <- fixture.app.RemovePlugin("review-helper") }()
	select {
	case <-removeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("RemovePlugin did not reach the MCP lifecycle lock")
	}
	// While RemovePlugin waits for the lock, background work starts on the
	// active tab — exactly the window the pre-lock check cannot see.
	busy := newBackgroundJobController(t, "remove-plugin-active-work")
	fixture.app.mu.Lock()
	fixture.app.tabs["active"].Ctrl = busy
	fixture.app.mu.Unlock()
	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	err := <-removeDone
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("RemovePlugin during background work error = %v, want active-work guard", err)
	}
	if !installedPluginNamed(t, "review-helper") {
		t.Fatal("active-work guard fired only after the plugin was already uninstalled")
	}
}

// A global uninstall disconnects every runtime, so the busy guard must cover
// every runtime too: a background job on a sibling tab must fail the removal
// before anything is deleted, not silently lose its plugin MCP mid-run.
func TestRemovePluginRejectsBusySiblingRuntime(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	installGatedTestPluginPackage(t, "h")
	busy := newBackgroundJobController(t, "sibling-busy")
	fixture.app.mu.Lock()
	fixture.app.tabs["sibling"].Ctrl = busy
	fixture.app.mu.Unlock()

	err := fixture.app.RemovePlugin("review-helper")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("RemovePlugin with busy sibling error = %v, want active-work guard", err)
	}
	if !installedPluginNamed(t, "review-helper") {
		t.Fatal("plugin was uninstalled despite a busy sibling runtime")
	}
	if !fixture.sharedHost.HasClient("h") {
		t.Fatal("busy-sibling guard still disconnected the shared MCP client")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("busy-sibling guard still removed active registry tools")
	}
}

// Detached runtimes keep running after their tab is closed; the uninstall busy
// guard must see them through the same gate sweep as visible tabs.
func TestRemovePluginRejectsBusyDetachedRuntime(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	installGatedTestPluginPackage(t, "h")
	busy := newBackgroundJobController(t, "detached-busy")
	fixture.app.mu.Lock()
	fixture.app.detachedSessions = map[string]*WorkspaceTab{
		"detached": {
			ID: "detached", Scope: "global", Ready: true,
			Ctrl: busy, disabledMCP: map[string]ServerView{},
		},
	}
	fixture.app.mu.Unlock()

	err := fixture.app.RemovePlugin("review-helper")
	if err == nil || !strings.Contains(err.Error(), "stop background jobs") {
		t.Fatalf("RemovePlugin with busy detached runtime error = %v, want active-work guard", err)
	}
	if !installedPluginNamed(t, "review-helper") {
		t.Fatal("plugin was uninstalled despite a busy detached runtime")
	}
}

// Work admission — not tab-set stability — is the gate invariant: a runtime
// added after the gate snapshot must not be able to start a turn while the
// uninstall holds the barrier. The late turn goes through the real
// beginTabTurn admission path and must only be admitted after the uninstall.
func TestRemovePluginBlocksLateTurnAdmission(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	installGatedTestPluginPackage(t, "h")
	dir := fixture.app.tabs["active"].WorkspaceRoot

	// Hold an existing tab's gate so the uninstall blocks mid-acquisition
	// with the admission barrier already held.
	sibling := fixture.app.tabs["sibling"]
	sibling.turnStartMu.Lock()
	removeDone := make(chan error, 1)
	go func() { removeDone <- fixture.app.RemovePlugin("review-helper") }()
	waitForRuntimeAdmissionBarrier(t, fixture.app)

	lateCtrl := control.New(control.Options{Host: plugin.NewHost(), WorkspaceRoot: dir})
	t.Cleanup(lateCtrl.Close)
	fixture.app.mu.Lock()
	fixture.app.tabs["late"] = &WorkspaceTab{
		ID: "late", Scope: "global", WorkspaceRoot: dir, Ready: true,
		Ctrl: lateCtrl, disabledMCP: map[string]ServerView{},
	}
	fixture.app.mu.Unlock()
	type admission struct {
		turn *tabTurnAdmission
		ctrl control.SessionAPI
		err  error
	}
	admitted := make(chan admission, 1)
	go func() {
		turn, ctrl, err := fixture.app.beginTabTurn("late", false)
		admitted <- admission{turn: turn, ctrl: ctrl, err: err}
	}()
	select {
	case got := <-admitted:
		t.Fatalf("late turn was admitted (err=%v) while the uninstall held the admission barrier", got.err)
	case <-time.After(300 * time.Millisecond):
	}

	sibling.turnStartMu.Unlock()
	if err := <-removeDone; err != nil {
		t.Fatalf("RemovePlugin(review-helper): %v", err)
	}
	got := <-admitted
	if got.err != nil {
		t.Fatalf("late turn admission after uninstall: %v", got.err)
	}
	got.turn.abort()
	if installedPluginNamed(t, "review-helper") {
		t.Fatal("uninstall did not complete before the late turn was admitted")
	}
}
