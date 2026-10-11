# CodeQL: review and remediation of the 85 rename findings

## Scope / 范围

The 85 annotations on rename PR #13 map to 85 distinct alerts on the main
baseline `a83f18d40070fe6d74498c2ea85bd64108028e7c`. The repository has 241
open alerts at this baseline; this review targets these 85, not a claim that
every repository alert has been resolved. Scanning and query coverage remain
enabled without exclusions or severity reductions.

本次逐条核对改名报告中的 85 条告警，并修复共享入口；没有把“历史遗留”
作为不处理的理由。其他告警不属于此报告，也不宣称仓库安全告警清零。

## Mechanisms and verdicts / 机制与判定

- **78 path findings (PARTIAL):** legacy HTTP traces pass through
  `serve.Server.resolveSessionPath`. It already resolves symlinks and checks
  directory containment, but returns the original path separately from the
  checked relative value. Rebuild from the trusted physical session root and
  a `filepath.IsLocal`-checked relative path instead. This also uses the OS
  rule for reserved device names. Reject outside paths, sibling-prefix paths,
  root/non-transcript paths and outside symlinks; preserve nested paths and
  contained symlink aliases. Case-folded comparison keys establish containment;
  the rebuilt suffix retains physical component spelling rather than using
  comparison keys as access paths. Additional identity-status traces reach the
  automatic mirror-reclaim sweep. It previously probed an identity registry key
  as a legacy filesystem path and trusted old legacy mirror paths without
  checking the current root again. Resolve targets before probing/reclaiming:
  canonical identities use their session-service writer directory; legacy paths
  pass the current containment guard. Tests prove live canonical writers stop
  reclaim and stale outside-root mirrors never reach a filesystem probe.
  The operation does not add a concurrent-filesystem
  mutation guarantee to path-based persistence APIs.
- **#238 allocation arithmetic (CONFIRMED):** `len(env)+1` is unnecessary
  unchecked arithmetic in a capacity expression. Allocate capacity
  `len(env)`; Go's checked append grows only when a new key is needed.
  Existing environment filtering and override tests retain behavior.
- **#1 shell injection (REJECTED as injection, adjacent fix confirmed):**
  the original shell program is fixed and paths are quoted positional
  arguments. Still remove the shell entirely and stream argv-only
  `dpkg-deb` to `tar`, checking both exit statuses and bounding member bytes.
  The old shell pipeline checked only the last process. Tests cover literal
  metacharacters, failed producers/consumers, missing programs and byte limits.
- **#234, #235, #236 logging (PARTIAL):** the reported sources include
  `APIKeyEnv`, a variable name, not the resolved credential. Raw configuration
  or rebuild errors can nevertheless contain pasted credentials. Apply the
  existing `secrets.RedactError` at those log boundaries and the related startup
  log/user warning. Invalid credential-variable validation no longer echoes
  the rejected value. Provider setup tests inject a synthetic credential into a
  rebuild error and assert it is absent from logs and HTTP output.
- **#237 doctor logging (REJECTED):** the trace merges ignored provider data
  into ignored project declarations. `configWarnings` selects only sandbox/
  permission declarations and runs `secrets.RedactCredentials` before both
  text and JSON output. A regression includes a synthetic credential inside
  an ignored permission declaration and checks both renderings.
- **#2 image SSRF (REJECTED):** the production custom RoundTripper resolves
  each initial and redirect host, rejects any non-public address, then dials
  only those vetted IPs through a fixed direct/proxy route. No unguarded
  hostname re-resolution occurs. URL credentials, unsafe schemes and private
  literal hosts are rejected; redirects and bytes are bounded. Existing tests
  exercise private DNS answers, mixed answers, fixed direct dials and HTTP/
  SOCKS proxy IP pinning. Added real-RoundTripper redirect tests also cover
  public redirects, private redirect DNS, same-host DNS rebinding and private
  literal redirects, asserting unsafe destinations never reach the dialer.
  The generic `client.Do` sink does not model that
  custom transport. A test-injected client factory is not a production route.

路径问题在共享入口表达并验证边界；日志修复不修改模型可见的会话内容或提示词。
误报只在完成证据核对后按具体告警记录处理，不采用目录排除或关闭规则。

## Verification / 验证

Focused containment, provider setup, configuration, doctor, secrets, environment
and desktop image/rebuild tests; complete packaging tests; root/desktop lint and
repository lint. Full root tests and exact-head hosted CI/CodeQL provide the
final publication evidence. Alert states are checked again after main scanning.

CodeQL guidance: [path injection](https://codeql.github.com/codeql-query-help/go/go-path-injection/),
[clear-text logging](https://codeql.github.com/codeql-query-help/go/go-clear-text-logging/).

## Per-finding ledger / 逐条记录

Line numbers below are the pre-fix locations recorded by PR #13, not current
diff lines. Alert IDs remain stable for final-state readback.

| Finding | Alert | Baseline location | Handling |
| --- | --- | --- | --- |
| F1 | #1 | `desktop/packaging/verify.mjs:60` | Shell removed; producer status verified |
| F2 | #238 | `internal/tool/builtin/bash.go:702` | Unsafe capacity arithmetic removed |
| F3 | #161 | `internal/agent/session_persistence_context.go:74` | Containment guard clarified |
| F4 | #160 | `internal/agent/session_persistence_context.go:71` | Containment guard clarified |
| F5 | #159 | `internal/agent/session_path_identity.go:53` | Containment guard clarified |
| F6 | #48 | `internal/agent/session_lock_unix.go:114` | Containment guard clarified |
| F7 | #47 | `internal/agent/session_lock_unix.go:43` | Containment guard clarified |
| F8 | #158 | `internal/agent/session_lock_unix.go:17` | Containment guard clarified |
| F9 | #46 | `internal/agent/session_load.go:104` | Containment guard clarified |
| F10 | #157 | `internal/agent/session_lease.go:322` | Containment guard clarified |
| F11 | #156 | `internal/agent/session_lease.go:304` | Containment guard clarified |
| F12 | #44 | `internal/agent/session_events_probe.go:67` | Containment guard clarified |
| F13 | #43 | `internal/agent/session_events_probe.go:53` | Containment guard clarified |
| F14 | #42 | `internal/agent/session_events.go:598` | Containment guard clarified |
| F15 | #155 | `internal/agent/session_events.go:593` | Containment guard clarified |
| F16 | #41 | `internal/agent/session_events.go:481` | Containment guard clarified |
| F17 | #154 | `internal/agent/session_events.go:466` | Containment guard clarified |
| F18 | #153 | `internal/agent/session_events.go:439` | Containment guard clarified |
| F19 | #40 | `internal/agent/session_events.go:431` | Containment guard clarified |
| F20 | #39 | `internal/agent/session_events.go:407` | Containment guard clarified |
| F21 | #38 | `internal/agent/session_events.go:399` | Containment guard clarified |
| F22 | #37 | `internal/agent/session_events.go:367` | Containment guard clarified |
| F23 | #16 | `internal/agent/session_dag_rotate.go:90` | Containment guard clarified |
| F24 | #236 | `internal/serve/provider_setup.go:154` | Error log redaction enforced |
| F25 | #56 | `internal/control/controller.go:3272` | Containment guard clarified |
| F26 | #237 | `internal/cli/doctor.go:91` | False positive: narrowed and redacted diagnostics |
| F27 | #55 | `internal/checkpoint/transaction.go:1481` | Containment guard clarified |
| F28 | #54 | `internal/checkpoint/transaction.go:1174` | Containment guard clarified |
| F29 | #52 | `internal/checkpoint/load.go:32` | Containment guard clarified |
| F30 | #172 | `internal/checkpoint/layout_v3.go:156` | Containment guard clarified |
| F31 | #171 | `internal/checkpoint/layout_v3.go:139` | Containment guard clarified |
| F32 | #170 | `internal/checkpoint/layout_v3.go:124` | Containment guard clarified |
| F33 | #53 | `internal/checkpoint/layout_v3.go:115` | Containment guard clarified |
| F34 | #169 | `internal/checkpoint/layout_v3.go:108` | Containment guard clarified |
| F35 | #51 | `internal/checkpoint/layout_v3.go:88` | Containment guard clarified |
| F36 | #168 | `internal/checkpoint/layout_v3.go:68` | Containment guard clarified |
| F37 | #167 | `internal/checkpoint/layout_v3.go:59` | Containment guard clarified |
| F38 | #174 | `internal/checkpoint/checkpoint.go:639` | Containment guard clarified |
| F39 | #173 | `internal/checkpoint/checkpoint.go:541` | Containment guard clarified |
| F40 | #165 | `internal/checkpoint/blob.go:68` | Containment guard clarified |
| F41 | #49 | `internal/checkpoint/atomic_json.go:26` | Containment guard clarified |
| F42 | #164 | `internal/checkpoint/atomic_json.go:18` | Containment guard clarified |
| F43 | #163 | `internal/agent/session_persistence_context.go:148` | Containment guard clarified |
| F44 | #162 | `internal/agent/session_persistence_context.go:145` | Containment guard clarified |
| F45 | #151 | `internal/agent/session_dag_replay.go:126` | Containment guard clarified |
| F46 | #15 | `internal/agent/session_dag_replay.go:108` | Containment guard clarified |
| F47 | #150 | `internal/agent/save_writer_tail.go:51` | Containment guard clarified |
| F48 | #13 | `internal/agent/save_recovery_helpers.go:172` | Containment guard clarified |
| F49 | #149 | `internal/agent/save_recovery_helpers.go:72` | Containment guard clarified |
| F50 | #148 | `internal/agent/save_persist.go:57` | Containment guard clarified |
| F51 | #11 | `internal/agent/save_dag_shutdown.go:104` | Containment guard clarified |
| F52 | #10 | `internal/agent/save_dag.go:202` | Containment guard clarified |
| F53 | #9 | `internal/agent/save_dag.go:176` | Containment guard clarified |
| F54 | #12 | `internal/agent/save.go:1511` | Containment guard clarified |
| F55 | #147 | `internal/agent/save.go:239` | Containment guard clarified |
| F56 | #8 | `internal/agent/projection.go:248` | Containment guard clarified |
| F57 | #7 | `internal/agent/projection.go:185` | Containment guard clarified |
| F58 | #6 | `internal/agent/branch.go:576` | Containment guard clarified |
| F59 | #5 | `internal/agent/branch.go:415` | Containment guard clarified |
| F60 | #146 | `internal/agent/branch.go:342` | Containment guard clarified |
| F61 | #4 | `internal/agent/branch.go:333` | Containment guard clarified |
| F62 | #235 | `desktop/settings_app.go:1481` | Error log redaction enforced |
| F63 | #2 | `desktop/remote_markdown_image.go:247` | False positive: DNS/IP/redirect guard |
| F64 | #234 | `desktop/deferred_rebuild.go:296` | Error log redaction enforced |
| F65 | #36 | `internal/agent/session_events.go:223` | Containment guard clarified |
| F66 | #35 | `internal/agent/session_events.go:164` | Containment guard clarified |
| F67 | #34 | `internal/agent/session_display_read_model.go:146` | Containment guard clarified |
| F68 | #33 | `internal/agent/session_display_read_model.go:139` | Containment guard clarified |
| F69 | #32 | `internal/agent/session_display_read_model.go:126` | Containment guard clarified |
| F70 | #31 | `internal/agent/session_display_read_model.go:119` | Containment guard clarified |
| F71 | #30 | `internal/agent/session_display_read_model.go:114` | Containment guard clarified |
| F72 | #29 | `internal/agent/session_display_read_model.go:110` | Containment guard clarified |
| F73 | #28 | `internal/agent/session_dag_writer.go:148` | Containment guard clarified |
| F74 | #27 | `internal/agent/session_dag_writer.go:144` | Containment guard clarified |
| F75 | #26 | `internal/agent/session_dag_writer.go:133` | Containment guard clarified |
| F76 | #25 | `internal/agent/session_dag_writer.go:107` | Containment guard clarified |
| F77 | #24 | `internal/agent/session_dag_writer.go:74` | Containment guard clarified |
| F78 | #152 | `internal/agent/session_dag_writer.go:67` | Containment guard clarified |
| F79 | #23 | `internal/agent/session_dag_writer.go:60` | Containment guard clarified |
| F80 | #22 | `internal/agent/session_dag_unlocked.go:159` | Containment guard clarified |
| F81 | #21 | `internal/agent/session_dag_unlocked.go:119` | Containment guard clarified |
| F82 | #20 | `internal/agent/session_dag_unlocked.go:94` | Containment guard clarified |
| F83 | #19 | `internal/agent/session_dag_unlocked.go:73` | Containment guard clarified |
| F84 | #18 | `internal/agent/session_dag_unlocked.go:62` | Containment guard clarified |
| F85 | #17 | `internal/agent/session_dag_rotate.go:94` | Containment guard clarified |
