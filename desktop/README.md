# Deepzora Desktop (Electron shell)

## macOS 1.38.7 update recovery

Desktop 1.38.7 can report `current executable is not inside a macOS .app bundle`
before it installs a newer build. Quit Deepzora, mount the Apple Silicon, Intel,
or Universal DMG from the official download page, and replace
`/Applications/Deepzora.app`. The application bundle is replaced; settings,
sessions, and other user data remain in their existing user-data directories.
After this one-time full install, verify automatic update by updating the repaired
build to the next candidate. Do not treat the manual replacement itself as an
automatic-update pass.

Model/provider setup: [English guide](../docs/MODEL_SETTINGS.md) · [中文指南](../docs/MODEL_SETTINGS.zh-CN.md).

A native desktop window around the Deepzora Go kernel. The same
transport-agnostic `control.Controller` that backs the chat TUI and the HTTP/SSE
server is driven by the Electron shell through the desktop host protocol — the
Go binary runs as a supervised service (`deepzora-desktop --host-rpc` over
stdio), the shell owns the window, tray, menu and renderer lifecycle. See
[the host protocol](../docs/DESKTOP_HOST_PROTOCOL.md) and
[the architecture contract](../docs/DESKTOP_ARCHITECTURE.md).

```
┌─────────────────────────────────────────────────────────────┐
│  Electron shell (desktop/electron)                           │
│    renderer: bridge.ts ──invoke──▶ window.deepzoraDesktop     │
│    bridge.ts ◀─events── host.on("agent:event")                │
└───────────────▲───────────────────────────┬─────────────────┘
        desktop/invoke (JSON-RPC over stdio) │ desktop/event
┌───────────────┴───────────────────────────▼─────────────────┐
│  desktop/app.go   App (bound)  +  eventSink (event.Sink)     │
│  desktop/host_rpc.go  --host-rpc service, embedded dist      │
└───────────────▲───────────────────────────┬─────────────────┘
       commands │                            │ typed event stream
┌───────────────┴────────────────────────────▼────────────────┐
│  internal/boot.Build → internal/control.Controller (kernel)  │
│  (same assembly the CLI uses: providers, tools, gate, …)     │
└──────────────────────────────────────────────────────────────┘
```

## Why a nested module

`desktop/` is its own Go module (`module deepzora/desktop`, `replace deepzora =>
../`). That keeps the CGO desktop build entirely separate from the CLI's
`CGO_ENABLED=0` single-static-binary guarantee: the parent module's `go build /
vet / test ./...` skip this directory, while the import path stays under
`deepzora/` so it can still import the `deepzora/internal/*` kernel.

## Prerequisites

- Go (matches the parent module).
- Node 24+ and **pnpm 10** (`npm install -g pnpm@10`); `pnpm --dir desktop
  install` pulls the Electron toolchain for the shell.
- No platform webview dependencies: the shell ships its own Chromium.

## Develop

For browser-only UI development with the built-in mock bridge:

```sh
cd desktop
pnpm install          # first run only
pnpm dev
```

For the complete Electron application, including the Go service and Vite dev
server, use the single development entry point:

```sh
cd desktop
pnpm install          # first run only
pnpm dev:desktop
```

For a production-style renderer build instead of the Vite development server,
the equivalent manual sequence remains:

```sh
cd desktop
pnpm install                                   # one workspace: frontend + electron
go build -o build/bin/deepzora-desktop-service .
pnpm --dir frontend build:electron             # rewrites drag regions for Chromium
pnpm --dir electron start                      # launches the shell against the service
```

Frontend-only iteration without the Go side:

```sh
cd desktop/frontend
pnpm install
pnpm dev             # opens in a plain browser; bridge.ts uses the dev mock
```

In a plain browser the native bindings are absent, so `bridge.ts` falls back to a
**mock** that streams a canned turn (text + one `edit_file` tool call) through the
exact same event contract — so layout, streaming, markdown, tool cards, and the
diff seam can all be built without rebuilding Go.

```sh
pnpm --dir electron smoke                      # real-service end-to-end check
```

`go run . -emit-contract frontend/src/generated` regenerates the TypeScript
contract; `go test -run HostContract .` fails when it drifts.

## Test

The desktop package is a nested Go module, so parent `go test ./...` does not run
it. Use the full lane before merging desktop changes, and the short lane for fast
local feedback:

```sh
make desktop-test        # cd desktop && go test .
make desktop-test-short  # skips slow desktop integration/e2e checks
```

To find the next bottleneck, rank individual test cases from the JSON stream:

```sh
make desktop-test-times
# or: cd desktop && go test -count=1 -json . | python3 ../scripts/desktop-test-times.py
```

### Frontend UI review checklist

For anchored menus, dropdowns, tooltips, and other portaled UI, review both the
component code and the CSS positioning contract:

- If a component uses `createPortal` plus `getBoundingClientRect()`, it must
  handle scrollable ancestors, window resize, and `visualViewport` changes.
- Add a focused regression test when changing shared positioning primitives such
  as `AnchoredPopover`, not only the specific menu that exposed the bug.
- Exercise at least one scrollable container path, such as Settings content, when
  manually checking dropdown or popover changes.

## Build

```sh
scripts/desktop-build.sh darwin/arm64 v0.0.0-dev   # one platform per run
```

The script regenerates the host contract (failing on drift), builds the Go
service, and packages the Electron shell through `desktop/packaging/package.mjs`.
Chromium cannot cross-compile the native targets from one host, so releases run
it once per platform on a native runner.

`frontend/dist` is generated by the build (it's git-ignored except for a
`.gitkeep` that keeps the Go `//go:embed all:frontend/dist` compilable on a fresh
checkout). A bare `go build` without a prior `pnpm build` produces a service with
no frontend assets.

### macOS icon acceptance

The macOS SVG, PNG and ICNS use the same inset rounded-square artwork under
`build/darwin/`. Keep its corner silhouette compatible with macOS: excessive
rounding makes macOS 26+ shrink the icon onto a gray compatibility tile.
After changing the SVG, regenerate the PNG and all ICNS sizes, then run the
`TestDarwinICNS` and `TestDarwinDevelopmentIcon` asset tests.

Validate the extracted ZIP or mounted DMG application with the native icon
service, rather than previewing only the source PNG:

```sh
swift desktop/packaging/verify-macos-icon.swift \
  desktop/build/candidate/darwin-arm64/Deepzora.app \
  desktop/build/macos-icon-acceptance/system
```

The check saves the system-rendered Retina icons at 32–1024px and rejects a gray
compatibility tile. Also launch the packaged app with a disposable data home
and inspect its Dock icon; a previously running build can retain its old icon.

### First-launch flow acceptance

The unconfigured welcome uses a single model-connection action with optional
project selection. Successful setup starts a real editable conversation.
Workspace panels default closed only when no saved layout exists; files,
changes and browser remain available before model setup. Explicit layout
choices survive normal restarts and completing model setup.

Run against an extracted ZIP or a mounted DMG with a new evidence directory:

```sh
node desktop/packaging/first-launch-smoke.mjs /path/to/Deepzora.app /tmp/deepzora-first-launch
node desktop/packaging/first-launch-smoke.mjs /path/to/Deepzora.app /tmp/deepzora-first-launch-open --open-before-setup --dark
```

The script isolates config/state/cache from real user data and captures desktop
and compact-window screens. It enters a dummy API Key through the actual catalog,
saves the provider, edits and saves the key again, then uses the pinned completion
footer to start a real session. It checks persisted defaults, runtime readiness,
normal-restart layout restoration, native picker dispatch, one sidebar row per
session, and page errors. The native picker is canceled at Electron's dialog seam;
manually check its visible macOS sheet as well. The default fixture uses an
unreachable local endpoint. `--official-visual` keeps the preset URL for brand
and layout comparison, still using a dummy key; catalog/balance reads may occur,
but the test never sends a conversation or uses real credentials. Neither mode
proves upstream model availability or a successful inference request.

First-time setup completion belongs to the selected connection's editor footer,
not the settings page header. Unsaved edits keep the save action; only the clean,
configured connection may finish setup. Ordinary model-service settings retain
their save/cancel actions and never show the onboarding completion controls.

## Releases & auto-update

Desktop releases ride their own tag namespace, `desktop-v<semver>` (plain `v*`
tags are the CLI release). Pushing one triggers `.github/workflows/release-desktop.yml`,
which builds on a native runner per platform (Electron's Chromium can't
cross-compile), packages each artifact, signs it with minisign, generates a
`latest.json` manifest, publishes a GitHub release that does not claim the repository-wide `Latest`
badge, mirrors everything to R2, and attaches the current desktop manifest to
the matching CLI release for old clients.
The Linux artifact bundles Electron's Chromium and ships a root-owned
`chrome-sandbox` helper in the `.deb`; no system webview is required.

```sh
git tag desktop-v1.1.0 && git push origin desktop-v1.1.0
```

The app checks `latest.json` attached to the latest stable release in
[SivanCola/deepzora](https://github.com/SivanCola/deepzora/releases) and shows an
update banner when a newer version is published; **Settings → Software update**
has a manual check. Stable promotion attaches the verified Desktop pointer to
the CLI release before advancing GitHub latest. Self-update behavior by platform:

- **Linux portable (`.tar.gz`)** — download, verify the minisign signature, replace
  the binaries in the install directory, and relaunch through Guard. No elevation.
- **Linux Debian/Ubuntu (`.deb`)** — download the signed `.deb`, request administrator
  authorization via Polkit (`pkexec`), re-verify and install with `apt-get
  --only-upgrade`, then relaunch through Guard. The first build that ships the
  update helper and Polkit policy is a one-time bootstrap: existing `.deb` users
  should overwrite-install once with
  `sudo apt install ./Deepzora-VERSION-Linux-x64.deb` (replace `VERSION` with the
  downloaded version; v0.0.1 uses `Deepzora-linux-amd64.deb`; no uninstall required). After
  that, in-app authorized updates work. If Polkit/`pkexec` is unavailable, use
  the same manual command. Failed installs leave the running app intact so you
  can retry; successful installs are managed by apt/dpkg and are not auto-downgraded.
- **Windows** — download, verify the minisign signature, then run the per-user
  NSIS installer (no admin rights needed).
- **macOS** — Developer ID signed and notarized release builds update in place.
  Local and fork builds use ad-hoc signing and remain manual-only because
  Gatekeeper cannot authorize their replacement bundle.

### Code signing — first launch

- **Windows** — v0.0.1 is temporarily distributed without a Deepzora Authenticode
  publisher signature and may show unknown-publisher or SmartScreen warnings.
  Download/payload Minisign integrity verification and native installer acceptance
  remain required. Later stable versions require trusted Authenticode signing;
  the exception is limited to v0.0.1. See the
  [Code signing policy](../CODE_SIGNING_POLICY.md). Even signed new versions may
  show SmartScreen while publisher/download reputation develops.
- **macOS** — official release builds are signed and notarized. Choose the Apple
  Silicon or Intel DMG for the smallest download, or the Universal DMG when the
  CPU architecture is unknown. Local ad-hoc builds may still require clearing the
  quarantine attribute when Gatekeeper reports the app "is damaged" or is from an
  unidentified developer:
  ```sh
  xattr -dr com.apple.quarantine /Applications/Deepzora.app
  ```
  The release workflow's `HAS_APPLE_CERT` gate controls the signed, notarized,
  self-updating path.

### Verifying a download

Artifacts are signed with Deepzora's own [minisign public key](internal/update/deepzora.pub),
embedded in the application and update helpers. CI uses the matching
`MINISIGN_PUBLIC_KEY`; see [release setup](../docs/DEEPZORA_INDEPENDENCE.md). The `.minisig`
signature sits next to each artifact in the release; verify with the
[minisign](https://jedisct1.github.io/minisign/) CLI:

```sh
minisign -Vm Deepzora-VERSION-macOS-arm64.zip \
  -p desktop/internal/update/deepzora.pub
```

Replace `VERSION` with the downloaded version. v0.0.1 retains its original
`Deepzora-darwin-arm64.zip` name. Later releases provide versioned downloads and
legacy aliases for v0.0.1 automatic updates; use the release's download table.

## Editor seams and workspace file previews

Code and diff rendering go through two components with stable prop contracts and
lazy boundaries, so heavier viewers stay out of the initial bundle. `CodeViewer`
keeps the compact highlighted viewer for chat, Markdown, and tool output, while
workspace file previews opt into the searchable line-number viewer:

| Component | Props | Default impl | Upgrade |
|---|---|---|---|
| `components/CodeViewer.tsx` | `EditorProps` | `editors/HljsCode.tsx`; `editors/LineNumberCode.tsx` when `showLineNumbers` is enabled | extend the implementation selection for Monaco or CodeMirror |
| `components/DiffView.tsx` | `DiffProps` | `editors/HljsDiff.tsx` (highlighted LCS/unified diff) | swap for `editors/MonacoDiff` or `editors/CodeMirrorMerge` |

```sh
# Monaco
pnpm add @monaco-editor/react monaco-editor
# or CodeMirror 6
pnpm add @uiw/react-codemirror @codemirror/lang-javascript @codemirror/merge
```

Then add `editors/MonacoCode.tsx` (default-export a component taking
`EditorProps`) and update the implementation selection in `CodeViewer.tsx`.
`ToolCard` already routes `edit_file` calls' `old_string`/`new_string` through
`DiffView`, and `Markdown` routes fenced code blocks through `CodeViewer`, so
both seams light up everywhere at once.

`WorkspacePanel` passes `showLineNumbers` for text-file previews. The resulting
viewer provides a line-number gutter, viewer-scoped Ctrl/Cmd+F search with case
and whole-word options, copy support, and virtualized rendering above 100 lines.
Search marks are applied only to visible rows so query input does not rebuild the
entire highlighted document. Files above 512 KiB or 20,000 lines keep line
numbers, search, copy, and virtualization but use escaped plain text instead of
syntax highlighting. Workspace files are previewed up to 2 MiB; larger files
display the first 2 MiB with a localized truncation notice.

## Multi-platform adaptation

One Chromium runtime (Electron) serves every OS, so the remaining platform work
is native-shell behavior, not per-engine rendering quirks:

- **Linux** — the shell runs with the Chromium sandbox (the `.deb` ships a
  root-owned 4755 `chrome-sandbox`, never `--no-sandbox`). Close-to-background is
  enabled only after a DBus health probe confirms a live StatusNotifierWatcher,
  a registered visual host, and this app's registered StatusNotifierItem. If any
  of them disappears while the main window is hidden, Deepzora presents the
  window again until the tray recovers.
- **Windows** — the shell follows the OS light/dark setting. Remote Markdown
  images are fetched by the Go backend with the configured proxy and re-served
  from the local asset origin, so embedded images never bypass the configured
  proxy. Image hosts must resolve locally to public addresses; direct,
  HTTP(S)-proxy, and SOCKS-proxy connections are pinned to those vetted IPs
  while preserving the original Host and TLS SNI.
- **macOS** — inset/hidden title bar; the CSS marks the top bar as an OS drag
  region (the Electron build rewrites `--deepzora-draggable` to
  `-webkit-app-region`) and leaves room for the traffic lights.
- **Renderer recovery** — the shell reloads a crashed renderer
  (`render-process-gone`) and reports service state to the UI; a renderer that
  never reports ready is presented anyway with a diagnostics trail instead of
  staying hidden.
- **Theming** — colors are CSS variables gated on `prefers-color-scheme`, so the
  UI follows the OS theme without native glue.
- **Fonts / offline** — system font stack only; no web-font fetches, so first paint
  is instant and identical offline.
- **First paint** — the window background is set to the dark shell color so there's
  no white flash before CSS loads.

## Files

```
desktop/
  main.go            service entry: host launch modes, shell bootstrap
  host_rpc.go        --host-rpc service over stdio + -emit-contract
  app.go             App (bound command surface) + eventSink (event.Sink)
  wire.go            event.Event → JSON wire form (mirrors internal/serve/wire.go)
  electron/          Electron shell (main process, preload, browser surface)
  packaging/         @electron/packager pipeline + packaged smoke test
  frontend/
    src/
      lib/
        types.ts         wire contract (mirrors wire.go)
        bridge.ts        desktop host bridge + browser dev mock
        desktopHost.ts   the only module touching window.deepzoraDesktop
        useController.ts event-stream reducer + command surface (the hook)
      components/
        Transcript, Message, ToolCard, Composer, ApprovalModal, ContextGauge,
        Markdown, CodeViewer, DiffView
        editors/  PlainCode, PlainDiff   ← editor seam impls (swap targets)
```

## Telemetry

The desktop app can send one anonymous ping per launch when the operator sets
`DEEPZORA_PING_ENDPOINT`; no collector is configured by default. The ping contains:
a random anonymous install id (generated locally and not an account id), app
version, OS, architecture, Windows build/revision or bounded Linux
distribution/kernel/session facts, and the renderer engine tag. When the
previous process ended abnormally, the next normal launch may also send a
bounded native diagnostic (lifecycle phase, symbolized stack, window failure
kind, and coarse device facts). Reports queued by the retired WebView2/WebKitGTK
shell still decode and forward unchanged after upgrade.
Panic values are removed and paths/secrets are scrubbed before the report is
queued. The install id is attached only while sending and is not stored in a
pending crash file. It never includes conversations, account data, API keys,
file contents, usernames, hostnames, GPU driver details, or full local paths.

Opt out any time: Settings > Updates > "Anonymous usage ping", or set
`telemetry = false` under `[desktop]` in the global config. Dev builds
never ping or upload queued native diagnostics. Frontend crash and
performance-pressure reports remain separate and are sent only when the user
clicks "Send report" on the diagnostic UI.

Aggregate quality metrics are also enabled by default and can be disabled from
Settings > Updates > "Share aggregate quality metrics", or by setting
`metrics = false` under `[desktop]`. These metrics are anonymous signal/bucket
counts, lifecycle/window failure buckets, and preference buckets; they never
include conversations, prompts, keys, paths, base URLs, or file contents.
