<p align="center">
  <img src="desktop/branding/logo-ribbon.png" alt="Deepzora orange ribbon logo" width="112" />
</p>

<h1 align="center">Deepzora</h1>

<p align="center">
  <strong>An independent derivative of Reasonix 1.x — think of it as Reasonix Mini.</strong>
</p>

<p align="center">
  AI coding in your desktop, terminal and remote workspace.<br />
  Read code, make changes and run tests with the model you choose.
</p>

<p align="center">
  <strong>English</strong> · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/SivanCola/deepzora/releases"><img src="https://img.shields.io/github/v/release/SivanCola/deepzora?style=flat-square&amp;color=f97316&amp;label=release" alt="Latest release" /></a>
  <a href="https://www.npmjs.com/package/@sivancola/deepzora"><img src="https://img.shields.io/npm/v/%40sivancola%2Fdeepzora?style=flat-square&amp;color=ea580c&amp;logo=npm&amp;logoColor=white" alt="npm version" /></a>
  <a href="https://github.com/SivanCola/deepzora/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/SivanCola/deepzora/ci.yml?branch=main&amp;style=flat-square&amp;label=CI" alt="Main branch CI" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0--only-64748b?style=flat-square" alt="License: AGPL-3.0-only" /></a>
</p>

<p align="center">
  <a href="#quick-start"><strong>Get started</strong></a> ·
  <a href="#desktop-downloads">Desktop downloads</a> ·
  <a href="docs/GUIDE.md">Guide</a> ·
  <a href="#documentation">Documentation</a> ·
  <a href="https://github.com/SivanCola/deepzora/issues">Report an issue</a>
</p>

---

## Reasonix 1.x, with an independent home

Deepzora is derived from the **Reasonix 1.x series** (`main-v2`), with its own brand, maintenance and release channels. **Reasonix Mini** is a way to understand its positioning, not a separate official Reasonix edition. It does not belong to the Reasonix Studio 2.x release line.

Describe a task, choose a project, and let the agent read files, edit code and run commands and tests. Use the Electron desktop app, work directly in the terminal, or connect to a remote workspace through SSH. DeepSeek presets and custom OpenAI-compatible or Anthropic-compatible providers let you choose the model connection.

The agent runs on your computer or the remote host you select. Your configured model provider receives the context used for requests; see the [privacy policy](PRIVACY.md) for storage, network access and optional diagnostics.

## What you can do

| Code with context | Stay in control | Extend your workflow |
| --- | --- | --- |
| Read a repository, edit files, run commands and tests, and inspect changes and check results. | Preview a plan before implementation, choose tool permissions, and rewind supported edit-tool changes by conversation turn. | Connect MCP tools, add skills and extensions, and integrate an ACP editor or the Go extension SDK. |
| [Turn results](docs/TURN_RESULTS.md) | [Permissions](docs/TOOL_APPROVAL_MODES.md) · [Checkpoints](docs/CHECKPOINTS.md) | [Extensions](docs/EXTENSIONS.md) · [Go SDK](sdk/go/README.md) |

Checkpoints cover supported file-edit tools; shell-command side effects are outside rewind coverage.

## Choose your workspace

| Interface | Best for | Entry point |
| --- | --- | --- |
| **Desktop** | A visual workspace for conversations, files, changes and tools | [Download the Electron app](#desktop-downloads) |
| **Terminal** | Interactive coding and scripted tasks | `deepzora` · `deepzora run "task"` |
| **Browser & remote** | A browser session or code on a remote SSH host | `deepzora web` · [Remote sessions](docs/REMOTE_SESSIONS.md) |
| **Editor** | Connecting an ACP-compatible editor to the agent | `deepzora acp` · [ACP guide](docs/ACP.md) |

## Quick start

### Desktop

1. [Download Deepzora](#desktop-downloads) for your system and install it.
2. Open the app and connect your model provider and API key.
3. Choose a project folder and describe a task, for example: **“Add retry handling to the HTTP client and run its tests.”**
4. Review the changes and test results; respond to approval requests when they appear.

### Terminal

Install the [published npm package](https://www.npmjs.com/package/@sivancola/deepzora), then configure your model:

```sh
npm install -g @sivancola/deepzora
deepzora setup
cd /path/to/your/project
deepzora
```

For a single task or a script:

```sh
deepzora run "Fix the failing tests and explain the changes"
deepzora -p "Summarize this repository" --output-format json
```

Native CLI archives are also available in the [latest CLI release](https://github.com/SivanCola/deepzora/releases/latest). See the [CLI reference](docs/CLI.md) for session resume, model selection and automation.

## Desktop downloads

**[Download Deepzora Desktop →](https://github.com/SivanCola/deepzora/releases?q=%22Deepzora%20Desktop%22&expanded=true)**

Choose the package for your operating system and architecture on the release page. Available packages, installation notes and version-specific signing status are listed with each release; see also the [code signing policy](CODE_SIGNING_POLICY.md).

For supported data formats and compatibility, see the [current baseline](docs/current-version-baseline.md).

## Documentation

| Start here | Customize & connect | Develop & contribute |
| --- | --- | --- |
| [User guide](docs/GUIDE.md) | [Model settings](docs/MODEL_SETTINGS.md) | [Contributing](CONTRIBUTING.md) |
| [CLI reference](docs/CLI.md) | [Configuration paths](docs/CONFIG_PATHS.md) | [Architecture & spec](docs/SPEC.md) |
| [Plans & goals](docs/COLLABORATION_MODES.md) | [Remote sessions](docs/REMOTE_SESSIONS.md) | [Desktop development](desktop/README.md) |
| [Permissions & sandbox](docs/TOOL_APPROVAL_MODES.md) | [ACP editor integration](docs/ACP.md) | [Go extension SDK](sdk/go/README.md) |
| [Checkpoints & rewind](docs/CHECKPOINTS.md) | [Extensions & MCP](docs/EXTENSIONS.md) | [Release candidates](docs/RELEASE_CANDIDATES.md) |
| [Context & memory](docs/SESSION_MEMORY_RETRIEVAL.md) | [Theme packs](docs/THEME_PACK.md) | [Optional feedback backend (中文)](supabase/README.md) |

## Build from source

Use the Go toolchain pinned in [go.mod](go.mod), Node from [.node-version](.node-version), and pnpm from [desktop/package.json](desktop/package.json).

```sh
git clone https://github.com/SivanCola/deepzora.git
cd deepzora
go build -o deepzora ./cmd/deepzora
./deepzora setup
./deepzora
```

For desktop development, install the pnpm version declared by the workspace, install dependencies and start the app:

```sh
cd desktop
npm install -g "$(node --print 'require("./package.json").packageManager')"
pnpm install --frozen-lockfile
pnpm dev:desktop
```

For production builds and native packaging, follow the [desktop build guide](desktop/README.md#build). Validation commands are maintained in the [contribution guide](CONTRIBUTING.md#development-workflow) and [desktop test guide](desktop/README.md#test).

## Origin, license & policies

Deepzora builds on the **Reasonix 1.x series** of [DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix/tree/main-v2). Thanks to the Reasonix authors and contributors for the foundation. Upstream authorship and source provenance are preserved in [NOTICE](NOTICE); the original MIT license is preserved in [LICENSE-MIT](LICENSE-MIT). Deepzora has independent release and update channels.

The combined work is distributed under [AGPL-3.0-only](LICENSE). Commercial use and independent forks are allowed subject to the applicable licenses, including Corresponding Source obligations for distribution and modified network services. Inherited MIT permissions and explicitly licensed components remain in effect; see [Licensing](LICENSING.md). The separate [brand policy](TRADEMARK_POLICY.md) does not restrict rights granted by the software licenses.

[Privacy](PRIVACY.md) · [Code signing](CODE_SIGNING_POLICY.md) · [SignPath review materials](docs/SIGNPATH_REVIEW.md) · [Security](SECURITY.md)

---

<p align="center">
  <sub>Built on Reasonix 1.x. Independently maintained as Deepzora.</sub>
</p>
