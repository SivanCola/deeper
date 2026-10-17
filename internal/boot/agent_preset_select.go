package boot

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"deepzora/internal/agent"
	"deepzora/internal/agentpreset"
	"deepzora/internal/config"
	"deepzora/internal/event"
	"deepzora/internal/plugin"
	"deepzora/internal/tool"
)

// AgentPresetDirs are the preset roots for a workspace.
func AgentPresetDirs(root string) agentpreset.Dirs {
	dirs := agentpreset.Dirs{Project: filepath.Join(root, ".deepzora", "presets")}
	if home := config.DeepzoraHomeDir(); home != "" {
		dirs.User = filepath.Join(home, "presets")
	}
	return dirs
}

// resolveAgentPreset picks this build's preset. A bound session's record wins
// over the caller's request, so a model or settings rebuild cannot change what
// a session runs. A session whose preset file was removed keeps its id and
// runs the default agent; an explicit request for an unknown id is an error.
func resolveAgentPreset(root string, opts Options, sink event.Sink) (agentpreset.Preset, error) {
	id, recorded := agentpreset.NormalizeID(opts.AgentPreset), false
	if opts.SessionRuntime != nil {
		projection := opts.SessionRuntime.Session().ExecutionSnapshot().Projection
		if projection.HasTurns() || projection.AgentPreset != "" {
			id, recorded = agentpreset.NormalizeID(projection.AgentPreset), true
		}
	}
	preset, err := agentpreset.Resolve(AgentPresetDirs(root), id)
	switch {
	case err == nil:
		return preset, nil
	case recorded && errors.Is(err, agentpreset.ErrNotFound):
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: fmt.Sprintf("agent preset %q is no longer defined; this session runs the default agent until it is restored", id)})
		return agentpreset.Missing(id), nil
	default:
		return agentpreset.Preset{}, err
	}
}

func agentPresetFingerprint(p agentpreset.Preset) string {
	return p.ID + "\x00" + agentpreset.Render(p)
}

// presetID is the form recorded on controllers and sessions: empty = default.
func presetID(p agentpreset.Preset) string {
	if p.IsDefault() {
		return ""
	}
	return p.ID
}

// applyAgentPresetTools installs the preset's admission on the executor
// registry and MCP runtime. It runs before tools are registered so every later
// Add, including MCP hot-connects, passes through the same filter.
func applyAgentPresetTools(p agentpreset.Preset, reg *tool.Registry) {
	reg.SetAdmission(p.ToolAdmission(plugin.ToolPrefix))
}

func applyAgentPresetMCP(p agentpreset.Preset, runtime *agent.MCPCapabilityRuntime) {
	if p.Unrestricted() {
		return
	}
	runtime.SetServerAdmission(p.AdmitsMCPServer)
}

func reportUnmatchedPresetTools(p agentpreset.Preset, reg *tool.Registry, sink event.Sink) {
	if missing := p.UnmatchedTools(reg.AllNames()); len(missing) > 0 {
		sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: fmt.Sprintf("agent preset %q lists unavailable tools: %s", p.ID, strings.Join(missing, ", "))})
	}
}
