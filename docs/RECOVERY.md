# Recovery and diagnostics (v1.20+)

Deepzora no longer ships a product `deepzora-guard` recovery shell. Crash
records, pending-update state, and configuration problems do not change the
next launch into a global Safe Mode.

## Prefer these tools

```text
deepzora doctor
deepzora doctor repair
deepzora crash report   # when available in your build
```

- **doctor** inspects configuration, derived desktop state, and common install
  problems without loading the desktop shell.
- **doctor repair** applies safe, explicit repairs the user opts into.
- Crash reports remain opt-in and never force a degraded product mode.

## When a conversation cannot continue

In `transcript gate ... at message N`, `N` is an index in the model request,
not the user's message number. Keep the complete error for diagnosis.

- Invalid tool arguments are recovered using recorded execution evidence.
  Calls that cannot be safely reconstructed become historical records for the
  model. Original arguments, results and chat history stay intact; recovery
  never executes the tools again.
- Missing or damaged local images from earlier turns are marked unavailable;
  text and remaining usable images continue. The model is told to request a
  replacement if needed. A failed image in the current turn must be reattached.
- Invalid requests from optional extensions are skipped. Required extensions
  and explicit blocking decisions still pause the operation with guidance.
- Context preparation has one five-minute generation budget, including queued
  and chunked work. Heartbeats do not extend it. A failed summary keeps the last
  committed context; it no longer triggers additional lossy truncation. If that
  context cannot be sent safely, the current attempt stops with a recoverable
  error. Retry with `/compact`, shorten the latest message, or select a model
  with a larger context window. Original chat history stays available.
- On save failures, keep the conversation open, export a backup if available,
  and check disk space and write permissions. Model requests and tool execution
  remain paused until the required save has been confirmed.

These recovery paths do not require deleting chat history or editing session
files. Derived recovery caches are rebuilt when needed.

## Install layout

Windows and Linux use a versioned install root:

```text
InstallRoot/
  deepzora-launcher[.exe]
  Deepzora.exe                 # Windows portable / Start Menu alias
  deepzora[-cli.exe]
  current.json
  versions/<version>/
    deepzora-desktop[.exe]
    deepzora-cli[.exe]
    deepzora-update-helper[.exe]
```

The thin launcher only reads `current.json` and starts the active desktop. It
never selects a previous version or enters Safe Mode.

## In-app update stuck

If Settings → Updates (or the top banner) reports that the previous update has
not finished (`pending update already exists`, `awaiting startup health`, or
`handoff backup` errors):

1. Click **Discard previous update** in the banner or Settings, then **Retry**.
2. If that button is missing or fails, quit Deepzora fully and start it once so
   startup can commit or retire the probationary transaction, then retry the
   in-app update.
3. If in-app update still fails, download the latest signed installer from the
   official download page and install it **over** the current copy without
   uninstalling first.
4. On macOS, also allow Deepzora under System Settings → Privacy & Security →
   App Management when the dialog appears; a leftover
   `Deepzora.app.deepzora-update-backup` that TCC will not let the app remove
   may still require the official installer path.

If the Windows installer reports `Deepzora layout activation failed`, expand
the installer details and copy the lines under `Deepzora layout activator
output:`. Current installers preserve the activator's concrete error instead of
showing only exit code 1.

## macOS

macOS keeps LaunchServices launching the desktop app bundle directly. Updates
replace the signed `.app` atomically; there is no Guard process.

After the replacement window becomes visible, Deepzora commits only the exact pending transaction captured before launch. Transaction and backup identity must be verified. An unknown backup is preserved rather than trusted as an automatic rollback source.
