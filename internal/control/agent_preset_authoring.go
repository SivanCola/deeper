package control

import (
	"errors"
	"fmt"
	"path/filepath"

	"deepzora/internal/agentpreset"
	"deepzora/internal/config"
)

// AgentPresetList includes definitions and diagnostics from the same workspace
// roots used by boot. Current is always a public id, including "default".
type AgentPresetList struct {
	Presets  []agentpreset.Preset `json:"presets"`
	Problems []string             `json:"problems"`
	Current  string               `json:"current"`
	Locked   bool                 `json:"locked"`
}

var _ AgentPresets = (*Controller)(nil)

func agentPresetDirs(root string) agentpreset.Dirs {
	dirs := agentpreset.Dirs{}
	if root != "" {
		dirs.Project = filepath.Join(root, ".deepzora", "presets")
	}
	if home := config.DeepzoraHomeDir(); home != "" {
		dirs.User = filepath.Join(home, "presets")
	}
	return dirs
}

// ListAgentPresetsForRoot supports authoring before a model is configured.
// Without a workspace root only user definitions are included.
func ListAgentPresetsForRoot(root string) (AgentPresetList, error) {
	presets, problems := agentpreset.List(agentPresetDirs(root))
	out := AgentPresetList{Presets: presets, Problems: []string{}, Current: agentpreset.DefaultID}
	for _, problem := range problems {
		out.Problems = append(out.Problems, problem.Error())
	}
	return out, nil
}

// ListAgentPresets reports the active selection and its authoritative lock.
func (c *Controller) ListAgentPresets() (AgentPresetList, error) {
	if c == nil {
		return AgentPresetList{}, errors.New("agent preset controller is unavailable")
	}
	out, err := ListAgentPresetsForRoot(c.WorkspaceRoot())
	out.Current, out.Locked = agentpreset.NormalizeID(c.AgentPreset()), c.AgentPresetLocked()
	return out, err
}

func agentPresetAuthoringDir(root, scope string) (string, agentpreset.Scope, error) {
	dirs := agentPresetDirs(root)
	switch agentpreset.Scope(scope) {
	case agentpreset.ScopeUser:
		return dirs.User, agentpreset.ScopeUser, nil
	case agentpreset.ScopeProject:
		return dirs.Project, agentpreset.ScopeProject, nil
	default:
		return "", "", fmt.Errorf("invalid agent preset scope %q: use user or project", scope)
	}
}

// SaveAgentPresetForRoot validates and atomically saves a definition without
// requiring a configured model or a session runtime.
func SaveAgentPresetForRoot(root, scope, id, content string) (agentpreset.Preset, error) {
	dir, resolvedScope, err := agentPresetAuthoringDir(root, scope)
	if err != nil {
		return agentpreset.Preset{}, err
	}
	preset, err := agentpreset.Write(dir, id, content)
	if err != nil {
		return agentpreset.Preset{}, err
	}
	preset.Scope = resolvedScope
	return preset, nil
}

// SaveAgentPreset leaves assembled prompt and tools fixed until a host rebuild.
func (c *Controller) SaveAgentPreset(scope, id, content string) (agentpreset.Preset, error) {
	if c == nil {
		return agentpreset.Preset{}, errors.New("agent preset controller is unavailable")
	}
	return SaveAgentPresetForRoot(c.WorkspaceRoot(), scope, id, content)
}

// DeleteAgentPresetForRoot removes only the named scope's definition.
func DeleteAgentPresetForRoot(root, scope, id string) error {
	dir, _, err := agentPresetAuthoringDir(root, scope)
	if err != nil {
		return err
	}
	return agentpreset.Remove(dir, id)
}

// DeleteAgentPreset preserves recorded session identities after file deletion.
func (c *Controller) DeleteAgentPreset(scope, id string) error {
	if c == nil {
		return errors.New("agent preset controller is unavailable")
	}
	return DeleteAgentPresetForRoot(c.WorkspaceRoot(), scope, id)
}
