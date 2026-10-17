package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"deepzora/internal/agentpreset"
	"deepzora/internal/capability"
	"deepzora/internal/config"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

func TestAgentPresetCeilingSurvivesRegistryAndCapabilityChildren(t *testing.T) {
	preset := agentpreset.Preset{ID: "reader", Tools: []string{"read_file", "mcp:allowed"}}
	reg := tool.NewRegistry()
	reg.SetAdmission(preset.ToolAdmission(plugin.ToolPrefix))
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	reg.Add(fakeTool{name: "write_file"})
	proxy := NewUseCapabilityTool(t.Context(), nil, nil, reg, capability.NewLedger(), nil, nil)
	reg.Add(proxy)
	for _, name := range []string{"write_file", "bash", "mcp__blocked__write"} {
		reg.Add(fakeTool{name: name})
		if _, ok := reg.Get(name); ok {
			t.Fatalf("hot registration escaped preset: %s", name)
		}
	}
	for _, child := range []*tool.Registry{
		reg,
		SubagentToolRegistry(reg, []string{"read_file", "write_file", "use_capability"}),
		ReadOnlySubagentToolRegistry(reg, []string{"read_file", "write_file", "use_capability"}),
		PlannerToolRegistry(reg),
	} {
		if _, ok := child.Get("write_file"); ok {
			t.Fatal("child registry widened preset tool ceiling")
		}
		frontend, ok := child.Get("use_capability")
		if !ok {
			t.Fatal("child lost isolated capability proxy")
		}
		resolver := frontend.(tool.CallResolver)
		for _, id := range []string{"tool:write_file", "workflow:write_file", "task:subagent"} {
			args, _ := json.Marshal(map[string]string{"action": "call", "capability_id": id})
			resolved, err := resolver.ResolveCall(t.Context(), args)
			if err == nil && (!resolved.Unavailable || resolved.Target != nil) {
				t.Fatalf("proxy widened preset via %q: %+v", id, resolved)
			}
		}
	}
}

func TestAgentPresetMCPServerCeilingSurvivesSharedHostAndHotEnable(t *testing.T) {
	t.Setenv("DEEPZORA_CACHE_HOME", t.TempDir())
	var loaded atomic.Bool
	var calls atomic.Int32
	server := dynamicToolsMCPServer(t, &loaded, &calls)
	defer server.Close()
	host := plugin.NewHost()
	defer host.Close()
	specs := []plugin.Spec{
		{Name: "allowed", Type: "http", URL: server.URL, Authorized: true},
		{Name: "blocked", Type: "http", URL: server.URL, Authorized: true},
	}
	// Another tab may have already connected a server excluded by this preset.
	if _, err := host.Add(t.Context(), specs[1]); err != nil {
		t.Fatal(err)
	}
	preset := agentpreset.Preset{ID: "reader", Tools: []string{"mcp:allowed"}}
	reg := tool.NewRegistry()
	reg.SetAdmission(preset.ToolAdmission(plugin.ToolPrefix))
	runtime := NewMCPCapabilityRuntime(t.Context(), host, nil, reg, nil)
	runtime.SetServerAdmission(preset.AdmitsMCPServer)
	entries := []config.PluginEntry{{Name: "allowed"}, {Name: "blocked"}}
	runtime.ConfigureServers(entries, specs, nil)
	frontends := []tool.Tool{runtime.NewFrontend(nil, nil)}
	reg.Add(frontends[0])
	child := SubagentToolRegistryForDepthWithRuntime(reg, []string{"use_capability", "mcp-server:blocked"}, 1, 2, runtime)
	childProxy, ok := child.Get("use_capability")
	if !ok {
		t.Fatal("missing child proxy")
	}
	frontends = append(frontends, childProxy)
	for _, frontend := range frontends {
		for _, id := range []string{"mcp-server:blocked", "mcp-tool:blocked/load_toolset"} {
			args, _ := json.Marshal(map[string]string{"action": "call", "capability_id": id})
			resolved, err := frontend.(tool.CallResolver).ResolveCall(t.Context(), args)
			if err == nil && (!resolved.Unavailable || resolved.Target != nil) {
				t.Fatalf("shared-host preset escape %q: %+v", id, resolved)
			}
		}
	}
	if !runtime.SetServerEnabled("blocked", true) {
		t.Fatal("blocked spec missing from runtime")
	}
	runtime.UpsertServer(entries[1], specs[1], true)
	_, _, _, disabled := runtime.CatalogState()
	if !disabled["blocked"] || runtime.serverEnabled("blocked") || !runtime.serverEnabled("allowed") {
		t.Fatalf("hot enable widened server ceiling: disabled=%v", disabled)
	}
	frontend := frontends[0].(*UseCapabilityTool)
	resolved, err := frontend.ResolveCall(context.Background(), json.RawMessage(`{"action":"call","capability_id":"mcp-tool:allowed/load_toolset"}`))
	if err != nil || resolved.Unavailable || resolved.Target == nil {
		t.Fatalf("whole allowed server is unusable: %+v %v", resolved, err)
	}
	if _, err := resolved.Target.Execute(t.Context(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
