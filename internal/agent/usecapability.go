package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"deepzora/internal/capability"
	"deepzora/internal/event"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

// UseCapabilityTool is the stable MCP capability proxy for Delivery, the
// two-model Planner, and task/fleet sub-agents. It lists, inspects, calls, or
// declines catalog capabilities without adding dynamic MCP tools to the
// provider-visible registry — subsequent calls keep using this stable schema.
// Multiple frontends may share one MCPCapabilityRuntime (Host + connection
// state) while keeping independent ledger/audit.
type UseCapabilityTool struct {
	host *plugin.Host
	// lifeCtx is the session-scoped context that owns on-demand MCP child
	// processes (mirrors lazySpawn.ctx): a proxied server must outlive the tool
	// call that started it and die with the session, not with a resolve-phase
	// timeout. nil falls back to context.Background() for direct/test use.
	lifeCtx context.Context
	// specs are the boot-converted plugin specs (env expansion, workspace
	// overrides and timeouts). The proxy never rebuilds
	// specs from raw config entries — that would fork the conversion logic.
	specs    []plugin.Spec
	runtime  *MCPCapabilityRuntime
	registry *tool.Registry // live registry for already-exposed MCP tools
	ledger   *capability.Ledger
	audit    *capability.Audit
	catalog  func() capability.Catalog
	// toolResultSession is bound by Agent.New; clones leave it empty so parent transcripts cannot
	// leak into planner or child frontends before their Agent binds them.
	toolResultMu      sync.RWMutex
	toolResultSession func() *Session
	mcpListMu         sync.RWMutex
	mcpListObserver   func(mcpListObservation)
	// state is session-shared connection observation when built via
	// MCPCapabilityRuntime; nil falls back to a private map for tests.
	state *mcpProxySharedState
}

// NewUseCapabilityTool builds a standalone capability proxy (tests and simple
// boots). Prefer MCPCapabilityRuntime.NewFrontend when multiple agents share
// one session Host.
func NewUseCapabilityTool(lifeCtx context.Context, host *plugin.Host, specs []plugin.Spec, reg *tool.Registry, ledger *capability.Ledger, audit *capability.Audit, catalog func() capability.Catalog) *UseCapabilityTool {
	return &UseCapabilityTool{
		host:     host,
		lifeCtx:  lifeCtx,
		specs:    append([]plugin.Spec(nil), specs...),
		registry: reg,
		ledger:   ledger,
		audit:    audit,
		catalog:  catalog,
		state:    &mcpProxySharedState{connected: map[string]bool{}},
	}
}

// CloneForAgent returns a new frontend sharing Host/specs/connection state but
// with independent ledger and audit (nil unless provided).
func (t *UseCapabilityTool) CloneForAgent(ledger *capability.Ledger, audit *capability.Audit) *UseCapabilityTool {
	if t == nil {
		return nil
	}
	state := t.state
	if state == nil {
		state = &mcpProxySharedState{connected: map[string]bool{}}
	}
	clone := &UseCapabilityTool{
		host:     t.host,
		lifeCtx:  t.lifeCtx,
		specs:    t.specs,
		runtime:  t.runtime,
		registry: t.registry,
		ledger:   ledger,
		audit:    audit,
		catalog:  t.catalog,
		state:    state,
	}
	return clone
}

func (*UseCapabilityTool) Name() string { return tool.HostUseCapability }

func (*UseCapabilityTool) Description() string {
	return "Fixed-schema capability proxy. Prefer search(query, limit<=8), then inspect one exact capability, then call it. list is a compact diagnostic inventory only. Supports stable ids such as tool:grep, skill:review, mcp-tool:server/tool, task:subagent, workflow:name, and web:/lsp:/session:/memory: namespaces. memory:remember saves facts (description+body required; activation=\"relevant\" on create; omit activation on update; \"pinned\" only if user asks); memory:forget(name); tool:memory(operation=search|read|list). decline records a reason for a prefer capability. Independent list/search/inspect calls are read-only and may be issued together. Calls keep the provider-visible schema fixed; real writers still pass permission, plan mode, sandbox, write-path, and workspace-lease checks."
}

func (*UseCapabilityTool) ReadOnly() bool { return true }

func (*UseCapabilityTool) Schema() json.RawMessage {
	// Stable schema — must not change across turns or when MCP connects.
	// capability_id is optional only for action=list; inspect/call/decline still
	// require it at resolve time. One intentional prefix upgrade; thereafter
	// install/connect churn does not change this schema.
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","enum":["list","search","inspect","call","decline"],"description":"Use search for discovery, inspect one exact result, then call. list is diagnostic only."},
			"capability_id":{"type":"string","description":"Capability id such as skill:review, mcp-server:github, or mcp-tool:github/search_issues. Not required for action=list."},
			"query":{"type":"string","description":"Local catalog query required for action=search. No process or network is started."},
			"limit":{"type":"integer","minimum":1,"maximum":100,"description":"Maximum results. Search defaults to 5 and allows at most 8; list defaults to 50 and allows at most 100."},
			"cursor":{"type":"string","description":"Opaque cursor returned by action=list. It is valid only for the same catalog version."},
			"arguments":{"type":"object","description":"Raw MCP tool arguments for action=call"},
			"reason":{"type":"string","description":"Required non-empty reason when action=decline"}
		},
		"required":["action"],
		"additionalProperties":false
	}`)
}

// ResolveCall implements tool.CallResolver so the agent can run permission,
// hooks, and evidence against the real MCP target before execution.
func (t *UseCapabilityTool) ResolveCall(ctx context.Context, args json.RawMessage) (tool.ResolvedCall, error) {
	p, action, id, err := parseUseCapabilityArgs(args)
	if err != nil {
		return tool.ResolvedCall{}, &capabilityInputError{err}
	}
	base := tool.ResolvedCall{
		DisplayName:  "use_capability",
		ProxyAction:  action,
		CapabilityID: id,
		Args:         p.Arguments,
	}
	switch action {
	case "list", "search", "inspect":
		return t.resolveDiscovery(ctx, p, action, id, base)
	case "decline":
		if id == "" {
			return tool.ResolvedCall{}, capabilityInputErrorf("capability_id is required for action=decline")
		}
		reason := strings.TrimSpace(p.Reason)
		if reason == "" {
			return tool.ResolvedCall{}, capabilityInputErrorf("reason is required for action=decline")
		}
		// Decline must not skip require. The mutation itself is delayed until the
		// agent has applied its post-resolution host boundary.
		if t.ledger != nil {
			if e, ok := t.ledger.Get(id); ok && e.Policy == capability.AutoUseRequire {
				return tool.ResolvedCall{}, fmt.Errorf("cannot decline a require capability %q", id)
			}
		}
		base.SkipExecute = true
		base.Result = fmt.Sprintf("declined capability %s: %s", id, reason)
		base.ReadOnly = true
		base.Commit = func() error {
			if t.ledger != nil {
				if err := t.ledger.MarkDeclined(id, reason); err != nil {
					return err
				}
			}
			if t.audit != nil {
				t.audit.RecordDecline()
			}
			return nil
		}
		return base, nil
	case "call":
		if id == "" {
			return tool.ResolvedCall{}, capabilityInputErrorf("capability_id is required for action=call")
		}
		if id == sessionToolResultCapabilityID {
			return t.resolveSessionToolResult(p.Arguments, base)
		}
		return t.resolveCall(ctx, id, p.Arguments, base)
	default:
		return tool.ResolvedCall{}, capabilityInputErrorf("unknown action %q; use list, search, inspect, call, or decline", p.Action)
	}
}

func (t *UseCapabilityTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	resolved, err := t.ResolveCall(ctx, args)
	if err != nil {
		return "", err
	}
	if resolved.SkipExecute {
		if resolved.Commit != nil {
			if err := resolved.Commit(); err != nil {
				return "", err
			}
		}
		if resolved.ProxyAction == "call" && !resolved.Unavailable {
			if t.ledger != nil {
				t.ledger.MarkSucceeded(resolved.CapabilityID)
			}
			if t.audit != nil {
				t.audit.RecordMCPProxy(false, true, false)
			}
		}
		return resolved.Result, nil
	}
	if resolved.Unavailable {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(resolved.CapabilityID, resolved.UnavailableReason)
		}
		return "", fmt.Errorf("capability unavailable: %s", resolved.UnavailableReason)
	}
	if resolved.Target == nil {
		return "", fmt.Errorf("no target tool resolved for %s", resolved.CapabilityID)
	}
	if t.ledger != nil {
		t.ledger.MarkInvoked(resolved.CapabilityID)
	}
	if t.audit != nil {
		t.audit.RecordMCPProxy(false, true, false)
	}
	out, err := resolved.Target.Execute(ctx, resolved.Args)
	if err != nil {
		if t.ledger != nil {
			t.ledger.MarkFailed(resolved.CapabilityID, err.Error())
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return out, err
	}
	if t.ledger != nil {
		t.ledger.MarkSucceeded(resolved.CapabilityID)
	}
	return out, nil
}

func (t *UseCapabilityTool) resolveCall(ctx context.Context, id string, args json.RawMessage, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	// Registry-backed tools and skills share the unified proxy. Real writers
	// still pass permission/plan/sandbox/lease checks via ResolvedCall.Target.
	if name, ok := strings.CutPrefix(id, "tool:"); ok {
		return t.resolveRegistryTool(ctx, strings.TrimSpace(name), id, args, base)
	}
	if name, ok := strings.CutPrefix(id, "skill:"); ok {
		return t.resolveSkillCall(strings.TrimSpace(name), id, args, base)
	}
	if name, ok := strings.CutPrefix(id, "task:"); ok {
		return t.resolveRegistryTool(ctx, taskToolName(name), id, args, base)
	}
	if name, ok := strings.CutPrefix(id, "workflow:"); ok {
		return t.resolveRegistryTool(ctx, strings.TrimSpace(name), "tool:"+strings.TrimSpace(name), args, base)
	}
	for _, prefix := range []string{"web:", "lsp:", "session:", "memory:"} {
		if rest, ok := strings.CutPrefix(id, prefix); ok {
			return t.resolveRegistryTool(ctx, strings.TrimSpace(rest), id, args, base)
		}
	}
	// Server-level call is the first-discovery path for servers with no
	// schema cache: it resolves to a gated connect-and-list target so the
	// model can learn tool names without inspect ever starting a process.
	if server, ok := parseMCPServerCapabilityID(id); ok {
		return t.resolveServerConnect(ctx, server, base)
	}
	server, raw, err := parseMCPCapabilityID(id)
	if err != nil {
		return tool.ResolvedCall{}, err
	}
	if !t.serverEnabled(server) {
		return t.resolveUnavailable(base, id, plugin.ModelToolName(server, raw), t.serverUnavailableReason(server)), nil
	}
	var runtimeSpec plugin.Spec
	releaseRuntime := func() {}
	if t.runtime != nil {
		spec, unlock, lockErr := t.lockAuthorizedRuntimeServer(ctx, server)
		if lockErr != nil {
			return t.resolveUnavailable(base, id, plugin.ModelToolName(server, raw), lockErr.Error()), nil
		}
		releaseRuntime = unlock
		defer func() { releaseRuntime() }()
		runtimeSpec = spec
	}
	// Prefer already-exposed registry tool (auto-started MCP). The model name
	// MUST come from the plugin layer's canonical constructor: it appends a
	// collision hash for sanitised raw names, and permission/hook rules are
	// written against that executed name — a proxy-local normalization would
	// let them silently miss.
	modelName := plugin.ModelToolName(server, raw)
	if t.registry != nil {
		if tl, ok := t.registry.Get(modelName); ok {
			if t.runtime != nil && !plugin.MCPToolMatchesSpec(tl, runtimeSpec) {
				return t.resolveUnavailable(base, id, modelName, fmt.Sprintf("connected MCP server %q identity does not match the current runtime configuration", server)), nil
			}
			base.TargetName = modelName
			base.Target = t.bindRuntimeMCP(runtimeSpec, tl)
			base.ReadOnly = tl.ReadOnly()
			if len(args) == 0 {
				base.Args = json.RawMessage(`{}`)
			} else {
				base.Args = args
			}
			return base, nil
		}
	}
	// Server already connected (auto-started, a previous proxy call, or a
	// sibling tab sharing this host): resolving against live tools is
	// side-effect-free. serverTools also refreshes the catalog snapshot so a
	// cross-tab connect still yields routable mcp-tool entries here.
	if t.host != nil && t.host.HasClient(server) {
		var tools []tool.Tool
		if t.runtime != nil {
			tools, err = t.serverToolsForSpec(ctx, server, runtimeSpec)
		} else {
			tools, err = t.serverTools(ctx, server)
		}
		if err != nil {
			return t.resolveUnavailable(base, id, modelName, err.Error()), nil
		}
		target := findMCPTool(tools, raw, modelName)
		if target == nil {
			return t.resolveUnavailable(base, id, modelName, fmt.Sprintf("MCP tool %q not found on server %q", raw, server)), nil
		}
		base.Target = t.bindRuntimeMCP(runtimeSpec, target)
		base.TargetName = target.Name()
		base.ReadOnly = target.ReadOnly()
		if len(args) == 0 {
			base.Args = json.RawMessage(`{}`)
		} else {
			base.Args = args
		}
		return base, nil
	}
	// Unconnected server: resolution must stay pure — no subprocess, no network.
	// Return a deferred target that connects in Execute, after the permission
	// gate and PreToolUse hooks have approved the real target name/arguments.
	spec := runtimeSpec
	if t.runtime == nil {
		var ok bool
		spec, ok = t.specFor(server)
		if !ok {
			return t.resolveUnavailable(base, id, modelName, mcpServerUnregisteredMessage(server)), nil
		}
		spec = plugin.ResolveStoredAuthorization(ctx, spec)
	}
	// Execute rechecks the immutable spec snapshot. Release dispatchMu because
	// Boot's catalog callback takes its own runtime snapshot.
	releaseRuntime()
	releaseRuntime = func() {}
	return t.resolveUnconnectedMCPCall(id, args, base, spec, server, raw, modelName), nil
}

// resolveSkillCall routes skill:<name> through run_skill / read_only_skill /
// read_skill when present, preserving the real skill tool name for evidence.
func (t *UseCapabilityTool) resolveSkillCall(skillName, id string, args json.RawMessage, base tool.ResolvedCall) (tool.ResolvedCall, error) {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return tool.ResolvedCall{}, fmt.Errorf("capability id %q is missing a skill name", id)
	}
	if t.registry == nil {
		return t.resolveUnavailable(base, id, skillName, "tool registry is unavailable"), nil
	}
	// Prefer full run_skill; fall back to read-only variants when the session
	// only exposes them (planner / plan mode).
	for _, toolName := range []string{"run_skill", "read_only_skill", "read_skill"} {
		if tl, ok := t.registry.Get(toolName); ok {
			payload := normalizeSkillCapabilityArgs(skillName, args)
			base.Target = tl
			base.TargetName = toolName
			base.ReadOnly = tl.ReadOnly()
			base.Args = payload
			return base, nil
		}
	}
	return t.resolveUnavailable(base, id, skillName, fmt.Sprintf("skill tools are not available for %q", skillName)), nil
}

func taskToolName(name string) string {
	name = strings.TrimSpace(name)
	switch name {
	case "subagent", "task":
		return "task"
	case "read_only_subagent", "read_only_task", "research":
		return "read_only_task"
	case "parallel", "parallel_tasks":
		return "parallel_tasks"
	case "fleet":
		return "fleet"
	default:
		return name
	}
}

// resolveUnavailable fills the host-proven unavailable shape shared by the
// side-effect-free resolution failures (missing config, unknown tool).
func (t *UseCapabilityTool) resolveUnavailable(base tool.ResolvedCall, id, modelName, reason string) tool.ResolvedCall {
	base.Unavailable = true
	base.UnavailableReason = reason
	base.SkipExecute = true
	base.Result = "capability unavailable: " + reason
	base.TargetName = modelName
	base.ReadOnly = false
	base.Commit = func() error {
		if t.ledger != nil {
			t.ledger.MarkUnavailable(id, reason)
		}
		if t.audit != nil {
			t.audit.RecordMCPProxy(false, true, true)
		}
		return nil
	}
	return base
}

// findMCPTool matches a server's tool list by raw MCP name or by the
// canonical namespaced model-visible name (plugin.ModelToolName).
func findMCPTool(tools []tool.Tool, raw, modelName string) tool.Tool {
	for _, tl := range tools {
		if m, ok := tl.(tool.MCPMetadata); ok && m.MCPRawToolName() == raw {
			return tl
		}
		if tl.Name() == modelName {
			return tl
		}
	}
	return nil
}

func (t *UseCapabilityTool) currentCatalog() capability.Catalog {
	if t.catalog != nil {
		return t.catalog()
	}
	return capability.Catalog{}
}

// Ensure UseCapabilityTool satisfies the tool contracts used by the agent.
var (
	_ tool.Tool            = (*UseCapabilityTool)(nil)
	_ tool.CallResolver    = (*UseCapabilityTool)(nil)
	_ tool.BatchClassifier = (*UseCapabilityTool)(nil)
)

// EmitProxyAudit is a helper for frontends: returns a notice describing the
// proxy name and real target for user audit trails.
func EmitProxyAudit(sink event.Sink, resolved tool.ResolvedCall, callIDs ...string) {
	if sink == nil || resolved.TargetName == "" {
		return
	}
	callID := ""
	if len(callIDs) > 0 {
		callID = callIDs[0]
	}
	detail, _ := json.Marshal(map[string]string{"callId": callID, "capabilityId": resolved.CapabilityID, "target": resolved.TargetName})
	sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelInfo,
		Code:   "capability_proxy_audit",
		Text:   capabilityProxyNoticeText(resolved.DisplayName, resolved.TargetName),
		Detail: string(detail),
	})
}
