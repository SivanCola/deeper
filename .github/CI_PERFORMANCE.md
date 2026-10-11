# CI and release execution

[中文](CI_PERFORMANCE.zh-CN.md)

## Desktop PR checks

`scripts/ci-paths.mjs` is the shared path classifier for normal and memory CI.
It distinguishes frontend, Go, generated protocol, Electron, native and
packaging inputs. Explicit documentation such as `desktop/AGENTS.md` skips
build and soak work, while Markdown inside the frontend remains a build input.
Unknown paths and unavailable diffs fail closed. Pull requests use merge-base
diffs; pushes use `before..sha`; normal `main` pushes keep the complete
qualification matrix after the existing release-notes-only exception.

Ordinary CI has eight job definitions and retains all seven required contexts;
see [the CI baseline](../docs/CI_BASELINE.md) for the complete coverage map.
Root and desktop Go suites run once on each of Linux, macOS, and Windows.
The `race` job checks root concurrency owners, and `desktop-go` checks focused
desktop lifecycle races on macOS before merge.

`desktop-frontend` regenerates the desktop host contract (failing on drift) and
produces one `electron/stable` frontend build on Linux. Each artifact carries
a versioned manifest with checkout, producer attempt, variant, build inputs,
toolchain and every `dist` file hash.
Linux, macOS and Windows consumers verify it before compilation or packaging.
Explicit reuse fails on a missing, stale or damaged manifest and never falls
back to a hidden rebuild. Static frontend files are portable; dependencies,
native modules and Electron binaries are not shared. Build-input verification
streams every committed blob through one Git batch process instead of starting
one process per file.

The protected `lint` job checks formatting, repository standards and root Go
platform tags. The protected `desktop` job requires successful frontend and
native matrix results; failed detection and unexpected skips cannot approve a
change. Motion unit tests run once in the frontend plan; the same producer job
runs focused approval and transcript Chromium regressions. Installer packaging,
signed-package startup and native acceptance belong to the release workflows.
Memory screening is a separate manual workflow.

`node desktop/frontend/scripts/run-ci-tests.mjs --list` prints the unit test
plan. It expands the existing dedicated scripts and lifecycle hooks, discovers
new tests, and schedules each TypeScript suite once with its original loader.
Unsupported script syntax and conflicting explicit invocations fail closed.
CI runs two isolated processes at a time; the history performance benchmark
runs alone after them. Local dedicated `pnpm test:*` commands remain available.

## Timing reports

The manual memory workflow summary reports stage execution without queue time,
workflow wall time, recorded job queue time and the sum of runner execution.
Ordinary CI timing is available from the Actions job and step timestamps;
`scripts/ci-timings.mjs` can render an exported run/jobs payload. Compare the
same candidate over three runs and report the median and range so runner
variance is visible. Native Go tests retain non-verbose output rather than
wrapping the process in Go's expensive Windows JSON test-cache output.

## Memory screening

Protocol v4 records the selected screening profile in every manifest, shard and
aggregate. The manually dispatched App memory workflow uses `full`: three
independent processes each complete 128 full, 128 windowed, 128 safety and 512
mixed round trips. Local `short` screening remains available: one process
completes 32 full, 32 windowed, 32 safety and 128 mixed round trips. Ordinary
pull requests and main pushes do not automatically run memory screening.

Both profiles keep the same evidence requirements: exact checkpoints, five heap
snapshots per process, GC, frame settling, source/build identity and screening
thresholds. Aggregation rejects missing shards and profile or protocol mismatches.
Only the explicit mock memory-soak URL removes the fixture's artificial
1.5-second hydration latency. Hydration still crosses an asynchronous timer
task. Default browser and native geometry fixtures retain the delayed path.

The pointer rests outside topic rows and the warmed baseline follows a complete
round trip after layout switching, avoiding samples of temporary menu state.
Reports declare the profile and protocol, and aggregation rejects older protocols.
`timings.json` records host-side counts, total time and maximum time for
navigation, frame settling, GC and heap capture/analysis. Aggregate results label
their `screeningLevel`, so a passing short PR screen cannot be mistaken for full
qualification. A green gate still does not prove offline heap-retainer attribution.

## Signed release artifacts

The successful stable SignPath preflight hands its full native matrix to the
desktop publisher. The publisher revalidates authorization, candidate identity,
signing contract and cache/docs guards, then verifies and publishes the same
signed bytes without rebuilding or signing them again. CLI and npm publication
still wait for the entire preflight; no public surface starts early.

Each platform bundle binds file sizes and SHA-256 hashes to candidate/control
SHAs, version, tag, channel, signing fingerprint, run, invocation and producer
attempt. Missing/extra platforms, conflicting identities, symlinks, duplicate
filenames or modified bytes stop publication. These transport hashes supplement
the existing Authenticode/minisign checks; they are not signature verification.

A failed-job retry may reuse earlier successful platforms from the same run
and invocation. A rebuilt platform replaces only its own fully verified bundle.
New workflow runs prepare a new artifact set. Standalone recovery still builds
and validates its own full matrix. pnpm dependencies are cached by lockfile;
signed release artifacts are transported as artifacts, never dependency caches.
