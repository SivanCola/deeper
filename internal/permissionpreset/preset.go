// Package permissionpreset owns the three user-visible execution permission
// presets. Legacy approval-mode strings are accepted only at compatibility
// boundaries and are normalized before they reach the runtime.
package permissionpreset

import "strings"

type Preset string

const (
	ReadOnly         Preset = "read-only"
	WorkspaceWrite   Preset = "workspace-write"
	DangerFullAccess Preset = "danger-full-access"
)

// Normalize resolves current values conservatively; invalid values are read-only.
func Normalize(value string) Preset {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ReadOnly):
		return ReadOnly
	case string(WorkspaceWrite):
		return WorkspaceWrite
	case string(DangerFullAccess):
		return DangerFullAccess
	default:
		return ReadOnly
	}
}

// NormalizeDefault applies the zero-configuration default for newly-created
// sessions while keeping Normalize's conservative behavior for restored data.
func NormalizeDefault(value string) Preset {
	if strings.TrimSpace(value) == "" {
		return WorkspaceWrite
	}
	return Normalize(value)
}

func Valid(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ReadOnly), string(WorkspaceWrite), string(DangerFullAccess):
		return true
	default:
		return false
	}
}
