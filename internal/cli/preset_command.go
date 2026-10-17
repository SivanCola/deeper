package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deepzora/internal/agentpreset"
	"deepzora/internal/boot"
	"deepzora/internal/control"
	fileencoding "deepzora/internal/fileutil/encoding"
	"deepzora/internal/i18n"

	"github.com/spf13/pflag"
)

const presetUsageText = `usage:
  deepzora preset list [--scope user|project] [--dir PATH] [--json]
  deepzora preset show <id> [--scope user|project] [--dir PATH] [--json]
  deepzora preset new <id> [--scope user|project] [--prompt TEXT | --prompt-file PATH] [--tools LIST] [--dir PATH]
  deepzora preset edit <id> [--scope user|project] [--prompt TEXT | --prompt-file PATH] [--tools LIST] [--dir PATH]
  deepzora preset copy <source-id> <new-id> [--scope user|project] [--dir PATH]
  deepzora preset rm <id> [--scope user|project] [--dir PATH]

New presets and copies default to user scope. Project definitions override user
ones; show, edit and rm use the visible definition unless --scope is specified.
Use --prompt-file - to read the persona from stdin. --tools takes comma-separated
built-in names/globs and mcp:<server>; all is unrestricted, [] or empty admits no
tools. Omitting --tools preserves the existing selection (unrestricted for new).
Presets select a persona and tools. Model and permission settings stay separate.
`

type presetCommandOptions struct {
	scope, dir, prompt, promptFile, tools, name, description string
	keepCoding, json                                         bool
}

func presetCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, presetUsageText)
		return 2
	}
	verb := args[0]
	if verb == "help" || verb == "--help" || verb == "-h" {
		fmt.Print(presetUsageText)
		return 0
	}
	counts := map[string]int{"list": 0, "show": 1, "new": 1, "edit": 1, "copy": 2, "rm": 1}
	count, known := counts[verb]
	if !known {
		fmt.Fprintf(os.Stderr, "unknown preset command %q\n\n%s", verb, presetUsageText)
		return 2
	}
	var values presetCommandOptions
	fs := pflag.NewFlagSet("preset "+verb, pflag.ContinueOnError)
	fs.StringVar(&values.scope, "scope", "", "preset scope: user or project")
	fs.StringVar(&values.dir, "dir", "", "project root")
	if verb == "list" || verb == "show" {
		fs.BoolVar(&values.json, "json", false, "print JSON")
	}
	if verb == "new" || verb == "edit" || verb == "copy" {
		fs.StringVar(&values.prompt, "prompt", "", "persona prompt text")
		fs.StringVar(&values.promptFile, "prompt-file", "", "read persona prompt from a file (- for stdin)")
		fs.StringVar(&values.tools, "tools", "", "comma-separated tool names/globs or mcp:<server>; all = unrestricted, [] = no tools")
		fs.StringVar(&values.name, "name", "", "display name")
		fs.StringVar(&values.description, "description", "", "short description")
		fs.BoolVar(&values.keepCoding, "keep-coding-instructions", true, "append the persona to the coding prompt; false replaces it")
	}
	if code, ok := parseCommandFlags(fs, args[1:]); !ok {
		return code
	}
	if fs.NArg() != count {
		fmt.Fprint(os.Stderr, presetUsageText)
		return 2
	}
	if values.scope != "" && values.scope != "user" && values.scope != "project" {
		return presetCommandError(errors.New("--scope must be user or project"), 2)
	}
	root, err := presetCommandRoot(values.dir)
	if err != nil {
		return presetCommandError(err, 2)
	}
	if verb == "list" {
		return listPresetCommand(root, values)
	}
	if verb == "show" {
		preset, err := resolveCommandPreset(root, values.scope, fs.Arg(0))
		if err != nil {
			return presetCommandError(err, 1)
		}
		if values.json {
			return presetCommandError(json.NewEncoder(os.Stdout).Encode(preset), 1)
		}
		fmt.Print(agentpreset.Render(preset))
		return 0
	}
	return mutatePresetCommand(root, verb, fs, values)
}

func presetCommandError(err error, code int) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, err)
	return code
}

func presetCommandRoot(dir string) (string, error) {
	if dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return "", fmt.Errorf("preset project root: %w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("preset project root %q is not a directory", dir)
		}
	}
	return boot.ResolveWorkspaceRoot(dir), nil
}

func scopedPresetDirs(root, scope string) agentpreset.Dirs {
	dirs := boot.AgentPresetDirs(root)
	switch scope {
	case "user":
		dirs.Project = ""
	case "project":
		dirs.User = ""
	}
	return dirs
}

func resolveCommandPreset(root, scope, id string) (agentpreset.Preset, error) {
	return agentpreset.Resolve(scopedPresetDirs(root, scope), id)
}

func listPresetCommand(root string, values presetCommandOptions) int {
	list, err := control.ListAgentPresetsForRoot(root)
	if err != nil {
		return presetCommandError(err, 1)
	}
	if values.scope != "" {
		presets, problems := agentpreset.List(scopedPresetDirs(root, values.scope))
		list.Presets, list.Problems = presets, []string{}
		for _, problem := range problems {
			list.Problems = append(list.Problems, problem.Error())
		}
	}
	if values.json {
		return presetCommandError(json.NewEncoder(os.Stdout).Encode(list), 1)
	}
	for _, preset := range list.Presets {
		fmt.Printf("%-24s %-10s %s\n", preset.ID, preset.Scope, preset.Description)
	}
	for _, problem := range list.Problems {
		fmt.Fprintln(os.Stderr, i18n.M.ErrorPrefix, problem)
	}
	return 0
}

func mutatePresetCommand(root, verb string, fs *pflag.FlagSet, values presetCommandOptions) int {
	id := agentpreset.NormalizeID(fs.Arg(0))
	if id == agentpreset.DefaultID && verb != "copy" {
		return presetCommandError(agentpreset.ErrBuiltin, 1)
	}
	if err := agentpreset.ValidID(id); err != nil {
		return presetCommandError(err, 2)
	}
	preset := agentpreset.Preset{ID: id, Name: id, KeepCoding: true}
	scope := values.scope
	if verb == "rm" {
		if scope == "" {
			dirs := boot.AgentPresetDirs(root)
			scope = "user"
			if _, err := os.Lstat(filepath.Join(dirs.Project, id+".md")); err == nil {
				scope = "project"
			} else if !errors.Is(err, os.ErrNotExist) {
				return presetCommandError(err, 1)
			}
		}
		return presetCommandError(control.DeleteAgentPresetForRoot(root, scope, id), 1)
	}
	if verb != "new" {
		var err error
		sourceScope := scope
		if verb == "copy" {
			sourceScope = ""
		}
		preset, err = resolveCommandPreset(root, sourceScope, id)
		if err != nil {
			return presetCommandError(err, 1)
		}
		if scope == "" && verb != "copy" {
			scope = string(preset.Scope)
		}
	}
	if scope == "" {
		scope = "user"
	}
	if verb == "copy" {
		id = agentpreset.NormalizeID(fs.Arg(1))
		preset.ID, preset.Name = id, id
	}
	if id == agentpreset.DefaultID {
		return presetCommandError(agentpreset.ErrBuiltin, 1)
	}
	if err := agentpreset.ValidID(id); err != nil {
		return presetCommandError(err, 2)
	}
	if verb == "new" || verb == "copy" {
		if err := requireNewPreset(root, scope, id); err != nil {
			return presetCommandError(err, 1)
		}
	}
	if verb == "edit" && !presetEditsRequested(fs) {
		return presetCommandError(errors.New("preset edit requires --prompt, --prompt-file, --tools, --name, --description or --keep-coding-instructions"), 2)
	}
	if err := applyPresetCommandEdits(&preset, fs, values); err != nil {
		return presetCommandError(err, 2)
	}
	saved, err := control.SaveAgentPresetForRoot(root, scope, id, agentpreset.Render(preset))
	if err != nil {
		return presetCommandError(err, 1)
	}
	fmt.Println(saved.Path)
	return 0
}

func requireNewPreset(root, scope, id string) error {
	dirs := scopedPresetDirs(root, scope)
	dir := dirs.User
	if scope == "project" {
		dir = dirs.Project
	}
	if _, err := os.Lstat(filepath.Join(dir, id+".md")); err == nil {
		return fmt.Errorf("preset %q already exists in %s scope; use preset edit", id, scope)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func presetEditsRequested(fs *pflag.FlagSet) bool {
	return slices.ContainsFunc([]string{"prompt", "prompt-file", "tools", "name", "description", "keep-coding-instructions"}, fs.Changed)
}

func applyPresetCommandEdits(preset *agentpreset.Preset, fs *pflag.FlagSet, values presetCommandOptions) error {
	if fs.Changed("prompt") && fs.Changed("prompt-file") {
		return errors.New("use only one of --prompt and --prompt-file")
	}
	if fs.Changed("prompt") {
		preset.Body = values.prompt
	}
	if fs.Changed("prompt-file") {
		var data []byte
		var err error
		if values.promptFile == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = fileencoding.ReadFileUTF8(values.promptFile)
		}
		if err != nil {
			return fmt.Errorf("read persona prompt: %w", err)
		}
		preset.Body = string(data)
	}
	if fs.Changed("name") {
		preset.Name = values.name
	}
	if fs.Changed("description") {
		preset.Description = values.description
	}
	if fs.Changed("keep-coding-instructions") {
		preset.KeepCoding = values.keepCoding
	}
	if fs.Changed("tools") {
		switch strings.TrimSpace(values.tools) {
		case "all":
			preset.Tools = nil
		case "", "[]":
			preset.Tools = []string{}
		default:
			preset.Tools = strings.Split(values.tools, ",")
		}
	}
	_, err := agentpreset.Parse(preset.ID, agentpreset.Render(*preset))
	return err
}
