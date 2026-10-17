# Deepzora CLI Reference

<a href="../README.md">README</a>
&nbsp;·&nbsp;
<a href="./CLI.zh-CN.md">简体中文</a>
&nbsp;·&nbsp;
<a href="./GUIDE.md">Guide</a>

This reference covers interactive sessions, one-shot automation, session
resume, permission flags, and the most useful in-session commands. For provider
configuration, plugins, and sandbox policy, see the [Guide](./GUIDE.md).

## Start a session

```sh
deepzora
deepzora --model deepseek-pro
deepzora --effort high
deepzora --dir /path/to/project
```

Ordinary requests always enter the executor. There is no automatic simple /
light / full task mode to pick. The dedicated planner runs only for an
explicit Plan, an approval boundary, or Goal start.

Running `deepzora` without a subcommand starts the interactive terminal UI. If
the selected connection has no credential, the local connection picker opens
instead of sending a request. History and local commands remain available while
authentication is incomplete.

| Flag | Purpose |
| --- | --- |
| `--model NAME` | Select a configured provider or `provider/model` reference. |
| `--preset ID` | Select an agent persona/tool preset for a new session; `default` is the built-in agent. Resuming restores the session's recorded preset. |
| `--effort LEVEL` | Override reasoning effort for this session. |
| `--max-steps N` | Set a one-off maximum tool-call round budget; `0` uses automatic execution. |
| `--dir PATH` | Change the workspace root before loading config and tools. |
| `--add-dir PATH` | Add another writable tool directory; repeat for multiple directories. |
| `-c`, `--continue` | Resume the most recent session. |
| `-r`, `--resume [QUERY]` | Open the session picker, or resume a matching session. |
| `--copy` | Continue in a writable copy of the resumed session. |
| `--allowed-tools RULES` | Add session-only permission allow rules. Repeatable; `--allowedTools` is an alias. |
| `--permission-mode MODE` | Start with a specific permission posture. |
| `--dangerously-skip-permissions` | Deprecated compatibility flag; migrates conservatively to `workspace-write`. Use `--permission-mode danger-full-access` for YOLO. |

Flags may appear before or after the prompt where applicable.

## Agent persona and tool presets

`--preset ID` works with interactive `deepzora`/`chat`, `run`, `serve`, and `web`.
A preset selects the persona and admitted tools; model and permission settings
remain independent. The built-in `default` preset is read-only.

```sh
deepzora preset list [--scope user|project] [--dir PATH] [--json]
deepzora preset show <id> [--scope user|project] [--dir PATH] [--json]
deepzora preset new <id> [--scope user|project] [--prompt TEXT | --prompt-file PATH] [--tools LIST] [--dir PATH]
deepzora preset edit <id> [--scope user|project] [--prompt TEXT | --prompt-file PATH] [--tools LIST] [--dir PATH]
deepzora preset copy <source-id> <new-id> [--scope user|project] [--dir PATH]
deepzora preset rm <id> [--scope user|project] [--dir PATH]
```

Management commands work before configuring a model or API key. New definitions
and copies default to `user` scope under `<Deepzora home>/presets/`. Project
presets live in `<workspace>/.deepzora/presets/` and override user definitions
with the same ID. `show`, `edit`, and `rm` use the visible definition unless
`--scope` selects one scope explicitly. For `copy`, `--scope` selects the
destination; the source is the visible definition. Existing destinations are
rejected. IDs use 1–64 lowercase letters, digits, or hyphens and start with a
letter or digit.

`--prompt-file -` reads the persona from stdin. New, edit, and copy also accept
`--name`, `--description`, and `--keep-coding-instructions=false`; the latter
requires a nonempty persona and replaces the standard coding instructions.
Copy accepts the same prompt and tool overrides as new and edit.

`--tools` takes comma-separated built-in names/globs and whole MCP servers such
as `read_file,web_*,mcp:docs`. Omit it for unrestricted tools on new presets or
to retain the existing value during edit/copy. `--tools all` explicitly restores
unrestricted tools; `--tools '[]'` or `--tools ''` admits no tools. Permission
rules still apply to admitted tools.

```sh
deepzora preset new reviewer --prompt "Review changes carefully" --tools 'read_file,mcp:docs'
deepzora --preset reviewer
deepzora run --preset reviewer "review the current diff"
deepzora web --preset reviewer
deepzora serve --preset reviewer
```

Inside a blank session, `/preset list` lists definitions, `/preset current`
(or `/preset`) shows the current choice, and `/preset select <id>` rebuilds the
agent with that selection. Once a turn starts the preset identity is locked;
start a new session to select another. Model changes preserve the identity.
Explicit resume restores the recorded ID and its latest saved definition;
`--preset` cannot replace a resumed session's selection. Editing a file leaves
an already assembled controller unchanged until an explicit resume or `/reload`.
A removed definition retains its recorded ID and uses default behavior until
restored. See [Agent presets](./AGENT_PRESETS.md) for the file format.

## Update the native CLI

```sh
deepzora upgrade                  # install the latest official release
deepzora upgrade --check          # report the target without installing
deepzora upgrade --force          # reinstall the current official release
```

The updater selects only strict `vX.Y.Z` non-prerelease GitHub Releases. Historical channels and retired flags are unsupported.

## Configure providers

```sh
deepzora setup                    # manage the user-global config
deepzora setup --local            # manage ./deepzora.toml
deepzora setup /path/to/config.toml
```

In an interactive terminal, `deepzora setup` is a staged provider manager. It
lists configured providers and lets you:

- add OpenAI-compatible or Anthropic-compatible providers;
- edit endpoints and model lists;
- update API keys or test the connection and refresh models;
- choose the default model; and
- remove providers.

Choose **Save and exit** to review and confirm the pending operations. Canceling
discards them. Setup reloads the latest config while saving: unrelated desktop
or CLI changes are retained, while an overlapping change is reported as a
conflict instead of being overwritten.

Provider definitions contain only the `api_key_env` variable name. Key values
are stored in the shared Deepzora home `.env`, even with `--local`. Adding,
replacing, or explicitly clearing a key creates a fresh private credential slot
and atomically switches only the selected connection to it. Existing fixed
variables remain readable and migrate only when that connection is edited.

Inside the TUI, `/setup` opens the same connection flow and `/auth` is an alias.
The key field is masked; press `Ctrl+T` to test the draft connection, Enter to
save, or Escape to cancel. `/?` is an alias for `/help`. Authentication that is
not ready never turns ordinary input into a provider request.

```sh
deepzora doctor credentials
deepzora doctor credentials --json
deepzora doctor credentials --probe
deepzora doctor credentials --repair --dry-run
deepzora doctor credentials --repair
```

The default diagnostic is read-only. `--probe` tests temporary create and
atomic rename without replacing `.env`. Repair is limited to a current-user-
owned regular file inside Deepzora home; it does not take ownership, remove deny
rules, grant `Everyone`, follow links/reparse points, or kill a file holder.

### Configure fee display currency

Use the user-global command to inspect or select the display currency:

```sh
deepzora config currency             # show the saved and resolved currency
deepzora config currency auto        # wallet hint, then original price currency
deepzora config currency CNY
deepzora config currency USD
```

`auto` remains unresolved in configuration. With one valid wallet currency it
can become a runtime session hint; otherwise CLI uses the original currency or
sorted currency buckets. Language and host locale never select a price table.
The preference is user-global and cannot be overridden by project
`deepzora.toml`; `--local` is therefore not supported. Custom prices are preserved.

In an interactive session, `/currency` shows the saved and resolved values, and
`/currency auto|CNY|USD` changes the preference and refreshes the current
runtime without discarding the conversation.

### Configure automatic compaction

The desktop app and CLI share the user-global automatic compaction threshold.
Inspect the effective percentage and its source, set the global default, or add
a project override:

```sh
deepzora config compact-ratio              # show effective value and source
deepzora config compact-ratio 75           # set the user-global default
deepzora config compact-ratio --local 75   # override in ./deepzora.toml
```

The editable range is 30–85%, with 80% as the built-in default. Lower values
compact earlier, may increase summary calls and cost, and may reduce
prompt-prefix cache reuse; higher values retain more context before compaction.
Below the threshold, complete tool results may
increase ordinary request cost; at pressure they are durably pruned before the
cache-aligned summary runs. Project `deepzora.toml` takes precedence over
the user config. Changes apply to new CLI sessions; an already-running session
keeps the threshold it loaded at startup.

## One-shot and automation

Use `-p` / `--print` when a script needs only the final answer:

```sh
deepzora -p "summarize this repository"
deepzora -p "summarize this repository" --output-format json
deepzora run "implement the TODOs in main.go"
deepzora run --auto "implement the TODOs in main.go"
echo "explain this code" | deepzora run
```

`deepzora run` keeps the normal streamed terminal presentation unless `-p` or a
structured output format is selected. It also accepts `--model`,
`--preset`, `--max-steps`, `--effort`, `--dir`, `--add-dir`,
`--continue`, `--resume QUERY`, `--copy`, `--allowed-tools`, and `--permission-mode`.

### Benchmark arms

`--ablate` switches whole subsystems off so a benchmark can attribute a change
in success rate to one of them. It accepts a comma-separated list of `evidence`,
`planner`, `subagent`, `retrieval` and `compaction`, plus `none` (the default,
everything on) and `all`. Sub-agents inherit the parent's arm, and the arm name
is written to the `--metrics` file so a recorded run is self-describing.

```sh
deepzora run --ablate evidence,planner --metrics run.json "fix the failing test"
```

This is a measurement tool, not a tuning knob: switching a subsystem off makes
Deepzora worse at the work it was added for.

### Trajectory recording

`--trajectory PATH` appends the run's full event stream — tool dispatches and
results with absolute start/end times, reasoning, retries, readiness and
recovery decisions — as one timestamped, sequenced JSONL record per event, so
a run can be replayed and its time attributed offline (tool execution vs. the
model thinking between calls). Records reuse the shared `eventwire` JSON
contract under an `event` key, wrapped in `schema_version`, `seq`, and `ts`
(unix ms). Every completed line survives a killed run. Unlike `--events-jsonl`,
the file contains prompts, tool arguments, and reasoning: treat it with the
same care as a session transcript.

```sh
deepzora run --metrics run.json --trajectory run.trajectory.jsonl "fix the failing test"
```

### Turn phases

While a turn runs, the host publishes a content-free phase so a frontend can
say what the turn is doing. The CLI shows it on the spinner line; the desktop
app shows it in the composer.

These phases describe execution timing, not verification evidence. Desktop
check-result cards follow actual running verification tools, not phase names.

| Phase | Emitted when | `capability_phases` bucket |
| --- | --- | --- |
| `working` | the turn starts, after each tool batch returns, after model generation | `ProviderWaitMs` |
| `checking` | a tool batch is about to execute | `ToolExecMs` |
| `verifying` | an actual verification tool runs | `ToolExecMs` |

A phase is billed to its bucket when the next phase opens, so the durations in
`--metrics` split a turn into model wait versus tool execution without replaying
the run. Spans under a millisecond are dropped, and a turn that ends through an
error or a pause rather than an answer does not bill its last span, so the
buckets read as a lower bound rather than a full partition of the turn.

An approval prompt raised inside a tool batch bills to `ToolExecMs`: the batch
stays open from `checking` until the next `working`, and no user-wait phase is
emitted. `ReviewMs`, `SubagentWaitMs`, `UserWaitMs` and `CompactMs` stay zero
because nothing opens those phases inside a turn — `reviewing` is published only
at run exit, after the turn's phase clock has already closed.

### Output formats

| Format | Behavior |
| --- | --- |
| `text` | Human-readable text. With `-p`, prints only the final answer. |
| `json` | Emits one final result object. |
| `stream-json` | Emits one shared `eventwire` JSON object per line, followed by the final result object. |

```sh
deepzora -p "list the risky changes" --output-format text
deepzora -p "summarize the diff" --output-format json
deepzora run "run the tests" --output-format stream-json
```

The final structured object has this shape:

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": false,
  "duration_ms": 123,
  "num_turns": 1,
  "result": "...",
  "result_from_reasoning": false,
  "session_id": "...",
  "total_cost": 0,
  "currency": "USD",
  "total_cost_usd": 0,
  "usage": {
    "input_tokens": 0,
    "output_tokens": 0,
    "cache_read_input_tokens": 0,
    "cache_creation_input_tokens": 0
  }
}
```

`result_from_reasoning` is present, and `true`, when the turn finished with an
empty visible message and `result` therefore carries the turn's reasoning
instead. A thinking model may answer entirely in the reasoning channel; without
the field a caller cannot tell that text apart from a visible answer, and
without the fallback `result` would be `""` and `-p` would print nothing. It is
omitted whenever the model emitted visible text, which is the ordinary case.

`total_cost` is present only when a single `selected` display amount exists (ISO
code in `currency`). Prefer the structured `cost_quote` field when present: it
carries the original estimate, `original_totals`, occurrence-time valuations
(`official_table` for dual-region public prices), `cost_complete`,
`display_complete`, `display_status`, and `billing_mode` (`payg` or `subscription_equivalent` for
pay-as-you-go equivalent estimates such as MiMo Token Plan).

`total_cost_usd` remains a numeric compatibility alias when `total_cost` exists
and does **not** imply USD. Mixed original currencies no longer fail the run:
`cost_complete` remains true when usage/pricing facts are known,
`display_complete` is false, and `original_costs`/`original_totals` list per-ISO
totals so clients never invent a cross-currency sum.

Global display preference is `[billing].display_currency` (`auto|CNY|USD`);
Provider list prices use each
entry's frozen `billing_currency` and are never rewritten by display switches.
Diagnose with `deepzora doctor billing`.

Execution failures use `subtype: "error_during_execution"` and
`is_error: true`. Structured modes keep runtime errors in JSON instead of also
printing a duplicate human-readable error. Authentication failures also include
optional `error_code`, `authentication_status`, and `recovery_actions` fields.
The same fields appear on the final `run_done` record from `--events-jsonl`.
For example, a missing key reports `missing_credential` and actions such as
`configure_credentials`, `select_model`, and `diagnose_credentials`; no model
request is made.

The completion validator has been removed. A clean model stop without tool
calls ends the turn directly; a response with tools continues through the tool
loop, and a truly empty response is retried at the frozen-request boundary.
Explicit budgets, tool-safety and protocol recovery boundaries remain active.
Goal completion is a model declaration; no host quality gate or independent
Goal evaluator runs. See [migration details](EXECUTION_MODEL_SIMPLIFICATION.md).

### Redacted machine interfaces

Use the dedicated event flag when an automation needs lifecycle telemetry but
must not receive prompts, reasoning, tool arguments, tool output, or approval
text:

```sh
deepzora run --events-jsonl "run the focused tests"
```

Every line has `schema_version`, `sequence`, and `kind`; the final line is
`kind: "run_done"`. `--events-jsonl` is intentionally separate from the richer
`--output-format stream-json` contract and cannot be combined with
`--output-format`.

The following read-only commands expose persisted state without transcript,
label, command, output, path, PID, or host-name content. Here, read-only means
the commands do not mutate transcript, runtime, recovery, or query state. The
first redacted-machine invocation may initialize a private identity key in the
Deepzora user-state directory:

```sh
deepzora session list --json [--dir SESSION_DIR | --project-root PATH]
deepzora session show <machine-session-id> --json [--dir SESSION_DIR | --project-root PATH]
deepzora session status <machine-session-id> --json [--dir SESSION_DIR | --project-root PATH]
deepzora task list --json [--dir SESSION_DIR | --project-root PATH] [--session MACHINE_SESSION_ID]
deepzora task show <task-id> --json [--dir SESSION_DIR | --project-root PATH] [--session MACHINE_SESSION_ID]
deepzora task monitor list --json [--dir PROJECT_DIR]
deepzora task monitor status <task-id> --json [--dir PROJECT_DIR]
deepzora task monitor events <task-id> --json|--jsonl [--dir PROJECT_DIR] [--after N] [--follow]
deepzora hook list --json [--project-root PATH] [--home-dir PATH]
deepzora hook status --json [--project-root PATH] [--home-dir PATH]
```

For `session` and `task`, `--dir` explicitly selects the session storage
directory, while `--project-root` resolves the selected project's session
store. The two options cannot be combined. Without either option, Deepzora
selects the current project's session store.
For `hook`, `--dir` is an alias for `--project-root`.
`hook list` reports `active` or `invalid`; `invalid` means the
configured event cannot execute because its event, command/context source, or
tool-event matcher is unusable. Matchers on non-tool events are ignored.

Machine session IDs are keyed opaque hashes, not transcript file names. They
remain stable for the same session and Deepzora user-state directory, while a
different installation key produces unrelated IDs and prevents offline guesses
from timestamps or model labels. Preserve the private identity key when moving
the Deepzora state directory if automation depends on existing machine IDs.
Task `finished_at` is empty while a task is running, and
`artifact_complete=true` is emitted only for a terminal task whose persisted
artifact exists. A `running` record without a live session lease is reported as
`interrupted`; opening that session also repairs the persisted lifecycle state.

Schema compatibility rules for version 1:

- consumers must ignore unknown fields;
- fields are not removed or retyped within the same schema version;
- empty collections are encoded as `[]`;
- argument errors exit with status `2`, state/query errors with status `1`;
- machine-command errors are JSON objects with a stable `error.code`.

## Resume sessions

```sh
deepzora --continue
deepzora --resume
deepzora --resume provider-config
deepzora --resume <session-id>
deepzora --resume provider-config --copy
```

- `--continue` resumes the newest saved session immediately.
- Bare `--resume` opens the searchable picker in an interactive terminal.
- `--resume QUERY` accepts an exact session ID or path, or a unique title or
  preview substring. Missing and ambiguous matches fail with a descriptive
  error.
- `--resume=true` and `--resume=false` remain accepted for compatibility.
- `--copy` leaves the original transcript untouched and continues in a new
  writable session. Use it when another Deepzora process owns the original.

For one-shot runs, `deepzora run --resume QUERY "task"` accepts a session file
path, a session ID, or an opaque machine session ID from `--events-jsonl` /
`deepzora session show --json`. Session leases prevent the desktop app and CLI
from writing the same transcript concurrently.

## Permissions

```sh
deepzora --permission-mode read-only
deepzora --permission-mode workspace-write
deepzora --permission-mode danger-full-access
deepzora -p "run the focused tests" --allowed-tools "Bash(go test ./...)"
```

| Preset | Behavior |
| --- | --- |
| `read-only` | Read the workspace; writes and external side effects require a scoped authorization. |
| `workspace-write` | Write inside the workspace and private session temporary directory. This is the default. |
| `danger-full-access` | Run as the current OS user without Deepzora filesystem or network sandboxing. Explicit host deny rules still apply before launch. |

Inline scripts, pipes, substitutions, and shell `-c` forms follow the same
preset and sandbox boundary as other commands. Syntax alone never creates an
approval request.

`--allowed-tools` is a session permission override, not a provider tool-schema
filter. Rules may be comma- or space-separated, and the flag is repeatable.
Configured deny rules always win over command-line allow rules.

In non-interactive runs (`deepzora run` / `-p`) there is no prompt to answer.
`read-only` therefore fails closed for writes and side effects unless a narrow
authorization was supplied at startup. `workspace-write` runs normal builds,
tests, pipes, and inline scripts inside the OS sandbox. `danger-full-access`
must be explicit and still cannot bypass configured deny rules.

## Additional directories

```sh
deepzora --add-dir ../shared
deepzora -p "update both projects" \
  --add-dir ../frontend \
  --add-dir ../backend
```

Relative paths resolve from the workspace root and must already exist as
directories. Deepzora resolves symlinks, removes duplicates, and extends the
file-writer and sandboxed Bash write boundaries for the session. These additions
are runtime-only and are not written to configuration.

## Interactive controls

The `/model`, `/provider`, and `/resume` commands use searchable pickers.
Approval prompts use the same row-selection behavior while retaining their
single-key shortcuts.

| Key | Action |
| --- | --- |
| `Up` / `Down`, `Ctrl+P` / `Ctrl+N` | Move through picker or approval rows. |
| `j` / `k` | Move while the search is empty; after search input starts, enter `j` / `k` as query text. |
| Type | Filter a searchable picker. |
| `Enter` | Select the highlighted row. |
| `Esc` | Cancel the current picker or approval. |
| `y` / `a` / `n`, number keys | Allow once, allow the displayed scope for this session, or deny. |
| `Shift+Tab` | Cycle Read only → Workspace write → YOLO → Plan. |
| `Ctrl+Y` | Toggle YOLO; the runtime permission preset is `danger-full-access`. |

The responsive footer keeps interaction state on the left and, when space
allows, places model and effort on the right. Its second row shows
available repository and session telemetry such as cache hit rate, context use,
compaction headroom, background jobs, and balance. `ready` means the composer is
idle; that slot changes when a picker, approval, image paste, shell mode, or
other interaction needs attention. Narrow terminals move or compact complete
groups instead of cutting labels in half. Visible labels follow `/language`.

Use `/theme auto|light|dark` to select the terminal background mode, or choose a
named accent from `/theme`. Both composer borders, the insertion cursor,
selection, scrollbar, and footer use the active CLI theme. See
[Keyboard shortcuts](./GUIDE.md#keyboard-shortcuts) for transcript navigation,
multiline input, rewind, and clipboard controls.

Clipboard actions are deliberately split by content type. Local transcript
and composer selections use the native system clipboard and report success only
after that write completes; SSH falls back to an explicitly labelled OSC 52
request. Text paste remains the terminal's bracketed-paste action (`Cmd+V` on
macOS and the terminal's configured shortcut elsewhere). While Deepzora owns the
mouse in a local session, right-click with no selection reads clipboard text
through the same paste path; right-click with a selection copies it. Over SSH,
use the terminal paste shortcut because the remote process cannot read the local
clipboard; `/mouse` restores the terminal's native right-click menu. Image paste
is application-owned: use `Ctrl+V` on macOS/Linux, `Alt+V` on Windows, or
`/paste-image`; the footer shows `Pasting image…` until the attachment token is
ready. Where the terminal forwards that shortcut instead of pasting itself, a
clipboard holding no image falls back to a text paste, so the key never swallows
plain text.

## In-session commands

Type `/help` in an interactive session for the complete command list. Slash
completion, help, dispatch, and aliases are generated from the same registry, so
the displayed list matches the commands the TUI accepts.

| Command | Purpose |
| --- | --- |
| `/continue-checks [guidance]` | Resume the immediately preceding paused task-completion check while preserving its verified tool evidence. The command is one-shot and refuses stale cards after another user turn. |
| `/model` | Search configured models and switch the active model. |
| `/provider` | Choose a provider, then choose one of its configured models. |
| `/preset [list\|current\|select <id>]` | Inspect agent persona/tool presets, or rebuild a blank session with a selected preset. |
| `/resume` | Search recent sessions and switch to one. |
| `/takeover` | Take over the last refused session (or a listed entry) from the resident serve: this CLI becomes the writer and remote viewers become read-only spectators until they reclaim. After a desktop reclaim it re-takes the remembered session directly; a session no runtime holds any more is simply resumed. |
| `/status` | Show model, effort, cache, Git, background jobs, and balance details. |
| `/theme [auto\|light\|dark\|style]` | View or change the CLI background mode and accent palette. |
| `/currency [auto\|CNY\|USD]` | View or change the user-global fee display currency and refresh the runtime. |
| `/paste-image` | Read a clipboard image and insert an editable attachment token. |
| `/mouse` | Toggle in-app mouse selection, scrollbar, and wheel handling; SSH sessions start with capture off so the terminal's native selection works. |
| `/effort` | View or change reasoning effort. |
| `/output-style` | Select an answer style. |
| `/verbose` | Toggle expanded reasoning display. |
| `/sandbox` | Inspect sandbox status. |
| `/goal [objective]` | Start a continuous goal, or inspect its runtime statistics. |
| `/goal status` | Show the active goal plus turns, requests, tokens, work time, and the last continuation reason. |
| `/goal pause` | Pause the running goal (keeps todos, Delivery checkpoint, and runtime history). |
| `/goal resume` | Resume a manually paused or genuinely blocked goal without changing a numeric quota. |
| `/goal clear` | End goal mode permanently. |
| `/docs [question]` | Show the embedded corpus identity, or search it locally and ask the configured AI to answer from version-matched evidence. |
| `/deepzora:docs [question]` | Preferred built-in fallback when an existing custom command or compatible plugin/skill alias owns `/docs`; if this spelling is also owned, the menu selects the next free `deepzora:`-qualified name without displacing it. |
| `/mcp`, `/skills`, `/hooks` | Inspect and manage extensions. |
| `/remember <note>` | Append a standing note to the project instruction document; `# <note>` is a shortcut. |
| `/memory [subcommand]` | Inspect instructions, memory provenance, recall, revisions, and recovery. |
| `/rewind` | Restore conversation and/or code to an earlier turn. |
| `/tree`, `/branch`, `/switch` | Inspect or navigate conversation branches. |
| `/reload` | Reload the agent runtime (extensions, tools, skills, commands, hooks, providers) while keeping the session. Queued once while a turn runs, then fail-atomic: a failed rebuild keeps the current runtime. |

Switching model or effort rebuilds the runtime while preserving the
active conversation, session-scoped permission overrides, additional directory
access, and session ownership. `/reload` uses the same fail-atomic rebuild.
Execution modes no longer exist: planning, verification, and review strength
follow task risk per turn.

## Session catalog diagnostics

The desktop session catalog is a disposable SQLite query projection; transcript
JSONL and sidecars remain authoritative. Inspect it read-only or replace only
the projection:

```sh
deepzora doctor sessions [--json]
deepzora sessions reindex [--json]
deepzora sessions reindex --dir /path/to/sessions --dir /another/path
```

Without `--dir`, reindex includes global sessions and all projects saved by the
desktop app. See [Session Catalog and Desktop Startup](./SESSION_CATALOG.md) for
failure, migration, and data-safety guarantees.

History search uses a separate disposable projection:

```sh
deepzora doctor catalogs [--json]
deepzora catalogs reindex history [--dir PATH ...] [--json]
```

See [History Search Catalog](./HISTORY_SEARCH_CATALOG.md).
Usage statistics use a separate disposable rollup projection:
deepzora catalogs reindex usage [--json]
See [Usage Catalog](./USAGE_CATALOG.md).

Inspect or rebuild the disposable task projection independently:

```sh
deepzora doctor catalogs [--json]
deepzora catalogs reindex tasks [--project PATH ...] [--json]
```

See [Task Catalog](./TASK_CATALOG.md) for the authoritative FileStore boundary,
cross-project routing, and rebuild behavior.

### Memory diagnostics and recovery

Bare `/memory` shows all active project/global facts without hiding same-name
entries. Facts include their stable ID, revision, scope, type, freshness, and
description. Slash completion offers the available subcommands, active IDs and
names, and owned archive paths.

| Command | Purpose |
| --- | --- |
| `/memory instructions` | Show resolved instruction precedence, directories, imports, and diagnostics. |
| `/memory recall` | Explain the latest automatic recall query, hits, scores, reasons, freshness, and budget. |
| `/memory revisions <id-or-name>` | Show the active revision and immutable history. |
| `/memory restore <id-or-name> <revision>` | Restore old content as a new monotonic revision. |
| `/memory archived` | List archived facts and their owned paths. |
| `/memory recover <archive-path>` | Recover an archive as a new revision without overwriting active data. |

These commands run against the active session controller. When the session
lives on a remote host (`deepzora remote connect` / a desktop remote web
window), they use the remote memory catalog and never fall back to local
desktop memory. See [Context Engine v2](./SESSION_MEMORY_RETRIEVAL.md) for
authority, automatic recall, write confirmation, and migration behavior.
