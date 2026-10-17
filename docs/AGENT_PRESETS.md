# Agent presets

[简体中文](AGENT_PRESETS.zh-CN.md)

An agent preset saves a persona and an optional tool allowlist for a conversation.
Use one for a reviewer, documentation writer, or another recurring role. Model
selection, permission mode, and planning or verification policy remain separate
settings.

## Desktop

Open **Settings → Agent presets** to create a definition, edit it, copy it, or
delete it. Choose **User** to reuse it across projects or **Project** to save it
with the current workspace. The built-in **Default** definition is read-only;
copy it to start your own preset.

Choose a preset beside the model and permissions in an empty conversation's
composer. After the first turn starts, the selection is locked. Start another
conversation to choose a different preset. Saving a definition does not change a
running conversation's prompt or tools.

Remote tabs use the remote workspace's preset files. The remote service must
advertise `agent-preset-v1`; older services show an unavailable message for this
feature.

## Terminal

```sh
deepzora preset new reviewer --scope user --prompt "Review changes and explain findings." --tools read_file,grep,glob
deepzora preset list
deepzora preset show reviewer
deepzora --preset reviewer
deepzora run --preset reviewer "Review the changes in this workspace"
```

In chat, `/preset` or `/preset current` shows the current selection; `/preset list`
lists definitions. `/preset select reviewer` selects a role while the conversation
is empty; `/preset select default` restores the standard agent. See the
[CLI reference](CLI.md) for authoring commands and flags.

## Files and scopes

Definitions are Markdown files named `<id>.md`:

- User: `<Deepzora home>/presets/<id>.md`, normally `~/.deepzora/presets/`.
- Project: `<workspace>/.deepzora/presets/<id>.md`.

`DEEPZORA_HOME` also relocates user presets. A project definition overrides the
same ID in the user directory. IDs contain 1–64 lowercase letters, digits, or
hyphens and start with a letter or digit. `default` is reserved.

```markdown
---
name: Reviewer
description: Reviews code and reports actionable findings
keep-coding-instructions: true
tools: [read_file, grep, glob, "mcp:github"]
---
Review the requested changes. Explain each finding with a concrete failure
scenario and a file location. Ask before making edits.
```

Supported frontmatter fields:

| Field | Meaning |
| --- | --- |
| `name` | Display name; defaults to the file ID. |
| `description` | Description shown in the picker and management page. |
| `keep-coding-instructions` | Defaults to `true`: append the body to the base persona. `false` replaces that persona; a nonempty body is required. Core policies still apply. |
| `tools` | Omit for all available tools. Use a list to admit only matching tools; `[]` admits no execution tools. |

Unknown frontmatter keys and malformed definitions are rejected. Tool names may
use glob patterns such as `read_*`. The shell names `bash` and `pwsh` are treated
as portable aliases. Tool restrictions also apply to tools added later.

MCP admission is per server: `mcp:github` admits that server and `mcp:*` admits all
configured MCP servers. Per-tool entries such as `mcp__github__create_issue` are
rejected. `use_capability` remains available as a discovery and routing tool; it
does not grant access to tools or servers excluded by the preset. Existing
permission checks still govern admitted operations.

## Session behavior

The session records the selected ID. Reopening a saved custom-preset session
loads the current definition with that ID, including its latest persona and tool
allowlist. Forked history inherits the source preset. A running conversation
keeps its assembled system prefix and tool schemas stable across turns.

If a recorded definition is deleted, the session keeps the ID, reports a warning,
and runs the default agent until the file is restored. An explicit request to
start with an unknown ID fails. A malformed existing definition reports an error
so it can be corrected.

## HTTP

Authenticated Serve clients can use these additive endpoints:

| Request | Body or response |
| --- | --- |
| `GET /agent-presets` | Returns `{presets, problems, current, locked}`; definitions include their bodies and scopes. |
| `PUT /agent-presets/{id}` | JSON `{ "scope": "user", "content": "<Markdown definition>" }`; returns the saved definition. |
| `DELETE /agent-presets/{id}` | JSON `{ "scope": "user" }`; deletes that scope's file. |
| `POST /agent-preset` | JSON `{ "agentPreset": "reviewer" }`; switches an empty conversation. |
| `POST /new` | Optional JSON `{ "agentPreset": "reviewer" }`; starts a new conversation using that preset. |

Use `project` instead of `user` for workspace definitions. Omitting `agentPreset`
from `/new` retains the current preset ID and loads its latest definition; pass
`"default"` to start with the standard agent. Session-changing requests use Serve's
existing expected-session identity fence. Locked selection returns HTTP 409.
`/status` includes `agentPreset` and `agentPresetLocked`.
