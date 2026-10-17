package agentpreset

import (
	"path/filepath"
	"strings"
)

// alwaysAdmitted are routing tools that grant nothing by themselves:
// use_capability reaches only tools and servers the same admission allows.
var alwaysAdmitted = map[string]bool{"use_capability": true}

// shellAliases keep one allowlist portable across bash and PowerShell hosts.
var shellAliases = map[string]string{"bash": "pwsh", "pwsh": "bash"}

// Missing stands in for a recorded preset whose file was removed. The session
// keeps its id, so it is not adopted by another preset, but runs the default
// agent until the file returns.
func Missing(id string) Preset {
	return Preset{ID: id, Name: id, Description: "definition not found; running the default agent", KeepCoding: true, IncludeRuntimeContext: true, Scope: ScopeBuiltin}
}

// Unrestricted reports whether the preset keeps every tool.
func (p Preset) Unrestricted() bool { return p.Tools == nil }

// AdmitsMCPServer reports whether a whole MCP server may run in this session.
func (p Preset) AdmitsMCPServer(server string) bool {
	if p.Unrestricted() {
		return true
	}
	server = strings.TrimSpace(server)
	for _, entry := range p.Tools {
		if entry == "mcp:*" || entry == "mcp:"+server {
			return true
		}
	}
	return false
}

// ToolAdmission returns a registry filter, or nil when unrestricted. mcpPrefix
// maps a server name to its registered tool-name prefix.
func (p Preset) ToolAdmission(mcpPrefix func(server string) string) func(string) bool {
	if p.Unrestricted() {
		return nil
	}
	exact := map[string]bool{}
	var patterns, mcpPrefixes []string
	allMCP := false
	for _, entry := range p.Tools {
		switch {
		case entry == "mcp:*":
			allMCP = true
		case strings.HasPrefix(entry, "mcp:"):
			mcpPrefixes = append(mcpPrefixes, mcpPrefix(strings.TrimPrefix(entry, "mcp:")))
		case strings.ContainsAny(entry, "*?["):
			patterns = append(patterns, entry)
		default:
			exact[entry] = true
			if alias, ok := shellAliases[entry]; ok {
				exact[alias] = true
			}
		}
	}
	return func(name string) bool {
		if exact[name] || alwaysAdmitted[name] {
			return true
		}
		if strings.HasPrefix(name, "mcp__") {
			if allMCP {
				return true
			}
			for _, prefix := range mcpPrefixes {
				if strings.HasPrefix(name, prefix) {
					return true
				}
			}
			return false
		}
		for _, pattern := range patterns {
			if ok, _ := filepath.Match(pattern, name); ok {
				return true
			}
		}
		return false
	}
}

// UnmatchedTools lists plain tool entries that name nothing in registered, so
// a typo surfaces as a notice instead of a silently missing tool.
func (p Preset) UnmatchedTools(registered []string) []string {
	have := make(map[string]bool, len(registered))
	for _, name := range registered {
		have[name] = true
	}
	var out []string
	for _, entry := range p.Tools {
		if strings.HasPrefix(entry, "mcp:") || strings.ContainsAny(entry, "*?[") || have[entry] || have[shellAliases[entry]] {
			continue
		}
		out = append(out, entry)
	}
	return out
}
