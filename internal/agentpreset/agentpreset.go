package agentpreset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"deepzora/internal/fileutil"
	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/frontmatter"
)

// DefaultID names the unmodified agent. It is never persisted to a session.
const DefaultID = "default"

// Scope says where a preset was loaded from.
type Scope string

const (
	ScopeBuiltin Scope = "builtin"
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// Preset is one resolved definition. Tools nil means unrestricted.
type Preset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	KeepCoding  bool     `json:"keepCoding"`
	Tools       []string `json:"tools"`
	Scope       Scope    `json:"scope"`
	Path        string   `json:"path"`
}

// Dirs are the two preset roots. Either may be empty.
type Dirs struct {
	User    string
	Project string
}

// ErrNotFound reports an id with no definition in any root.
var ErrNotFound = errors.New("agent preset not found")

// ErrBuiltin refuses a write or removal of the built-in preset.
var ErrBuiltin = errors.New("the built-in default preset is read-only; copy it to a new id instead")

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidID checks the id before it becomes a file name, so no path is ever
// assembled from untrusted input.
func ValidID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid preset id %q: use 1-64 lowercase letters, digits or '-', starting with a letter or digit", id)
	}
	return nil
}

// NormalizeID maps the empty selection to the default preset.
func NormalizeID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return DefaultID
	}
	return id
}

// Default returns the built-in unmodified preset.
func Default() Preset {
	return Preset{ID: DefaultID, Name: "Default", Description: "The standard Deepzora agent with every tool", KeepCoding: true, Scope: ScopeBuiltin}
}

// IsDefault reports whether p leaves the agent unmodified.
func (p Preset) IsDefault() bool { return p.ID == DefaultID }

// List returns the built-in preset followed by every valid file preset, sorted
// by id. Malformed files are reported in problems rather than hidden, so a
// picker can explain why a preset is missing.
func List(dirs Dirs) (presets []Preset, problems []error) {
	byID := map[string]Preset{}
	for _, root := range []struct {
		dir   string
		scope Scope
	}{{dirs.User, ScopeUser}, {dirs.Project, ScopeProject}} {
		if root.dir == "" {
			continue
		}
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				problems = append(problems, err)
			}
			continue
		}
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".md")
			if !ok {
				continue
			}
			p, err := load(root.dir, id, root.scope)
			if err != nil {
				// An invalid project override must not advertise a user definition
				// that Resolve would refuse to load.
				delete(byID, id)
				problems = append(problems, err)
				continue
			}
			byID[id] = p
		}
	}
	out := make([]Preset, 0, len(byID)+1)
	for _, p := range byID {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return append([]Preset{Default()}, out...), problems
}

// Resolve finds id, preferring the project root over the user root.
func Resolve(dirs Dirs, id string) (Preset, error) {
	id = NormalizeID(id)
	if id == DefaultID {
		return Default(), nil
	}
	if err := ValidID(id); err != nil {
		return Preset{}, err
	}
	for _, root := range []struct {
		dir   string
		scope Scope
	}{{dirs.Project, ScopeProject}, {dirs.User, ScopeUser}} {
		if root.dir == "" {
			continue
		}
		p, err := load(root.dir, id, root.scope)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return p, err
	}
	return Preset{}, fmt.Errorf("%w: %q", ErrNotFound, id)
}

func load(dir, id string, scope Scope) (Preset, error) {
	if err := ValidID(id); err != nil || id == DefaultID {
		return Preset{}, fmt.Errorf("%s: %w", filepath.Join(dir, id+".md"), errors.Join(err, reservedErr(id)))
	}
	path := filepath.Join(dir, id+".md")
	data, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return Preset{}, err
	}
	p, err := Parse(id, string(data))
	if err != nil {
		return Preset{}, fmt.Errorf("%s: %w", path, err)
	}
	p.Scope, p.Path = scope, path
	return p, nil
}

func reservedErr(id string) error {
	if id == DefaultID {
		return ErrBuiltin
	}
	return nil
}

type fileMeta struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	KeepCoding  *bool     `yaml:"keep-coding-instructions"`
	Tools       *[]string `yaml:"tools"`
}

// Parse decodes a preset file body. Unknown keys are errors so a typo such as
// `tool:` cannot silently grant every tool.
func Parse(id, text string) (Preset, error) {
	var meta fileMeta
	body, err := frontmatter.Decode(text, &meta, frontmatter.DecodeOptions{KnownFields: true})
	if err != nil {
		return Preset{}, fmt.Errorf("frontmatter: %w", err)
	}
	p := Preset{ID: id, Name: strings.TrimSpace(meta.Name), Description: strings.TrimSpace(meta.Description), Body: strings.TrimSpace(body), KeepCoding: true}
	if p.Name == "" {
		p.Name = id
	}
	if meta.KeepCoding != nil {
		p.KeepCoding = *meta.KeepCoding
	}
	if !p.KeepCoding && p.Body == "" {
		return Preset{}, errors.New("keep-coding-instructions: false needs a body to replace the prompt with")
	}
	if meta.Tools != nil {
		tools, err := normalizeTools(*meta.Tools)
		if err != nil {
			return Preset{}, err
		}
		p.Tools = tools
	}
	return p, nil
}

func normalizeTools(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item)
		switch {
		case name == "":
			continue
		case strings.HasPrefix(name, "mcp__"), strings.HasPrefix(name, "mcp-tool:"):
			return nil, fmt.Errorf("tools: %q names one MCP tool; presets admit whole servers only (use mcp:<server>)", name)
		case strings.HasPrefix(name, "mcp:"):
			if strings.TrimSpace(strings.TrimPrefix(name, "mcp:")) == "" {
				return nil, fmt.Errorf("tools: %q is missing a server name", name)
			}
		case strings.ContainsAny(name, "*?["):
			if _, err := filepath.Match(name, ""); err != nil {
				return nil, fmt.Errorf("tools: bad pattern %q: %w", name, err)
			}
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

// Render serializes a preset back to its file form for authoring surfaces.
func Render(p Preset) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", yamlScalar(p.Name))
	if p.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", yamlScalar(p.Description))
	}
	if !p.KeepCoding {
		b.WriteString("keep-coding-instructions: false\n")
	}
	if p.Tools != nil {
		quoted := make([]string, len(p.Tools))
		for i, t := range p.Tools {
			quoted[i] = yamlScalar(t)
		}
		fmt.Fprintf(&b, "tools: [%s]\n", strings.Join(quoted, ", "))
	}
	b.WriteString("---\n")
	if p.Body != "" {
		b.WriteString(p.Body)
		b.WriteString("\n")
	}
	return b.String()
}

func yamlScalar(s string) string { return fmt.Sprintf("%q", s) }

// Write validates text by parsing it under id, then publishes it atomically to
// dir. A file that would not load is never saved.
func Write(dir, id, text string) (Preset, error) {
	id = NormalizeID(id)
	if id == DefaultID {
		return Preset{}, ErrBuiltin
	}
	if err := ValidID(id); err != nil {
		return Preset{}, err
	}
	if strings.TrimSpace(dir) == "" {
		return Preset{}, errors.New("preset directory is unavailable")
	}
	p, err := Parse(id, text)
	if err != nil {
		return Preset{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Preset{}, err
	}
	path := filepath.Join(dir, id+".md")
	if err := fileutil.AtomicWriteFile(path, []byte(text), 0o644); err != nil {
		return Preset{}, err
	}
	p.Path = path
	return p, nil
}

// Remove deletes id from dir.
func Remove(dir, id string) error {
	id = NormalizeID(id)
	if id == DefaultID {
		return ErrBuiltin
	}
	if err := ValidID(id); err != nil {
		return err
	}
	if strings.TrimSpace(dir) == "" {
		return errors.New("preset directory is unavailable")
	}
	err := os.Remove(filepath.Join(dir, id+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return err
}

// Apply folds the preset body into a base system prompt.
func (p Preset) Apply(base string) string {
	if p.Body == "" {
		return base
	}
	if !p.KeepCoding || strings.TrimSpace(base) == "" {
		return p.Body
	}
	return base + "\n\n" + p.Body
}
