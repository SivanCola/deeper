package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"deepzora/internal/boot"
	"deepzora/internal/config"
	"deepzora/internal/control"
	"deepzora/internal/mcplaunch"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

func desktopMCPHTTPServer(t *testing.T) *httptest.Server {
	return desktopMCPHTTPServerWithTool(t, "h", "greet")
}

func desktopMCPHTTPServerWithTool(t *testing.T, serverName, toolName string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2026-07-28",
				"serverInfo":      map[string]any{"name": serverName, "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        toolName,
				"description": "Greet someone.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			result = map[string]any{}
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestDesktopMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DESKTOP_MCP_HELPER") != "1" {
		return
	}
	if addr := os.Getenv("DESKTOP_MCP_START_GATE_ADDR"); addr != "" {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "connect MCP start gate %s: %v\n", addr, err)
			os.Exit(24)
		}
		var release [1]byte
		if _, err := io.ReadFull(conn, release[:]); err != nil {
			_ = conn.Close()
			_, _ = fmt.Fprintf(os.Stderr, "wait for MCP start gate %s: %v\n", addr, err)
			os.Exit(25)
		}
		_ = conn.Close()
	}
	var instanceListener net.Listener
	if addr := os.Getenv("DESKTOP_MCP_SINGLE_INSTANCE_ADDR"); addr != "" {
		var err error
		instanceListener, err = net.Listen("tcp", addr)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "another MCP instance is already using %s: %v\n", addr, err)
			os.Exit(23)
		}
		defer instanceListener.Close()
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("decode helper request: %v", err)
		}
		if req.ID == nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2026-07-28",
				"serverInfo":      map[string]any{"name": "desktop-helper", "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "greet", "description": "Greet someone.",
				"inputSchema": map[string]any{"type": "object"},
			}}}
		default:
			result = map[string]any{}
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}); err != nil {
			t.Fatalf("encode helper response: %v", err)
		}
	}
}

func newGatedDesktopMCPLaunchFixture(t *testing.T, startGateAddr string) gatedDesktopMCPLaunchFixture {
	t.Helper()
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	singleInstanceAddr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	gateConfig := ""
	if startGateAddr != "" {
		gateConfig = fmt.Sprintf("DESKTOP_MCP_START_GATE_ADDR = %q\n", startGateAddr)
	}
	helperArgs := []string{"-test.run=TestDesktopMCPHelperProcess", "--"}
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), fmt.Appendf(nil, `
[[plugins]]
name = "h"
command = %q
args = ["-test.run=TestDesktopMCPHelperProcess", "--"]

[plugins.env]
GO_WANT_DESKTOP_MCP_HELPER = "1"
DESKTOP_MCP_SINGLE_INSTANCE_ADDR = %q
%s
[sandbox]
network = true
`, exe, singleInstanceAddr, gateConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	entry := config.PluginEntry{
		Name: "h", Command: exe, Args: helperArgs,
		Env: map[string]string{
			"GO_WANT_DESKTOP_MCP_HELPER":       "1",
			"DESKTOP_MCP_SINGLE_INSTANCE_ADDR": singleInstanceAddr,
		},
	}
	if startGateAddr != "" {
		entry.Env["DESKTOP_MCP_START_GATE_ADDR"] = startGateAddr
	}
	cfg, err := config.LoadForRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSpecs := boot.PluginSpecsForRootWithOptions([]config.PluginEntry{entry}, dir, boot.PluginSpecOptions{
		DefaultCallTimeout: time.Duration(cfg.MCPCallTimeoutSeconds()) * time.Second,
		LaunchManager:      mcplaunch.ForWorkspace(config.DeepzoraHomeDir(), dir),
		ConfigSource:       "workspace_config",
		StateHome:          config.DeepzoraHomeDir(),
		WriterRoots:        cfg.WriteRootsForRoot(dir),
		ForbidReadRoots:    cfg.ForbidReadRootsForRoot(dir),
		Network:            cfg.Sandbox.Network,
	})
	if len(runtimeSpecs) != 1 {
		t.Fatalf("runtime specs = %d, want 1", len(runtimeSpecs))
	}
	runtimeSpec := runtimeSpecs[0]
	configure := func(spec *plugin.Spec) { *spec = runtimeSpec }
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	t.Cleanup(lifeCancel)
	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	sharedHost := plugin.NewHost()
	t.Cleanup(sharedHost.Close)
	tools, err := sharedHost.AddWithLifecycle(lifeCtx, callCtx, runtimeSpec)
	if err != nil {
		t.Fatalf("sharedHost.Add: %v", err)
	}

	activeRegistry := tool.NewRegistry()
	siblingRegistry := tool.NewRegistry()
	disabledRegistry := tool.NewRegistry()
	for _, mt := range tools {
		activeRegistry.Add(mt)
		siblingRegistry.Add(mt)
		disabledRegistry.Add(mt)
	}
	activeCtrl := control.New(control.Options{
		Host: sharedHost, Registry: activeRegistry, PluginCtx: lifeCtx,
		MCPConfigureSpec: configure, WorkspaceRoot: dir,
	})
	siblingCtrl := control.New(control.Options{
		Host: sharedHost, Registry: siblingRegistry, PluginCtx: lifeCtx,
		MCPConfigureSpec: configure, WorkspaceRoot: dir,
	})
	disabledCtrl := control.New(control.Options{
		Host: sharedHost, Registry: disabledRegistry, PluginCtx: lifeCtx,
		MCPConfigureSpec: configure, WorkspaceRoot: dir,
	})
	disabledCtrl.UnregisterMCPServerTools("h")
	app := NewApp()
	app.tabs = map[string]*WorkspaceTab{
		"active": {
			ID: "active", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: activeCtrl, SharedHostKey: dir, disabledMCP: map[string]ServerView{},
		},
		"sibling": {
			ID: "sibling", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: siblingCtrl, SharedHostKey: dir, disabledMCP: map[string]ServerView{},
		},
		"disabled": {
			ID: "disabled", Scope: "global", WorkspaceRoot: dir, Ready: true,
			Ctrl: disabledCtrl, SharedHostKey: dir,
			disabledMCP: map[string]ServerView{"h": {Name: "h", Status: "disabled"}},
		},
	}
	app.activeTabID = "active"
	return gatedDesktopMCPLaunchFixture{
		app: app, sharedHost: sharedHost,
		activeRegistry: activeRegistry, siblingRegistry: siblingRegistry, disabledRegistry: disabledRegistry,
	}
}

func newDesktopMCPStartGate(t *testing.T, handle func(attempt int, conn net.Conn)) (string, <-chan int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	attempts := make(chan int, 8)
	go func() {
		for attempt := 1; ; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			attempts <- attempt
			handle(attempt, conn)
			_ = conn.Close()
		}
	}()
	return listener.Addr().String(), attempts
}

func waitForDesktopMCPStartAttempt(t *testing.T, attempts <-chan int, want int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case got := <-attempts:
			if got == want {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for MCP start attempt %d", want)
		}
	}
}

// RemovePlugin disconnects the uninstalled plugin's MCP servers, so it must
// serialize on the MCP lifecycle lock: an unlocked disconnect interleaving
// with authorization lets the reconnect relaunch the just-removed
// server from its stale snapshot. The plugin does not need to exist — the
// lock is taken before the uninstall runs, which is the contract under test.
func TestRemovePluginSerializesWithMCPAuthorization(t *testing.T) {
	releaseConnection := make(chan struct{})
	gateAddr, attempts := newDesktopMCPStartGate(t, func(attempt int, conn net.Conn) {
		if attempt == 2 {
			<-releaseConnection
		}
		_, _ = conn.Write([]byte{1})
	})
	fixture := newGatedDesktopMCPLaunchFixture(t, gateAddr)

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
	go func() { removeDone <- fixture.app.RemovePlugin("not-an-installed-plugin") }()
	select {
	case <-removeEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("RemovePlugin did not reach the MCP lifecycle lock")
	}
	select {
	case err := <-removeDone:
		t.Fatalf("RemovePlugin bypassed authorization serialization: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseConnection)
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	// The uninstall itself is expected to fail (the plugin is not installed);
	// only the ordering matters. It must complete once the lock is free.
	select {
	case <-removeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("RemovePlugin did not complete after the trust connection released the lock")
	}
	if _, found := fixture.activeRegistry.Get("mcp__h__greet"); !found {
		t.Fatal("authorization reconnect result was lost after the serialized RemovePlugin")
	}
}

func TestCloseTabRemainsVisibleToPendingMCPHostGateSnapshot(t *testing.T) {
	fixture := newGatedDesktopMCPLaunchFixture(t, "")
	active := fixture.app.tabs["active"]
	active.turnStartMu.Lock()

	authorizeDone := make(chan error, 1)
	go func() { authorizeDone <- fixture.app.AuthorizeAndConnectMCPServer("h") }()
	waitForRuntimeAdmissionBarrier(t, fixture.app)

	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.app.CloseTab("active") }()
	time.Sleep(300 * time.Millisecond)
	fixture.app.mu.RLock()
	stillVisible := fixture.app.tabs["active"] == active
	fixture.app.mu.RUnlock()
	if !stillVisible {
		active.turnStartMu.Unlock()
		t.Fatal("CloseTab unlinked the runtime before the pending MCP Host gate snapshot")
	}
	select {
	case err := <-closeDone:
		active.turnStartMu.Unlock()
		t.Fatalf("CloseTab bypassed the lifecycle barrier: %v", err)
	default:
	}

	active.turnStartMu.Unlock()
	if err := <-authorizeDone; err != nil {
		t.Fatalf("AuthorizeAndConnectMCPServer(h): %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("CloseTab(active): %v", err)
	}
}

func TestCapabilitiesMarksBackgroundRemoteMCPAuthPossible(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "dida"
type = "http"
url = "https://mcp.dida365.com"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)
	enableProjectMCPForWorkspace(t, dir, "")

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "dida" {
			if s.Status != "deferred" || !s.Enabled || s.RuntimeState != "idle" || s.AuthStatus != "possible" || s.AuthURL != "https://mcp.dida365.com" {
				t.Fatalf("dida auth diagnosis = %+v", s)
			}
			return
		}
	}
	t.Fatalf("dida MCP missing from Capabilities: %+v", view.Servers)
}

func TestCapabilitiesDoesNotMarkRemoteMCPWithAuthHeaderPossible(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := robustTempDir(t)
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "deepzora.toml"), []byte(`
[[plugins]]
name = "stripe"
type = "http"
url = "https://mcp.stripe.com"
headers = { Authorization = "Bearer ${STRIPE_TOKEN}" }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	approveWorkspace(t, dir)

	app := NewApp()
	app.setTestCtrl(control.New(control.Options{Host: plugin.NewHost()}), "")
	defer app.activeCtrl().Close()

	view := app.Capabilities()
	for _, s := range view.Servers {
		if s.Name == "stripe" {
			if s.AuthStatus != "none" {
				t.Fatalf("stripe auth status = %q, want none; server = %+v", s.AuthStatus, s)
			}
			return
		}
	}
	t.Fatalf("stripe MCP missing from Capabilities: %+v", view.Servers)
}
