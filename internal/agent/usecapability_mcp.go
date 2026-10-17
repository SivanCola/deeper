package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"deepzora/internal/config"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

// runtimeBoundMCPTool keeps the provider-visible MCP adapter unchanged while
// binding execution to the current controller runtime. The underlying Host may
// be shared by sibling tabs, so a server name alone must never authorize reuse.
type runtimeBoundMCPTool struct {
	proxy      *UseCapabilityTool
	target     tool.Tool
	server     string
	authorized bool
}

func (b *runtimeBoundMCPTool) Name() string              { return b.target.Name() }
func (b *runtimeBoundMCPTool) Description() string       { return b.target.Description() }
func (b *runtimeBoundMCPTool) Schema() json.RawMessage   { return b.target.Schema() }
func (b *runtimeBoundMCPTool) ReadOnly() bool            { return b.target.ReadOnly() }
func (b *runtimeBoundMCPTool) MCPServerAuthorized() bool { return b.authorized }
func (b *runtimeBoundMCPTool) MCPServerName() string     { return b.server }
func (b *runtimeBoundMCPTool) MCPRawToolName() string    { return mcpRawToolName(b.target) }
func (b *runtimeBoundMCPTool) MCPDestructiveHint() bool  { return mcpDestructiveHint(b.target) }
func (b *runtimeBoundMCPTool) MCPVisibleToolName() string {
	if meta, ok := b.target.(tool.MCPVisibleMetadata); ok {
		return meta.MCPVisibleToolName()
	}
	return b.MCPRawToolName()
}
func (b *runtimeBoundMCPTool) MCPPackageName() string {
	if meta, ok := b.target.(tool.MCPPackageMetadata); ok {
		return meta.MCPPackageName()
	}
	return ""
}

func (b *runtimeBoundMCPTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var out string
	err := b.proxy.withRuntimeBoundMCP(ctx, b.server, b.target, func() error {
		var execErr error
		out, execErr = b.target.Execute(ctx, args)
		return execErr
	})
	return out, err
}

func (b *runtimeBoundMCPTool) ExecuteWithImages(ctx context.Context, args json.RawMessage) (string, []string, error) {
	var out string
	var images []string
	err := b.proxy.withRuntimeBoundMCP(ctx, b.server, b.target, func() error {
		if imageTool, ok := b.target.(tool.ImageTool); ok {
			var execErr error
			out, images, execErr = imageTool.ExecuteWithImages(ctx, args)
			return execErr
		}
		var execErr error
		out, execErr = b.target.Execute(ctx, args)
		return execErr
	})
	return out, images, err
}

func mcpRawToolName(target tool.Tool) string {
	if meta, ok := target.(tool.MCPMetadata); ok {
		return meta.MCPRawToolName()
	}
	return ""
}

// onDemandMCPTool defers MCP server startup to Execute so permission and hook
// gates always run before any subprocess or network side effect. Before the live
// handshake it remains write-capable until the resolved MCP tool is classified.
type onDemandMCPTool struct {
	proxy     *UseCapabilityTool
	spec      plugin.Spec
	server    string
	raw       string
	modelName string
	// destructive comes from the schema cache when available. A live promotion
	// is detected in Execute so a retry re-enters the current Plan/read-only
	// execution boundary.
	destructive bool
	readOnly    bool
	schema      json.RawMessage
}

func (o *onDemandMCPTool) Name() string { return o.modelName }

func (o *onDemandMCPTool) Description() string {
	return "on-demand MCP tool " + o.server + "/" + o.raw + " (connects when first used)"
}

func (o *onDemandMCPTool) Schema() json.RawMessage {
	if len(o.schema) > 0 {
		return o.schema
	}
	return json.RawMessage(`{"type":"object"}`)
}

func (o *onDemandMCPTool) ReadOnly() bool {
	return o.readOnly
}

func (o *onDemandMCPTool) ReadOnlyExecutionHostMutation() bool { return true }

func (o *onDemandMCPTool) MCPServerAuthorized() bool {
	// Spec.Authorized is the single runtime authorization result. Boot/install
	// and ResolveStoredAuthorization set it; this path never invents trust.
	return o.spec.ServerAuthorized()
}

func (o *onDemandMCPTool) ReadOnlyExecutionBlockReason() string {
	return "connect this MCP capability from a parent session first"
}

// MCPServerName/MCPRawToolName expose the deferred target for audit and
// diagnostics (tool.MCPMetadata).
func (o *onDemandMCPTool) MCPServerName() string  { return o.server }
func (o *onDemandMCPTool) MCPRawToolName() string { return o.raw }
func (o *onDemandMCPTool) MCPDestructiveHint() bool {
	return o.destructive
}

func (o *onDemandMCPTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	text, _, err := o.executeWithImages(ctx, args)
	return text, err
}

// ExecuteWithImages preserves structured MCP image results on the first call,
// when the deferred target must connect the server before dispatch. Keeping the
// resolution and safety checks in executeWithImages ensures text-only and image
// callers share the same authorization and runtime-identity boundary.
func (o *onDemandMCPTool) ExecuteWithImages(ctx context.Context, args json.RawMessage) (string, []string, error) {
	return o.executeWithImages(ctx, args)
}

func (o *onDemandMCPTool) executeWithImages(ctx context.Context, args json.RawMessage) (string, []string, error) {
	// Final runtime-bound authorization and identity check before any
	// process/network start. The read lock also linearizes this dispatch against
	// disable, uninstall, and same-name hot replacement.
	spec, unlock, err := o.proxy.lockAuthorizedRuntimeServer(ctx, o.server)
	if err != nil {
		msg := err.Error()
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, msg)
		}
		return "", nil, err
	}
	defer unlock()
	if !plugin.MCPRuntimeSpecMatches(spec, o.spec) {
		return "", nil, fmt.Errorf("MCP server %q runtime identity changed after resolution; retry so Deepzora can bind the current configuration", o.server)
	}
	tools, err := o.proxy.ensureServerToolsForSpec(ctx, o.server, spec)
	if err != nil {
		// Audit for the call path is recorded once by the agent loop
		// (noteCapabilityInvocation); only the ledger outcome lands here.
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, err.Error())
		}
		return "", nil, err
	}
	target := findMCPTool(tools, o.raw, o.modelName)
	if target == nil {
		msg := fmt.Sprintf("MCP tool %q not found on server %q", o.raw, o.server)
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-tool:"+o.server+"/"+o.raw, msg)
		}
		return "", nil, fmt.Errorf("%s", msg)
	}
	if !plugin.MCPToolMatchesSpec(target, spec) {
		return "", nil, fmt.Errorf("connected MCP server %q identity does not match the current runtime configuration; reconnect this server before retrying", o.server)
	}
	if _, err := plugin.ReconcileCachedToolSafety(o.server, o.raw, plugin.CachedToolSafety{
		ReadOnly:    o.readOnly,
		Destructive: o.destructive,
	}, target); err != nil {
		return "", nil, err
	}
	// Planner non-destructive lane and reader lane: re-check live metadata
	// before tools/call even when Reconcile did not see a cache promotion.
	if tool.HasNonDestructiveMCPExecutionIntent(ctx) {
		if !mcpServerAuthorized(target) || mcpDestructiveHint(target) {
			return "", nil, fmt.Errorf("MCP server %q changed the authorization or destructive classification for tool %q; the call was blocked before dispatch — retry so Deepzora can re-apply the current Planner MCP safety boundary", o.server, o.raw)
		}
	}
	if blocked, msg := hostValidateBeforeDispatch(target, args, "mcp-tool:"+o.server+"/"+o.raw); blocked {
		return "", nil, fmt.Errorf("%s", msg)
	}
	if imageTool, ok := target.(tool.ImageTool); ok {
		return imageTool.ExecuteWithImages(ctx, args)
	}
	text, err := target.Execute(ctx, args)
	return text, nil, err
}

func (t *UseCapabilityTool) ensureServerToolsForSpec(ctx context.Context, server string, spec plugin.Spec) ([]tool.Tool, error) {
	// Reuse shared host if already connected (including auto-started).
	if t.host.HasClient(server) {
		return t.serverToolsForSpec(ctx, server, spec)
	}
	// The session owns the child and handshake; a slow server keeps connecting
	// in the background after this call's wait budget. Its tools stay off the
	// main provider-visible registry.
	life := t.lifeCtx
	if life == nil {
		life = context.Background()
	}
	started := time.Now()
	result := t.host.EnsureConnectedInBackground(life, spec)
	waitBudget := plugin.DefaultStartupWaitBudget()
	timer := time.NewTimer(waitBudget)
	defer timer.Stop()
	var tools []tool.Tool
	var err error
	select {
	case connected := <-result:
		tools, err = connected.Tools, connected.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("MCP server %q is still initializing after %s; startup continues in background (limit %s) — retry on a later turn",
			server, waitBudget, spec.ResolvedStartupTimeout())
	}
	if err != nil {
		if plugin.IsServerAlreadyConnected(err) {
			return t.serverToolsForSpec(ctx, server, spec)
		}
		t.host.RecordFailure(spec, err)
		return nil, fmt.Errorf("connect %q: %w", server, err)
	}
	t.ensureState().markConnected(server)
	schemaBytes := 0
	for _, target := range tools {
		schemaBytes += len(target.Schema())
	}
	durationMs := time.Since(started).Milliseconds()
	t.capabilityAudit().RecordMCPList("remote", "connect", durationMs, len(tools), schemaBytes)
	t.observeMCPList(mcpListObservation{
		Server: server, Source: "remote", Trigger: "connect", DurationMs: durationMs,
		ToolCount: len(tools), SchemaBytes: schemaBytes, NetworkCall: true,
	})
	// Intentionally do NOT add tools to t.registry — provider schema stays stable.
	_ = tools
	return t.serverToolsForSpec(ctx, server, spec)
}

// serverTools fetches the live tools for a connected server and refreshes the
// shared catalog snapshot so mcp-tool entries stay routable once the server
// is StatusReady (its tools are absent from the provider-visible registry).
func (t *UseCapabilityTool) serverTools(ctx context.Context, server string) ([]tool.Tool, error) {
	spec, unlock, err := t.lockAuthorizedRuntimeServer(ctx, server)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return t.serverToolsForSpec(ctx, server, spec)
}

func (t *UseCapabilityTool) serverToolsForSpec(ctx context.Context, server string, spec plugin.Spec) ([]tool.Tool, error) {
	tools, err := t.host.ToolsForSpec(ctx, spec)
	if err != nil {
		return nil, err
	}
	t.ensureState().setLiveTools(server, snapshotMCPTools(tools))
	return tools, nil
}

// ConnectedProxyTools returns raw tool metadata for servers connected through
// any frontend sharing this proxy state, keyed by server name. Catalog builders
// consume it so concrete mcp-tool capabilities survive an on-demand connect
// without ever touching the provider-visible registry.
func (t *UseCapabilityTool) ConnectedProxyTools() map[string][]plugin.CachedTool {
	if t == nil {
		return nil
	}
	return t.ensureState().snapshotLiveTools()
}

func (t *UseCapabilityTool) ensureState() *mcpProxySharedState {
	if t.state == nil {
		t.state = &mcpProxySharedState{connected: map[string]bool{}}
	}
	return t.state
}

// specFor looks up the boot-converted spec for server. The proxy deliberately
// holds []plugin.Spec, not raw config entries: env expansion, workspace
// overrides, call timeouts, and read-only tool names all live in the
// shared conversion and must not be re-derived here.
func (t *UseCapabilityTool) specFor(server string) (plugin.Spec, bool) {
	if t.runtime != nil {
		return t.runtime.enabledSpec(server)
	}
	for _, s := range t.specs {
		if s.Name == server {
			return s, true
		}
	}
	return plugin.Spec{}, false
}

// lockAuthorizedRuntimeServer acquires the shared runtime dispatch read lock
// and returns the current enabled, authorized spec. The caller must keep the
// returned lock until the identity-bound Host operation or tools/call has
// crossed its dispatch boundary; lifecycle mutations take the write lock.
func (t *UseCapabilityTool) lockAuthorizedRuntimeServer(ctx context.Context, server string) (plugin.Spec, func(), error) {
	if t.runtime == nil {
		spec, ok := t.specFor(server)
		if !ok {
			return plugin.Spec{}, func() {}, mcpServerUnregisteredError(server)
		}
		spec = plugin.ResolveStoredAuthorization(ctx, spec)
		if !spec.ServerAuthorized() {
			return plugin.Spec{}, func() {}, fmt.Errorf("MCP server %q is not authorized; install it or complete project identity approval before connecting", server)
		}
		return spec, func() {}, nil
	}

	t.runtime.dispatchMu.RLock()
	unlock := t.runtime.dispatchMu.RUnlock
	t.runtime.mu.RLock()
	configured, ok := t.runtime.servers[strings.TrimSpace(server)]
	t.runtime.mu.RUnlock()
	if !ok {
		unlock()
		return plugin.Spec{}, func() {}, mcpServerUnregisteredError(server)
	}
	if !configured.enabled {
		unlock()
		return plugin.Spec{}, func() {}, mcpServerOffError(server, configured.decision)
	}
	spec := plugin.ResolveStoredAuthorization(ctx, cloneMCPSpec(configured.spec))
	if !spec.ServerAuthorized() {
		unlock()
		return plugin.Spec{}, func() {}, fmt.Errorf("MCP server %q is not authorized; install it or complete project identity approval before connecting", server)
	}
	return spec, unlock, nil
}

func (t *UseCapabilityTool) bindRuntimeMCP(spec plugin.Spec, target tool.Tool) tool.Tool {
	if t.runtime == nil || target == nil {
		return target
	}
	return &runtimeBoundMCPTool{
		proxy:      t,
		target:     target,
		server:     spec.Name,
		authorized: spec.ServerAuthorized(),
	}
}

func (t *UseCapabilityTool) withRuntimeBoundMCP(ctx context.Context, server string, target tool.Tool, execute func() error) error {
	if t.runtime == nil {
		return execute()
	}
	spec, unlock, err := t.lockAuthorizedRuntimeServer(ctx, server)
	if err != nil {
		return err
	}
	defer unlock()
	if !plugin.MCPToolMatchesSpec(target, spec) {
		return fmt.Errorf("connected MCP server %q identity does not match the current runtime configuration; reconnect this server before retrying", server)
	}
	return t.runtime.withServerGate(ctx, server, execute)
}

func (t *UseCapabilityTool) serverEnabled(server string) bool {
	if t.runtime != nil {
		return t.runtime.serverEnabled(server)
	}
	// Standalone proxies predate the authoritative runtime and may resolve
	// already-registered MCP tools without carrying a duplicate spec slice.
	return true
}

func (t *UseCapabilityTool) configuredServers() []mcpRuntimeServer {
	if t.runtime != nil {
		return t.runtime.configuredServers()
	}
	servers := make([]mcpRuntimeServer, 0, len(t.specs))
	seen := map[string]bool{}
	for _, raw := range t.specs {
		spec := cloneMCPSpec(raw)
		name := strings.TrimSpace(spec.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		servers = append(servers, mcpRuntimeServer{
			entry:   config.PluginEntry{Name: name},
			spec:    spec,
			enabled: true,
		})
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].spec.Name < servers[j].spec.Name })
	return servers
}

// parseMCPServerCapabilityID extracts the server name from an mcp-server id.
func parseMCPServerCapabilityID(id string) (string, bool) {
	if !strings.HasPrefix(id, "mcp-server:") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(id, "mcp-server:"))
	return name, name != ""
}

// resolveServerConnect resolves action=call on an mcp-server id. A connected
// server lists its tools immediately (side-effect free); an unconnected one
// resolves to a deferred connect target that runs only after the permission
// gate and PreToolUse hooks approve it. Stored project authorization is applied
// at resolve time so unauthorized project MCP never reaches process startup.
func (t *UseCapabilityTool) resolveServerConnect(ctx context.Context, server string, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	id := "mcp-server:" + server
	if !t.serverEnabled(server) {
		return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), t.serverUnavailableReason(server)), nil
	}
	if t.host != nil && t.host.HasClient(server) {
		out, err := t.listServerTools(ctx, server)
		if err != nil {
			return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), err.Error()), nil
		}
		base.SkipExecute = true
		base.HostCompleted = true
		base.Result = out
		base.ReadOnly = true
		return base, nil
	}
	spec, unlock, err := t.lockAuthorizedRuntimeServer(ctx, server)
	if err != nil {
		return t.resolveUnavailable(base, id, plugin.ToolPrefix(server), err.Error()), nil
	}
	unlock()
	connect := &onDemandMCPConnect{proxy: t, spec: spec, server: server}
	base.Target = connect
	// A dedicated exact identity names the connect for permission and hook
	// rules. It cannot collide with a real mcp__ tool, and rules do not need to
	// rely on unsupported tool-name glob matching.
	base.TargetName = connect.Name()
	// Connecting spawns a subprocess, so it is never a read-only fast path for
	// ordinary Plan/strict agents. PlannerMCPExecution may allow authorized
	// connects; unauthorized specs are blocked before process/network start.
	base.ReadOnly = false
	base.Args = json.RawMessage(`{}`)
	return base, nil
}

// onDemandMCPConnect is the deferred first-discovery target: it connects the
// server post-approval and returns the live tool directory.
type onDemandMCPConnect struct {
	proxy  *UseCapabilityTool
	spec   plugin.Spec
	server string
}

func (o *onDemandMCPConnect) Name() string { return plugin.MCPConnectPermissionName(o.server) }

func (o *onDemandMCPConnect) Description() string {
	return "connect MCP server " + o.server + " on demand and list its tools"
}

func (o *onDemandMCPConnect) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (o *onDemandMCPConnect) ReadOnly() bool { return false }

// MCPLifecycleConnect marks this target as an MCP connect-and-list lifecycle
// action for Planner authorization (not a remote tools/call).
func (o *onDemandMCPConnect) MCPLifecycleConnect() bool { return true }

func (o *onDemandMCPConnect) MCPServerAuthorized() bool {
	return o.spec.ServerAuthorized()
}

func (o *onDemandMCPConnect) MCPServerName() string { return o.server }

func (o *onDemandMCPConnect) ReadOnlyExecutionHostMutation() bool { return true }

func (o *onDemandMCPConnect) ReadOnlyExecutionBlockReason() string {
	if !o.spec.ServerAuthorized() {
		return "start an unauthorized MCP server (install it or complete project identity approval first)"
	}
	return "connect this MCP server from a parent session first"
}

func (o *onDemandMCPConnect) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	// Zero process/network start when authorization, enable state, or exact
	// runtime identity changed after resolve.
	spec, unlock, err := o.proxy.lockAuthorizedRuntimeServer(ctx, o.server)
	if err != nil {
		msg := err.Error()
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-server:"+o.server, msg)
		}
		return "", err
	}
	defer unlock()
	if !plugin.MCPRuntimeSpecMatches(spec, o.spec) {
		return "", fmt.Errorf("MCP server %q runtime identity changed after resolution; retry so Deepzora can bind the current configuration", o.server)
	}
	if _, err := o.proxy.ensureServerToolsForSpec(ctx, o.server, spec); err != nil {
		if o.proxy.ledger != nil {
			o.proxy.ledger.MarkUnavailable("mcp-server:"+o.server, err.Error())
		}
		return "", err
	}
	return o.proxy.listServerToolsForSpec(ctx, o.server, spec)
}

func parseMCPCapabilityID(id string) (server, raw string, err error) {
	id = strings.TrimSpace(id)
	switch {
	case strings.HasPrefix(id, "mcp-tool:"):
		rest := strings.TrimPrefix(id, "mcp-tool:")
		server, raw, ok := strings.Cut(rest, "/")
		if !ok || server == "" || raw == "" {
			return "", "", fmt.Errorf("invalid mcp-tool id %q; want mcp-tool:<server>/<tool>", id)
		}
		return server, raw, nil
	case strings.HasPrefix(id, "mcp-server:"):
		return "", "", fmt.Errorf("%q is a server id; call it directly to connect and list tools, or use mcp-tool:<server>/<tool>", id)
	default:
		return "", "", fmt.Errorf("action=call requires an mcp-tool capability id, got %q", id)
	}
}
