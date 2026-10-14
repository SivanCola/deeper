# Privacy policy

[中文](PRIVACY.zh-CN.md) · [Code signing policy](CODE_SIGNING_POLICY.md)

Last updated: 2026-10-14. This policy covers the Deepzora CLI and desktop
application maintained at <https://github.com/SivanCola/deepzora>. It describes
the reviewed source, including optional features; it does not cover Reasonix
releases or independently operated model services and integrations.

## Local data and access

Deepzora stores configuration, credentials, conversation history, drafts, logs,
memory, indexes and caches on the machine running the application or agent.
Defaults are `~/.deepzora` on macOS/Linux and `%APPDATA%\deepzora` on Windows.
`DEEPZORA_HOME`, `DEEPZORA_STATE_HOME` and `DEEPZORA_CACHE_HOME` can change these
locations; see [Config paths](docs/CONFIG_PATHS.md). Desktop/browser profiles and
OS-managed application data can occupy additional locations.

Provider credentials can be stored in a local `.env` file. File permissions
protect access, but this file is not encrypted by Deepzora. Protect your account,
machine and backups. This project does not automatically import credentials from
legacy applications.

Within granted permissions, the agent can read workspace files, run tools and
commands, and use relevant content as model context. Context can include prompts,
conversation history, file contents or excerpts, attachments and tool results;
it is not limited to files you explicitly attach. Permissions and configured
tools should match the access you intend to grant.

Support files can contain sensitive data. In particular, `deepzora doctor session`
can export the complete, unredacted transcript. Review and redact reports,
screenshots, logs and archives before sharing them.

## Network recipients and purposes

| Feature | Recipient and data | When it connects |
| --- | --- | --- |
| Model requests | Your configured model provider or proxy receives task context and request metadata; auxiliary/evaluator models can receive relevant context too | During tasks that use those models; local model endpoints are also supported |
| Tools and integrations | Configured MCP servers, plugins, hooks, bots, remote agents, shell commands and browser destinations receive the data their operation requires | When enabled/invoked by you or the agent within its permissions |
| Updates and downloads | GitHub and its release/CDN infrastructure receive normal HTTP connection/request information for manifests and downloads; optional component downloads contact their distributors | Supported desktop builds check updates by default; manual checks/downloads also connect |
| Optional diagnostics | A configured collector receives the limited diagnostic payloads described below | Only when a destination and the applicable collection/consent conditions are present |
| Support submissions | The destination you choose, such as a GitHub issue, receives the contents you submit | When you share feedback, reports or files |

The standard update manifest is
<https://github.com/SivanCola/deepzora/releases/latest/download/latest.json>.
Update checks do not upload conversations. Network services can observe an IP
address, requested resource and normal HTTP metadata. A remotely running agent
stores and processes its data on that remote host; local deletion does not remove
remote-host copies. Review remote host and SSH configuration before use.

## Optional diagnostics and controls

The reviewed source has no built-in collector URL. These process environment
variables configure destinations; an unset/empty destination prevents the
corresponding upload:

| Destination variable | Payload and collection controls |
| --- | --- |
| `DEEPZORA_PING_ENDPOINT` | Desktop launch ping: installation ID, version, OS, architecture, channel and bounded platform facts. Controlled by desktop `telemetry`; automatic uploads skip development builds. |
| `DEEPZORA_METRICS_ENDPOINT` | Desktop aggregate feature/preference/lifecycle counters and installation/build/device metadata. Controlled by desktop `metrics`; automatic uploads skip development builds. |
| `DEEPZORA_REPORT_ENDPOINT` | Crash/error stacks and diagnostic breadcrumbs, plus contents of explicitly submitted feedback. Automatic queued desktop crash submission depends on `telemetry`; CLI crash reports are locally reviewed before sending. Explicit desktop report/feedback submission can send to a configured endpoint even with `telemetry` disabled. |
| `DEEPZORA_TELEMETRY_ENDPOINT` | CLI launch/aggregate usage metrics: installation/build/platform information and bounded counters. Requires an eligible release build and enabled CLI telemetry; disabled in detected CI and by CLI opt-out environment variables. |

Desktop `telemetry` and `metrics` default to enabled, but cannot upload without
destinations. Installation IDs persist across launches and are pseudonymous,
not a promise of complete anonymity. Aggregate metrics exclude prompt text, file
contents and API keys. Crash scrubbing attempts to remove tokens, email addresses
and user-specific paths, but may miss sensitive details; review explicit reports.
Feedback can include whatever you enter or attach.

On its first eligible interactive launch with CLI telemetry unconfigured, the
CLI asks for consent and records the choice. `auto` limits eligible collection
to interactive sessions; `on` can enable eligible non-interactive use; `off`
disables it. Desktop preferences are separate from CLI telemetry.

To disable automatic desktop update checks and optional diagnostic collection,
merge these settings into existing sections of `config.toml`:

```toml
[desktop]
check_updates = false
telemetry = false
metrics = false

[telemetry]
cli_metrics = "off"
```

You can also run `deepzora config telemetry off`. Nonempty `DO_NOT_TRACK`, or
`DEEPZORA_TELEMETRY=off` (also `0`, `false`, `no`), overrides CLI metrics. These
environment controls apply to CLI metrics; they do not disable model requests,
desktop features, tools or manually initiated checks/downloads/reports. Unset
collector destination variables before launching if you do not want those
configured destinations available. Disabling telemetry drops unsent automatic
desktop crash reports; explicit report submission remains a separate action.

An operator enabling a custom collector must identify its operator, purposes,
retention, access and transfer arrangements to users. Before distributing an
official release with new collection, the project must update this policy and
provide installation disclosure and a way to disable unexpected collection.

## Retention, deletion and third parties

Local data remains until you delete it or the relevant feature's cleanup/rotation
removes it. There is no single retention period for all histories, logs, caches
and backups. Uninstalling the program does not necessarily delete its user data.
Remove relevant local/remote data directories and backups to delete retained
copies; trash and external backups may retain additional copies. Local deletion
does not request deletion from providers or destinations previously contacted.

Configured providers apply their own retention, security and privacy practices,
including possible processing in other countries. Review the policies for the
actual endpoint you use; a proxy may have different practices from the upstream
model provider. Examples include [DeepSeek](https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html),
[OpenAI](https://openai.com/policies/privacy-policy/),
[Anthropic](https://privacy.anthropic.com/en/) and
[GitHub](https://docs.github.com/en/site-policy/privacy-policies/github-general-privacy-statement).
Deepzora does not promise their retention periods or training exclusions. No
central diagnostic service or fixed collector retention period is represented
by the currently unconfigured collector endpoints.

## Contact and changes

The current project maintainer is [SivanCola](https://github.com/SivanCola).
Use [project issues](https://github.com/SivanCola/deepzora/issues) for nonsensitive
privacy questions. For private security or credential concerns, use the reporting
route in [SECURITY.md](SECURITY.md). Do not include secrets or private transcripts
in public issues. Material data-flow changes must update this policy before the
affected release is distributed; release notes must link to the current policy.
