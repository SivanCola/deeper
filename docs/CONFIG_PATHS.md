# Configuration paths

CLI and Desktop share a single Deepzora home. On macOS/Linux it is `~/.deepzora`; on Windows it is `%APPDATA%\deepzora`. `DEEPZORA_HOME` overrides it for portable installs and isolated development.

Configuration precedence is flags, project `deepzora.toml`, global `config.toml`, then defaults. Provider credentials live only in `<home>/.env`, referenced through `api_key_env`. Project `.env` can expand workspace MCP/plugin variables; it does not supply provider keys. There is no legacy credential import, keyring fallback, or automatic data migration.

`DEEPZORA_STATE_HOME` optionally changes the state root; it does not move global configuration or credentials. `DEEPZORA_CACHE_HOME` changes the cache root. With only `DEEPZORA_HOME` set, cache lives in `<home>/cache`. Otherwise cache uses the OS user-cache directory. Workspace writer and repair locks use a shared OS-user cache so isolated profiles cannot acquire conflicting ownership of the same workspace.

| Data | Current path |
| --- | --- |
| Global configuration | `<home>/config.toml` |
| Provider credentials | `<home>/.env` |
| Credential transactions | `<home>/transactions/model-credentials/` |
| Model settings receipts | `<home>/transactions/model-settings-receipts/` |
| Commands, skills, hooks | `<home>/commands/`, `<home>/skills/`, `<home>/settings.json` |
| Remote SSH host keys | `<home>/remote/known_hosts` |
| CLI session records | `<state>/sessions/`, `<state>/sessions-v4/` |
| Desktop session records | `<state>/desktop-sessions-v5/by-id/` |
| Desktop workspace registry | `<state>/desktop/workspace-state-v1.json` |
| Unsent session input | `<state>/desktop/session-ui-v1.sqlite` |
| Topic metadata | `<state>/desktop/topic-state-v1.sqlite`, or the corresponding project state directory |
| Archives and memory | `<state>/archive/`, `<state>/memory/`, `<state>/projects/` |
| Usage statistics | `<state>/stats/` |
| Disposable indexes | `<cache>/session-catalog/v6.sqlite`, `history-search/v1.sqlite`, `usage-catalog/v1.sqlite`, `task-catalog/v1.sqlite` |

Desktop records are addressed by SessionID. Workspace assignment belongs to the workspace registry; moving a session between workspaces does not move its transcript. Unsent input and staged images are separate from committed transcript data.

Credentials are written through the model-settings transaction owner and must not enter project files, logs, or release artifacts. Disposable indexes can be rebuilt; session records, workspace assignments, input, credentials, and transaction receipts are durable data.

Numeric suffixes describe the current schemas. The first-release baseline does not rename these directories or silently reinterpret older formats. Only the current formats are supported. See [current baseline](current-version-baseline.md) and [configuration schema](SPEC.md#5-configuration-toml).
