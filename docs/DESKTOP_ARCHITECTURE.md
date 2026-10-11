# Desktop architecture

[简体中文](DESKTOP_ARCHITECTURE.zh-CN.md)

Deepzora Desktop consists of a React UI, an Electron host, and a Go desktop service. These processes ship as one version unit. Electron owns native windows and browser views; Go owns business state and runtime execution.

```text
React UI → typed preload IPC → Electron main → private stdio JSON-RPC → Go service
                                   │                              │
                                   ├─ browser views               ├─ controllers, sessions
                                   ├─ windows, tray, dialogs      ├─ leases, approvals, recovery
                                   └─ clipboard, notifications    └─ SSH, settings, extensions
```

| Layer | Responsibility |
| --- | --- |
| React | Render state projections and submit user intent through the typed bridge |
| Electron | Native lifecycle, browser views, menus, tray, dialogs, clipboard, notifications |
| Go service | Commands, controller ownership, sessions, approvals, settings, terminals, SSH and update coordination |
| Shared Go kernel | Providers, tools, persistence, leases, recovery and billing |
| Remote adapter | Forward session-authorized host capabilities through the managed SSH connection |

The reflected `DesktopContract` generates the command table, DTO declarations and handshake digest. Events carry sequence, generation, name and arguments. Go delegates native operations through `NativeHost`; browser tools use the host capability registry and per-session grants. See [host protocol](DESKTOP_HOST_PROTOCOL.md), [browser contract](DESKTOP_BROWSER.md), and [generated inventory](desktop-contract/INVENTORY.md).

Unsent session input stays separate from committed transcripts. Runtime and attachment mutations are scoped to the originating session and revision. Late work from replaced tabs cannot acquire the current session's authority. Transcript rendering retains its single viewport writer and bounded history window; see [scroll contract](TRANSCRIPT_SCROLL_CONTRACT.md).

## Verification

Build and test both Go modules, regenerate the host contract, run frontend and Electron tests, and verify the generated inventory. Browser suites exercise new-session creation, input recovery, approvals and transcript navigation. Release candidates additionally require native signing and acceptance checks on each supported platform; local browser tests or cross-compilation do not establish native platform qualification.

Use [Contributing](../CONTRIBUTING.md) for development commands and [release candidates](RELEASE_CANDIDATES.md) for publication gates. The first-release baseline supports only the current Electron host and current wire/storage contracts.
