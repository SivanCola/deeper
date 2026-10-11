package boot

import (
	"deepzora/internal/tool"
)

// CoreProviderToolNames is the stable top-level tool surface shared by every
// Agent role setting under identical configuration. Host-control tools
// (ask, create_goal, get_goal, update_goal, todo_write) are appended when enabled.
func CoreProviderToolNames() []string {
	return []string{
		"bash",
		"job_output",
		"job_kill",
		"read_file",
		"view_image",
		"edit_file",
		"write_file",
		"compress",
		"use_capability",
		"web_search",
	}
}

func coreProviderToolNamesForRegistry(reg *tool.Registry) []string {
	names := CoreProviderToolNames()
	if reg == nil {
		return names
	}
	if _, ok := reg.Get("pwsh"); !ok {
		return names
	}
	for i, name := range names {
		if name == "bash" {
			names[i] = "pwsh"
			break
		}
	}
	return names
}

// HostControlToolNames are collaboration/contract tools that may appear in the
// provider schema independently of the Agent role setting.
func HostControlToolNames() []string {
	return []string{
		"ask",
		"create_goal",
		"get_goal",
		"update_goal",
		"todo_write",
	}
}

// UnifiedProviderToolNames returns the provider-visible allowlist for a boot
// with host-control tools enabled.
func UnifiedProviderToolNames() []string {
	core := CoreProviderToolNames()
	host := HostControlToolNames()
	out := make([]string, 0, len(core)+len(host))
	out = append(out, core...)
	out = append(out, host...)
	return out
}

// applyUnifiedProviderToolSurface restricts Schemas/ContractEntries to the
// shared core + host-control tools. use_capability can still Get every
// registered tool, including those hidden from the provider schema.
func applyUnifiedProviderToolSurface(reg *tool.Registry) {
	if reg == nil {
		return
	}
	allow := make([]string, 0, 16)
	names := coreProviderToolNamesForRegistry(reg)
	names = append(names, HostControlToolNames()...)
	for _, name := range names {
		if _, ok := reg.Get(name); ok {
			allow = append(allow, name)
		}
	}
	// Always keep use_capability if somehow only that remains.
	if len(allow) == 0 {
		if _, ok := reg.Get("use_capability"); ok {
			allow = []string{"use_capability"}
		}
	}
	reg.SetProviderVisibleTools(allow)
}
