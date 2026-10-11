# Configuration Paths

Starting with **Deepzora v1.8.1**, Deepzora uses one user-facing home directory
for global configuration and user-owned state. CLI and desktop share this
location.

## Deepzora Home

| Platform | Deepzora home |
| --- | --- |
| macOS | `~/.deepzora` |
| Linux | `~/.deepzora` |
| Windows | `%APPDATA%\deepzora` |

Set `DEEPZORA_HOME` to override Deepzora home for tests, CI, or portable
installations. Normal users should not need it.

When `DEEPZORA_HOME` is set, the runtime is fully self-contained: all
configuration, state, cache, and data live under that directory tree. Legacy
migration, OS-home convention directory scanning, and all other fallback paths
are skipped so no data leaks in from a system-wide production install.

Advanced test and portable setups may set `DEEPZORA_STATE_HOME` to move runtime
state such as sessions, archives, and memory. It does not move global config or
provider credentials: those remain under `DEEPZORA_HOME`. If an older build wrote
provider keys to `DEEPZORA_STATE_HOME/.env`, Deepzora imports those keys
non-destructively when `<Deepzora home>/.env` is missing them.

## What Lives There

| Data | Path |
| --- | --- |
| Global config | `<Deepzora home>/config.toml` |
| Global provider credentials | `<Deepzora home>/.env` |
| In-progress model credential commits | `<Deepzora home>/transactions/model-credentials/` |
| Completed model settings receipts | `<Deepzora home>/transactions/model-settings-receipts/` |
| Legacy credentials import source | `<Deepzora home>/credentials` |
| Global slash commands | `<Deepzora home>/commands/` |
| Global skills | `<Deepzora home>/skills/` |
| Global hooks | `<Deepzora home>/settings.json` |
| Remote-SSH managed known_hosts | `<Deepzora home>/remote/known_hosts` |
| Sessions | `<state root>/sessions/` |
| Archives | `<state root>/archive/` |
| Memory | `<state root>/memory/` and `<state root>/projects/` |
| Global Desktop topic metadata | `<state root>/desktop/topic-state-v1.sqlite` |
| Project Desktop topic metadata | `<state root>/projects/<workspace slug>/desktop/topic-state-v1.sqlite` |
| Disposable session catalog | `<cache root>/session-catalog/v6.sqlite` |
| Disposable history search catalog | `<cache root>/history-search/v1.sqlite` |
| Disposable usage catalog | `<cache root>/usage-catalog/v1.sqlite` |
| Disposable task catalog | `<cache root>/task-catalog/v1.sqlite` |

`<state root>` defaults to `<Deepzora home>`. It only differs when
`DEEPZORA_STATE_HOME` is set.

Desktop detects a project-directory name collision when it saves a newly added
project in `desktop-projects.json`. A new assignment is made only when another
recorded project still resolves to the same legacy directory. Existing projects
and projects imported from older workspace records keep their current
`<state root>/projects/<workspace slug>/` directory. Re-adding the original
project after its colliding peer was assigned elsewhere also keeps that legacy
directory. Only a newly added project that meets the collision rule uses
`<state root>/projects/@<SHA-256 of its absolute root>/`; its `.workspace-root`
file records the assignment. Session, topic, and project-memory paths follow
that assignment. Listing a project never creates or changes an assignment, and
existing files are never moved. If two projects were already recorded with the
same slug before this fix, their historical shared files remain in place: the
old directory does not identify which project owns each file.
Studio currently resolves only `<state root>/projects/<workspace slug>/` and does
not read `.workspace-root`; a newly assigned project's state is therefore not
shared with Studio until Studio supports these assignments.

Desktop topic titles, title sources, creation times, and automatic-title state
are authoritative in these SQLite files. On first access, Desktop imports the
legacy `desktop-topic-*.json` files from a project's `.deepzora/` directory (or
the global Deepzora directory). A scope with legacy files continues mirroring
them for downgrade compatibility; a fresh scope does not create them. Legacy
files are retained, and project-local settings, skills, commands, attachments,
and `deepzora.toml` are unaffected.

The session catalog is a rebuildable query projection, not user data. Session
JSONL, event logs, metadata sidecars, and `desktop-projects.json` remain
authoritative. See [Session Catalog and Desktop Startup](./SESSION_CATALOG.md).
The history projection is documented in
[History Search Catalog](./HISTORY_SEARCH_CATALOG.md).
The usage rollup projection is documented in [Usage Catalog](./USAGE_CATALOG.md).
Task snapshots and event logs likewise remain authoritative; the rebuildable
cross-project projection is documented in [Task Catalog](./TASK_CATALOG.md).

The global user config is named `config.toml`. Project-local config files keep
the name `deepzora.toml`. If someone says "global deepzora.toml", they usually
mean `<Deepzora home>/config.toml`.

## Global `config.toml`

`<Deepzora home>/config.toml` stores non-secret configuration shared by the CLI
and desktop app. It may contain the same provider, plugin, UI, desktop, tool,
skill, sandbox, bot, and agent settings that Deepzora renders into user config.
Provider entries store the name of the credential variable in `api_key_env`, not
the secret value.

Saved provider and bot credential variables are removed from every
model-controlled child-process environment. On macOS and Linux, the global
credential `.env` is also hidden from Deepzora's file readers, sandboxed shell
commands, and MCP servers; this does not change the visibility of a project's
ordinary `.env`. Windows has no OS-level shell sandbox: shell commands and
local tools run as the same OS user and can deliberately read user-readable
files, including the credential store, so treat restricted permissions there
as a tool-layer write boundary rather than a credential vault.

If a deny entry left behind by the retired Windows sandbox (v1.38.8 to
v1.38.10) blocks the credential store, Deepzora removes it automatically when
a marker from that sandbox run proves the entry came from Deepzora. Saving a
key works even without that proof: the save resets the file's ACL to the
current user without reading it, and if that is also denied it moves the
locked file aside as `.env.locked-<timestamp>` (a read deny does not block
the move) and writes a new store, so re-entering a key always succeeds. Plain reads never rewrite ACLs; they report
the original access error together with the repair outcome.

Example:

```toml
config_version = 11
default_model = "deepseek/deepseek-flash"
language = "zh"
credentials_store = "auto"   # legacy compatibility; provider keys are in .env

[ui]
theme = "auto"
cursor_shape = "bar"         # CLI/TUI text cursor: underline|block|bar
show_turn_usage = false       # hide per-request token/cost receipts in the TUI; default true

[desktop]
provider_access = ["deepseek"]

[[providers]]
name        = "deepseek"
kind        = "openai"
base_url    = "https://api.deepseek.com"
models      = ["deepseek-flash", "deepseek-v4-pro"]
default     = "deepseek-flash"
api_key_env = "DEEPSEEK_API_KEY"
web_search  = true

[[plugins]]
name    = "example"
command = "example-mcp-server"
```

Do not put API key values in `config.toml`. This file is regular configuration:
it is safe to inspect, edit, migrate, and include in diagnostics after standard
redaction. Secrets belong in the global `.env` below.

`[ui].cursor_shape` affects only the CLI/TUI composer. The default `bar` stays
visible without covering double-width CJK characters; use `block` or
`underline` if you prefer those cursor shapes.

`[ui].show_turn_usage = false` hides the token and cost receipt appended to the
TUI transcript after each model request. Accounting and live status updates
remain active. The default is `true`.

### Custom provider `api_key_env` names

When a provider credential is added, replaced, or explicitly cleared from
desktop settings, TUI `/setup`, or `deepzora setup`, Deepzora allocates a fresh
`DEEPZORA_CONNECTION_*_KEY` slot. It writes that slot first and atomically
publishes the selected provider's new `api_key_env` reference second. Other
providers keep their current references, even when they previously shared a
fixed variable. Existing fixed names remain readable and are not migrated at
startup.

Legacy and manually authored provider entries may derive a default from the provider name. Names that normalize to
ASCII keep readable env names such as `LOCAL_GATEWAY_API_KEY`; names made
entirely of non-ASCII characters get a stable hash suffix such as
`CUSTOM_d39b9067_API_KEY` so two Chinese provider names do not share
`CUSTOM_API_KEY`. Names beginning with a digit get a `CUSTOM_` prefix so the
generated environment variable remains valid; for example, `9router` becomes
`CUSTOM_9ROUTER_API_KEY`.

The CLI custom-provider wizard uses this rule for its draft name. For example
`https://token.sensenova.cn/v1` creates provider name
`custom-token-sensenova-cn`, whose draft key env is
`CUSTOM_TOKEN_SENSENOVA_CN_API_KEY`. Pressing Enter at the variable-name
prompt keeps that draft only until the key is saved; the saved connection then
uses a newly allocated private slot.

A variable name you type at that prompt in `deepzora setup` is kept, so scripts
can refer to a stable name, as long as saving under it changes nothing another
connection reads: no other provider, bot or remote-host setting in the config
reads it, the global `.env` holds no value (or cleared marker) for it, and the
environment Deepzora runs in does not already set it. Otherwise the wizard says
what holds the name and asks again; Enter falls back to a
private slot. If the name is claimed between the prompt and saving, the save is
refused and nothing is written.

Saving a new key later for a provider in the user config rewrites its
variable in place when that provider (or the set of providers the key is saved
for) is the only reader of it in the user config, both before and after the
edit, and the global `.env` already holds its value. A project that reads the
same name sees the new key, as it saw the old one. Providers declared in a
project `deepzora.toml` always get a private slot. The previous value is kept in the global `.env` under
a temporary variable until the config is published: a save that fails or is
interrupted puts it back, unless something else has written the variable since.
When another provider or setting also reads the variable, the new key goes to a
private slot as before and the shared variable is left unchanged.

Existing configs are not rewritten on upgrade. If an old custom provider already
uses `CUSTOM_API_KEY`, it will keep working with that key. If several old custom
providers accidentally share `CUSTOM_API_KEY`, save each provider's API key
again to rotate that connection to a private slot.

### Custom provider endpoint URLs

The desktop custom-provider form treats its **API address** as the exact request
URL and stores it in `request_url`; Deepzora does not append or rewrite its path.
Only `request_url` selects an exact request target. The historical `chat_url` field is unsupported.
Without `request_url`, the protocol path is derived from `base_url`.
If model discovery needs a separate address, set `models_url`; otherwise Deepzora
probes candidates derived from `base_url`.

If a gateway requires vendor-specific top-level request body fields, set
`extra_body`, for example `extra_body = { enable_thinking = true }`. These values
are merged into the OpenAI-compatible chat JSON request body without allowing
core fields such as `model`, `messages`, `tools`, or `stream` to be overridden.

## Global `.env`

`<Deepzora home>/.env` is the single runtime source for provider API keys saved
by Deepzora. The setup wizard, desktop settings, CLI missing-key prompts, and
provider-key delete actions all read or write this file through the same
credential helpers.

Structure:

```dotenv
DEEPSEEK_API_KEY=sk-...
GEMINI_API_KEY=...
ANTHROPIC_API_KEY=...
# deepzora-cleared OLD_API_KEY
```

Rules:

- one `KEY=value` assignment per line;
- blank lines and `#` comments are ignored;
- `export KEY=value` and quoted values are accepted when reading;
- multiline values are rejected by Deepzora writes;
- keys must use shell-style names such as `DEEPSEEK_API_KEY`;
- `# deepzora-cleared KEY` comments are non-secret tombstones written after a key
  is deleted so legacy stores do not silently re-import it;
- Deepzora writes this file with restricted permissions where the OS supports
  them.

For provider requests, Deepzora resolves only this global `.env`. Project `.env`
files, home `.env` files, inherited shell environment variables, the old
`credentials` file, and the OS keyring do not act as runtime provider-key
fallbacks. Project `.env`, home `.env`, and inherited shell environment values
are not imported into the global credentials file. The old `credentials` file
and old keyring entries are read only as non-destructive migration sources when
the new global `.env` is missing a key. Project `.env` files are still read as
workspace-scoped, non-provider expansion sources for `${VAR}` references in
MCP/plugin env, headers, URLs, commands, and args; those values are not written
into the process environment, and Deepzora control variables such as
`DEEPZORA_HOME`, `DEEPZORA_STATE_HOME`, and `XDG_CONFIG_HOME` are ignored there.

Caches remain in the OS cache directory, for example
`~/Library/Caches/deepzora` on macOS, `$XDG_CACHE_HOME/deepzora` or
`~/.cache/deepzora` on Linux, and `%LOCALAPPDATA%\deepzora\cache` on Windows.
Set `DEEPZORA_CACHE_HOME` to override the cache root. When `DEEPZORA_HOME` is
set, the cache is placed under `$DEEPZORA_HOME/cache` (unless
`DEEPZORA_CACHE_HOME` is also set, which takes precedence).

## Config Priority

Runtime configuration is resolved in this order:

```text
command-line flags
> project ./deepzora.toml
> global <Deepzora home>/config.toml
> compatible legacy global config
> built-in defaults
```

Writes always target the new global path:

```text
macOS/Linux: ~/.deepzora/config.toml
Windows:     %APPDATA%\deepzora\config.toml
```

## Legacy Migration

Starting with **v1.8.1**, Deepzora automatically checks legacy locations on
startup before the first config load. Migration is synchronous, one-time, and
non-destructive: old files are copied or converted to Deepzora home and left
untouched.

Legacy config sources include:

```text
~/Library/Application Support/deepzora/config.toml
~/.config/deepzora/config.toml
~/.deepzora/deepzora.toml
~/.deepzora/config.json
```

Legacy credentials, memory files, and sessions are also imported into Deepzora
home when the new destination does not already exist. Legacy provider keys are
copied into `<Deepzora home>/.env` only when that file does not already contain
the same key. If the new global config already exists, it wins and legacy config
files are only kept as compatibility fallbacks.

Starting in **v1.9.1**, Deepzora also backfills MCP servers from known legacy
paths, legacy `config.json`, desktop-registered projects, and restored tab
projects into the global `<Deepzora home>/config.toml`. Existing global
`[[plugins]]` entries win by name, so project or legacy entries never overwrite a
server the user already configured globally. Source files are left untouched, and
the backfill writes a one-time marker so a user-deleted global MCP server is not
recreated repeatedly from an old project config.

## Manual Migration Rescue

If Deepzora has already created the new home directory but some legacy data was
not present yet, or if the desktop app was opened before the old paths were
available, run the migration rescue command from either frontend:

```text
/migrate
```

In the CLI TUI, type `/migrate` into the chat input. In the desktop app, type the
same command into the composer. The command prints progress notices while it:

1. checks legacy config and credentials,
2. scans known legacy memory locations,
3. scans known legacy session directories,
4. imports memory files and sessions that were not previously imported, and
5. prints a final summary.

If old v0.x sessions live outside the known legacy locations — for example a
Windows v0.52 install/data directory chosen during setup — pass that directory
explicitly:

```text
/migrate --from "D:\OldDeepzora"
```

The explicit form imports sessions only. The path may be the old install
directory, a `.deepzora`/data directory, or the `sessions` directory itself;
Deepzora checks the common layouts below that root and uses a source-specific
marker, so a previous plain `/migrate` run does not hide the later import.

The rescue command is intentionally non-destructive. It does not overwrite an
existing `<Deepzora home>/config.toml`; if the new config already exists, copy
any missing legacy settings across by hand. It copies legacy memory files only
when the destination file is absent. It also respects session import markers, so
sessions that were already imported and later deleted by the user will not be
restored on a later `/migrate` run.

Version limits:

- Automatic migration starts in **v1.8.1**.
- `/migrate` is available only in Go-based Deepzora builds that include the
  command. If Deepzora reports `unknown command`, upgrade first and rerun it.
- The command is not available in the legacy `0.x` TypeScript line.
- Plain `/migrate` rescans the legacy locations listed above. Use
  `/migrate --from <path>` only for a known v0.x session source; it is not a
  backup restore tool or a downgrade importer.
