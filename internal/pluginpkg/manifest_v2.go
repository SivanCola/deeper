package pluginpkg

import (
	"encoding/json"
	"fmt"
	"strings"

	"deepzora/internal/extensioncontract"
	fileencoding "deepzora/internal/fileutil/encoding"
)

// ManifestAPIVersionV2 is the only native plugin manifest apiVersion Deepzora
// accepts for install, doctor, and boot. v1 and legacy (no apiVersion) native
// manifests are rejected by the parser. Legacy pre-extension manifests can be
// upgraded explicitly, or automatically when they live in Deepzora's managed
// plugin directory where an atomic backup is safe.
const ManifestAPIVersionV2 = "deepzora.com/plugin/v2"

// CapabilityRef is the wire form of a capability in a v2 manifest.
type CapabilityRef struct {
	Namespace    string `json:"namespace"`
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Version      string `json:"version,omitempty"`
	VersionRange string `json:"versionRange,omitempty"`
	SchemaHash   string `json:"schemaHash,omitempty"`
	Optional     bool   `json:"optional,omitempty"`
}

// ToCapability converts a provides entry.
func (c CapabilityRef) ToCapability() extensioncontract.Capability {
	return extensioncontract.Capability{
		Key: extensioncontract.CapabilityKey{
			Namespace: strings.TrimSpace(c.Namespace),
			Kind:      strings.TrimSpace(c.Kind),
			ID:        strings.TrimSpace(c.ID),
		},
		Version:    strings.TrimSpace(c.Version),
		SchemaHash: strings.TrimSpace(c.SchemaHash),
	}
}

// ToRequirement converts a requires entry.
func (c CapabilityRef) ToRequirement() extensioncontract.Requirement {
	return extensioncontract.Requirement{
		Capability: extensioncontract.Capability{
			Key: extensioncontract.CapabilityKey{
				Namespace: strings.TrimSpace(c.Namespace),
				Kind:      strings.TrimSpace(c.Kind),
				ID:        strings.TrimSpace(c.ID),
			},
			Version:    strings.TrimSpace(c.Version),
			SchemaHash: strings.TrimSpace(c.SchemaHash),
		},
		VersionRange: strings.TrimSpace(c.VersionRange),
		Optional:     c.Optional,
	}
}

// checkAPIVersionV2 accepts the current namespace and the exact pre-rename v2
// identity. Schema versions and path/capability validation remain unchanged.
func checkAPIVersionV2(v string) error {
	if v == ManifestAPIVersionV2 {
		return nil
	}
	return fmt.Errorf("%s: unsupported apiVersion %q: want exact %s", NativeManifest, v, ManifestAPIVersionV2)
}

// rejectNonV2Native explains why a non-v2 native manifest is refused.
func rejectNonV2Native(apiVersion string) error {
	if apiVersion == "" {
		return fmt.Errorf("%s: missing apiVersion; native manifests must declare %s", NativeManifest, ManifestAPIVersionV2)
	}
	return checkAPIVersionV2(apiVersion)
}

type v2Root struct {
	APIVersion  string          `json:"apiVersion"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Homepage    string          `json:"homepage"`
	Repository  string          `json:"repository"`
	Requires    json.RawMessage `json:"requires"`
	Provides    json.RawMessage `json:"provides"`
	Contributes json.RawMessage `json:"contributes"`
	Runtime     json.RawMessage `json:"runtime"`
	// Resource path fields (same shapes as earlier native manifests).

}

func parseNativeV2(b []byte, root, apiVersion string) (Package, []string, error) {
	if err := checkAPIVersionV2(apiVersion); err != nil {
		return Package{}, nil, err
	}
	var raw v2Root
	if err := strictDecode(b, &raw, NativeManifest); err != nil {
		return Package{}, nil, err
	}
	var contrib nativeContributes
	if len(raw.Contributes) > 0 && string(raw.Contributes) != "null" {
		if err := strictDecode(raw.Contributes, &contrib, "contributes"); err != nil {
			return Package{}, nil, err
		}
	}

	contribSkills, err := parseNativePathList(contrib.Skills, "contributes.skills")
	if err != nil {
		return Package{}, nil, err
	}
	contribAgents, err := parseNativePathList(contrib.Agents, "contributes.agents")
	if err != nil {
		return Package{}, nil, err
	}
	contribCommands, err := parseNativePathList(contrib.Commands, "contributes.commands")
	if err != nil {
		return Package{}, nil, err
	}
	contribPrompts, err := parseNativePathList(contrib.Prompts, "contributes.prompts")
	if err != nil {
		return Package{}, nil, err
	}
	contribThemes, err := parseNativePathList(contrib.Themes, "contributes.themes")
	if err != nil {
		return Package{}, nil, err
	}
	contribHooks, err := parseNativeHookMap(contrib.Hooks, "contributes.hooks")
	if err != nil {
		return Package{}, nil, err
	}
	contribMCP, err := parseNativeMCPServerMap(contrib.MCPServers, "contributes.mcpServers")
	if err != nil {
		return Package{}, nil, err
	}
	runtime, err := parseNativeRuntime(raw.Runtime)
	if err != nil {
		return Package{}, nil, err
	}
	requires, err := parseCapabilityRefs(raw.Requires, "requires", true)
	if err != nil {
		return Package{}, nil, err
	}
	provides, err := parseCapabilityRefs(raw.Provides, "provides", false)
	if err != nil {
		return Package{}, nil, err
	}

	manifest := Manifest{
		APIVersion:  ManifestAPIVersionV2,
		Name:        strings.TrimSpace(raw.Name),
		Version:     strings.TrimSpace(raw.Version),
		Description: strings.TrimSpace(raw.Description),
		Homepage:    strings.TrimSpace(raw.Homepage),
		Repository:  strings.TrimSpace(raw.Repository),
		Skills:      contribSkills,
		Commands:    contribCommands,
		Agents:      contribAgents,
		Prompts:     contribPrompts,
		Themes:      contribThemes,
		Hooks:       normalizeHooks(contribHooks),
		MCPServers:  contribMCP,
		Runtime:     runtime,
		Requires:    requires,
		Provides:    provides,
	}
	if err := validateManifest(root, &manifest); err != nil {
		return Package{}, nil, err
	}
	if err := validateV2Capabilities(&manifest); err != nil {
		return Package{}, nil, err
	}
	var warnings []string
	pathWarnings, err := validateV1Paths(root, &manifest)
	warnings = append(warnings, pathWarnings...)
	if err != nil {
		return Package{}, warnings, err
	}
	pkg := Package{Root: root, ManifestKind: "deepzora", Manifest: manifest}
	pkg.Compatibility = compatibilityFor(pkg, nil)
	return pkg, warnings, nil
}

func parseCapabilityRefs(raw json.RawMessage, path string, asRequirement bool) ([]CapabilityRef, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	out := make([]CapabilityRef, 0, len(items))
	for i, item := range items {
		var ref CapabilityRef
		if err := strictDecode(item, &ref, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return nil, err
		}
		ref.Namespace = strings.TrimSpace(ref.Namespace)
		ref.Kind = strings.TrimSpace(ref.Kind)
		ref.ID = strings.TrimSpace(ref.ID)
		ref.Version = strings.TrimSpace(ref.Version)
		ref.VersionRange = strings.TrimSpace(ref.VersionRange)
		ref.SchemaHash = strings.TrimSpace(ref.SchemaHash)
		if asRequirement {
			if err := ref.ToRequirement().Validate(); err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", path, i, err)
			}
		} else {
			if err := ref.ToCapability().Validate(); err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", path, i, err)
			}
		}
		out = append(out, ref)
	}
	return out, nil
}

func validateV2Capabilities(m *Manifest) error {
	seen := map[string]bool{}
	for i, p := range m.Provides {
		key := p.ToCapability().Key.String()
		if seen[key] {
			return fmt.Errorf("provides[%d]: duplicate capability %s", i, key)
		}
		seen[key] = true
	}
	return nil
}

// ComponentDescriptorFields projects a package into kernel component fields.
func (p Package) Requires() []extensioncontract.Requirement {
	out := make([]extensioncontract.Requirement, 0, len(p.Manifest.Requires))
	for _, r := range p.Manifest.Requires {
		out = append(out, r.ToRequirement())
	}
	return out
}

// ProvidesCapabilities returns the manifest capability ceiling.
func (p Package) ProvidesCapabilities() []extensioncontract.Capability {
	out := make([]extensioncontract.Capability, 0, len(p.Manifest.Provides))
	for _, c := range p.Manifest.Provides {
		out = append(out, c.ToCapability())
	}
	return out
}

func parseNative(path, root string) (Package, []string, error) {
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return Package{}, nil, err
	}
	apiVersion, err := sniffManifestAPIVersion(b)
	if err != nil {
		return Package{}, nil, err
	}
	if apiVersion == "" {
		return Package{}, nil, rejectNonV2Native(apiVersion)
	}
	if err := checkAPIVersionV2(apiVersion); err != nil {
		return Package{}, nil, err
	}
	return parseNativeV2(b, root, apiVersion)
}
