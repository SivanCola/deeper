package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"deepzora/internal/fileutil"
	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/mcpdiag"
)

// mcpJSONFile is the project-root file Claude Code calls .mcp.json. Deepzora reads
// it so an MCP server already configured for Claude works here unchanged — the
// server specs map field-for-field onto PluginEntry.
const mcpJSONFile = ".mcp.json"

// mcpServerSpec mirrors one entry of Claude Code's "mcpServers" map. The field
// names and semantics match PluginEntry: command/args/env describe a local
// stdio server; type/url/headers describe a remote one. Deepzora also accepts
// startup and call timeout fields as Deepzora policy extensions.
type mcpServerSpec struct {
	Type                  string            `json:"type"`
	Command               string            `json:"command"`
	Args                  []string          `json:"args"`
	Env                   map[string]string `json:"env"`
	URL                   string            `json:"url"`
	Headers               map[string]string `json:"headers"`
	StartupTimeoutSeconds int               `json:"startup_timeout_seconds"`
	CallTimeoutSeconds    int               `json:"call_timeout_seconds"`
	ToolTimeoutSeconds    map[string]int    `json:"tool_timeout_seconds"`
	AutoStart             *bool             `json:"auto_start"`
}

// loadMCPJSON reads path (Claude Code's .mcp.json) and returns its servers as
// PluginEntry values, sorted by name for a stable connection order. An absent
// file is not an error (returns nil, nil). A present-but-malformed file is an
// error so a typo surfaces loudly instead of silently dropping every server.
func loadMCPJSON(path string) ([]PluginEntry, error) {
	resolved, err := resolveConfigAccessPath(path, false)
	if err != nil {
		return nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	b, err := fileencoding.ReadFileUTF8(resolved)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	var doc struct {
		MCPServers map[string]mcpServerSpec `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	return specsToEntries(doc.MCPServers, nil), nil
}

// LoadMCPJSONPlugin returns one server entry from a Claude-compatible .mcp.json.
func LoadMCPJSONPlugin(path, name string) (PluginEntry, bool, error) {
	entries, err := loadMCPJSON(path)
	if err != nil {
		return PluginEntry{}, false, err
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return PluginEntry{}, false, nil
}

// specsToEntries converts an mcpServers map to PluginEntry values in stable name order.
func specsToEntries(specs map[string]mcpServerSpec, skip map[string]bool) []PluginEntry {
	names := make([]string, 0, len(specs))
	for name := range specs {
		if !skip[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	entries := make([]PluginEntry, 0, len(names))
	for _, name := range names {
		entry := pluginEntryFromMCPSpec(name, specs[name])
		entry.Source = MCPSourceProjectMCPJSON
		entries = append(entries, entry)
	}
	return entries
}

func pluginEntryFromMCPSpec(name string, s mcpServerSpec) PluginEntry {
	e := PluginEntry{
		Name:                  name,
		Type:                  s.Type,
		Command:               s.Command,
		Args:                  s.Args,
		Env:                   s.Env,
		URL:                   s.URL,
		Headers:               s.Headers,
		StartupTimeoutSeconds: s.StartupTimeoutSeconds,
		CallTimeoutSeconds:    s.CallTimeoutSeconds,
		ToolTimeoutSeconds:    s.ToolTimeoutSeconds,
		AutoStart:             s.AutoStart,
	}
	e, _ = NormalizePluginCommandLine(e)
	return e
}

// mergeMCPJSON appends servers from .mcp.json that the TOML config did not
// already declare. deepzora.toml's [[plugins]] win on a name collision: it is the
// Deepzora-specific, more explicit of the two, so it overrides the shared,
// checked-in .mcp.json rather than the other way round.
func (c *Config) mergeMCPJSON(entries []PluginEntry) {
	index := make(map[string]int, len(c.Plugins))
	for i, p := range c.Plugins {
		index[p.Name] = i
	}
	for _, e := range entries {
		if i, exists := index[e.Name]; exists {
			// Project configuration always wins over user-global configuration.
			// Within one project, deepzora.toml remains more specific than the
			// Claude-compatible .mcp.json file.
			if e.Source == MCPSourceProjectMCPJSON && !c.Plugins[i].Source.ProjectScoped() {
				c.Plugins[i] = e
			}
			continue
		}
		index[e.Name] = len(c.Plugins)
		c.Plugins = append(c.Plugins, e)
	}
}

// UpsertMCPJSONPlugin writes one MCP server to a Claude-compatible .mcp.json
// file, preserving unrelated top-level fields and unknown per-server fields.
func UpsertMCPJSONPlugin(path string, entry PluginEntry) (bool, error) {
	entry, _ = NormalizePluginCommandLine(entry)
	if err := validatePlugin(entry); err != nil {
		return false, err
	}
	root, servers, err := readMCPJSONRaw(path)
	if err != nil {
		return false, err
	}
	raw, existed := servers[entry.Name]
	server := map[string]json.RawMessage{}
	if existed && len(raw) > 0 {
		if err := json.Unmarshal(raw, &server); err != nil || server == nil {
			return false, fmt.Errorf("mcp config %s: server %q is not an object", path, entry.Name)
		}
	}
	if err := applyPluginEntryToMCPJSONServer(server, entry); err != nil {
		return false, fmt.Errorf("mcp config %s: server %q: %w", path, entry.Name, err)
	}
	updatedRaw, err := json.Marshal(server)
	if err != nil {
		return false, fmt.Errorf("mcp config %s: server %q: %w", path, entry.Name, err)
	}
	servers[entry.Name] = updatedRaw
	if err := writeMCPJSONServers(path, root, servers); err != nil {
		return false, err
	}
	return !existed, nil
}

// RemoveMCPJSONPlugin removes one MCP server from a Claude-compatible .mcp.json
// file. Missing files or missing entries are reported as unchanged.
func RemoveMCPJSONPlugin(path, name string) (bool, error) {
	root, servers, err := readMCPJSONRaw(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, ok := servers[name]; !ok {
		return false, nil
	}
	delete(servers, name)
	if err := writeMCPJSONServers(path, root, servers); err != nil {
		return false, err
	}
	return true, nil
}

func readMCPJSONRaw(path string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}
	resolved, err := resolveConfigAccessPath(path, false)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	b, err := fileencoding.ReadFileUTF8(resolved)
	if os.IsNotExist(err) {
		return root, servers, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, nil, fmt.Errorf("mcp config %s: %w", path, err)
	}
	raw, ok := root["mcpServers"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return root, servers, nil
	}
	if err := json.Unmarshal(raw, &servers); err != nil || servers == nil {
		return nil, nil, fmt.Errorf("mcp config %s: mcpServers must be an object", path)
	}
	return root, servers, nil
}

func applyPluginEntryToMCPJSONServer(server map[string]json.RawMessage, entry PluginEntry) error {
	transport := strings.ToLower(strings.TrimSpace(entry.Type))
	if transport == "" {
		transport = "stdio"
	}
	if transport == "stdio" {
		delete(server, "type")
		setMCPJSONString(server, "command", strings.TrimSpace(entry.Command))
		setMCPJSONStringArray(server, "args", entry.Args)
		setMCPJSONStringMap(server, "env", entry.Env)
		delete(server, "url")
		delete(server, "headers")
	} else {
		setMCPJSONString(server, "type", transport)
		setMCPJSONString(server, "url", strings.TrimSpace(entry.URL))
		setMCPJSONStringMap(server, "headers", entry.Headers)
		setMCPJSONStringMap(server, "env", entry.Env)
		delete(server, "command")
		delete(server, "args")
	}
	setMCPJSONInt(server, "startup_timeout_seconds", entry.StartupTimeoutSeconds)
	setMCPJSONInt(server, "call_timeout_seconds", entry.CallTimeoutSeconds)
	setMCPJSONIntMap(server, "tool_timeout_seconds", entry.ToolTimeoutSeconds)
	setMCPJSONBool(server, "auto_start", entry.AutoStart)
	return nil
}

func writeMCPJSONServers(path string, root map[string]json.RawMessage, servers map[string]json.RawMessage) error {
	serversRaw, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("mcp config %s: %w", path, err)
	}
	root["mcpServers"] = serversRaw
	return writeMCPJSON(path, root)
}

func clearMCPJSONAuthentication(path, name string) (PluginEntry, bool, error) {
	root, servers, err := readMCPJSONRaw(path)
	if err != nil {
		return PluginEntry{}, false, err
	}
	raw, ok := servers[name]
	if !ok {
		return PluginEntry{}, false, fmt.Errorf("clear plugin authentication: no plugin %q", name)
	}
	var spec mcpServerSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return PluginEntry{}, false, fmt.Errorf("mcp config %s: server %q: %w", path, name, err)
	}
	cleanHeaders, cleanEnv, cleanURL, changed := mcpdiag.ClearAuthConfig(spec.Headers, spec.Env, spec.URL)
	if !changed {
		return pluginEntryFromMCPSpec(name, spec), false, nil
	}
	spec.Headers = cleanHeaders
	spec.Env = cleanEnv
	spec.URL = cleanURL

	var server map[string]json.RawMessage
	if err := json.Unmarshal(raw, &server); err != nil || server == nil {
		return PluginEntry{}, false, fmt.Errorf("mcp config %s: server %q is not an object", path, name)
	}
	setMCPJSONStringMap(server, "headers", cleanHeaders)
	setMCPJSONStringMap(server, "env", cleanEnv)
	setMCPJSONString(server, "url", cleanURL)
	updatedRaw, err := json.Marshal(server)
	if err != nil {
		return PluginEntry{}, false, fmt.Errorf("mcp config %s: server %q: %w", path, name, err)
	}
	servers[name] = updatedRaw
	serversRaw, err := json.Marshal(servers)
	if err != nil {
		return PluginEntry{}, false, fmt.Errorf("mcp config %s: %w", path, err)
	}
	root["mcpServers"] = serversRaw
	if err := writeMCPJSON(path, root); err != nil {
		return PluginEntry{}, false, err
	}
	return pluginEntryFromMCPSpec(name, spec), true, nil
}

func setMCPJSONStringMap(server map[string]json.RawMessage, key string, values map[string]string) {
	if len(values) == 0 {
		delete(server, key)
		return
	}
	raw, err := json.Marshal(values)
	if err != nil {
		delete(server, key)
		return
	}
	server[key] = raw
}

func setMCPJSONString(server map[string]json.RawMessage, key, value string) {
	if value == "" {
		delete(server, key)
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		delete(server, key)
		return
	}
	server[key] = raw
}

func setMCPJSONStringArray(server map[string]json.RawMessage, key string, values []string) {
	if len(values) == 0 {
		delete(server, key)
		return
	}
	raw, err := json.Marshal(values)
	if err != nil {
		delete(server, key)
		return
	}
	server[key] = raw
}

func setMCPJSONInt(server map[string]json.RawMessage, key string, value int) {
	if value <= 0 {
		delete(server, key)
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		delete(server, key)
		return
	}
	server[key] = raw
}

func setMCPJSONIntMap(server map[string]json.RawMessage, key string, values map[string]int) {
	clean := make(map[string]int, len(values))
	for k, v := range values {
		if strings.TrimSpace(k) == "" || v <= 0 {
			continue
		}
		clean[k] = v
	}
	if len(clean) == 0 {
		delete(server, key)
		return
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		delete(server, key)
		return
	}
	server[key] = raw
}

func setMCPJSONBool(server map[string]json.RawMessage, key string, value *bool) {
	if value == nil {
		delete(server, key)
		return
	}
	raw, err := json.Marshal(*value)
	if err != nil {
		delete(server, key)
		return
	}
	server[key] = raw
}

func writeMCPJSON(path string, root map[string]json.RawMessage) error {
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp config %s: %w", path, err)
	}
	out = append(out, '\n')
	resolved, err := resolveConfigAccessPath(path, false)
	if err != nil {
		return fmt.Errorf("mcp config %s: %w", path, err)
	}
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mcp config %s: create dir: %w", path, err)
	}
	tmp, err := os.CreateTemp(dir, ".mcp.*.json.tmp")
	if err != nil {
		return fmt.Errorf("mcp config %s: create temp: %w", path, err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("mcp config %s: write: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("mcp config %s: close temp: %w", path, err)
	}
	if err := fileutil.ReplaceFile(tmpPath, resolved); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
