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

Ordinary CI has ten validation jobs plus a non-gating timing collector and retains all seven required contexts;
see [the CI baseline](../docs/CI_BASELINE.md) for the complete coverage map.
Root Go routing is unchanged. Desktop Go suites run on Linux, macOS, and Windows
for Go, host bridges, generated contracts, Electron, packaging and toolchain inputs.
Ordinary frontend components, styles, resources and tests skip native Go suites.
Unknown paths select all checks; renames inspect both old and new paths.
The `race` job checks root concurrency owners, and `desktop-go` checks focused
desktop lifecycle races on macOS before merge.

The full Windows desktop suite has a 35-minute test-process deadline and a
65-minute job budget for cold compilation, vet, lint and cache work. A 25-minute
suite deadline has expired while an ordinary test had run for only three seconds;
this budget covers the complete suite without skipping tests. Linux and macOS
retain their 15-minute test-process deadlines and 35-minute job budgets.

On PRs requiring native validation, Windows runs two independently compiled
main-package test shards. A deterministic SHA-256 assignment discovers every
top-level Test, Example and Fuzz seed entry; subtests stay with their parent.
Only shard one runs other desktop packages, vet, build and lint. Both restore
the existing cache without saving. Main and full manual CI retain whole suites.
The desktop gate verifies exactly two reports, source/run/toolchain identity,
matching inventories, disjoint assignments, complete terminal results and other
package coverage. Successful prior attempts can be reused within the same run;
missing evidence never passes. Reports and raw logs are retained for 14 days.

The runner compiles once per shard and splits long selectors below 12,000 UTF-16
units. All batches share one 35-minute test budget; cancellation stops the child
process tree. No global parallel-test flag or retry-to-green is introduced.
Run `node scripts/ci-go-shard.mjs --index 1 --count 2 --output /tmp/shard-1` locally
and repeat with index 2 to reproduce. `--count 1` executes the full main package
in one process, including its normal shared state, for comparison.

`CI Windows benchmark` is a manual, read-only-cache workflow with three full
runs and three shard pairs on one checkout SHA, at most three concurrent jobs.
It verifies coverage and skip outcomes, reports medians/ranges and execution
cost, and does not change repository settings or enable sharding elsewhere.
Keep its candidate PR in draft until results meet the 35% test-time reduction
target with no more than 25% additional compile/test/cleanup time; use Actions
job timings separately to evaluate total runner cost and queue effects.
Rollback PR sharding by restoring whole-suite Windows execution and removing
its report requirement together; keep routing and frontend concurrency intact.

`desktop-frontend` regenerates the desktop host contract (failing on drift) and
produces one `electron/stable` frontend build on Linux. `desktop-tests` runs unit,
Electron and packaging tests independently. `desktop-browser` consumes the build
and verifies its SHA, run, producer attempt, inputs, toolchain and byte hashes
before Chromium checks. Artifacts last seven days; expired artifacts require
rerunning the producer. The Go service does not embed the frontend and starts
independently without downloading it. Release artifact verification is unchanged.

The protected `lint` job checks formatting, repository standards and root Go
platform tags. The protected `desktop` job requires successful frontend and
native matrix results according to a versioned execution plan; failed detection,
invalid plans and unexpected skips cannot approve a change. Motion unit tests
run once; the browser job retains approval and transcript Chromium regressions.
The transcript harness builds its own entrypoint separately. Installer packaging,
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
Ordinary CI uploads raw run/jobs metadata, desktop logs and timing reports for
14 days. Collection failure does not override test results. Missing queue data
is unknown, not zero; known dependency waiting is separated from queue time.
Go JSON logs report top-level tests separately from subtests to avoid double counting.

Run `node scripts/ci-collect.mjs --repository SivanCola/deepzora --run RUN_ID --output /tmp/ci-evidence`
to collect an existing run. `scripts/ci-timings.mjs` also accepts exported run/jobs
JSON. Dispatch `ci.yml` three times on an unchanged candidate branch for full
validation with read-only caches; dispatches have independent concurrency groups.
Record the exact SHA, event, attempt, runner image and cache restores for each
sample. Compare medians and ranges, separating cold caches and infrastructure
failures; never attribute improvements already present on the baseline to a new change.

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

The successful candidate signing preflight hands its full native matrix to the
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
