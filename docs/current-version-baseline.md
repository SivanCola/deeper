# Current baseline / 当前基线

Deepzora starts with the current implementation as its first supported version. It does not inherit upstream release numbering or historical migrations. LICENSE and NOTICE preserve attribution.

Deepzora 将当前实现作为第一个受支持版本，不继承上游发布编号和历史迁移逻辑，LICENSE 与 NOTICE 保留署名。

- Background tools use `job_output` and `job_kill` throughout scheduling, evidence classification, CLI cards and Desktop history. Removed the retired `bash_output`, `wait`, `kill_shell` and `waitJob` branches. Current imported hooks translate job IDs, polling/waiting and bounded millisecond timeouts; cancellation and evidence collection retain their ownership fences.
- 后台工具的调度、证据分类、CLI 卡片与 Desktop 历史统一使用 `job_output` 和 `job_kill`，删除 `bash_output`、`wait`、`kill_shell` 和 `waitJob` 的旧分支。当前导入 Hook 同步转换任务 ID、轮询／等待与有上限的毫秒超时，保留取消和证据收集的归属校验。

- Remote catalog rows, navigation, actions, tab restoration and preferences use explicit `sessionId`. Name/path selectors are rejected instead of resolved; the derived sidebar cache requires its current identity schema. New sessions keep their ID before the first turn, including title and pin operations, without migrating empty-name preferences.
- 远端列表、导航、操作、标签页恢复及偏好均使用显式 `sessionId`，拒绝按名称或路径解析旧选择器；侧栏派生缓存只读取当前身份格式。新会话在首轮输入前即保持稳定 ID，改名与置顶直接使用该 ID，不再迁移空名称偏好。
- Every Desktop release uses versioned public filenames, including v0.0.1. Signing, manifests, updater routes and release verification share one artifact catalog. Build-stage files are renamed before signing; published bundles contain exactly the current assets and signatures, with no compatibility aliases.
- 所有桌面版本（含 v0.0.1）均使用版本化发布文件名。签名、更新清单、更新器路由与发布验证共享资产目录；内部构建文件在签名前重命名，发布包严格包含当前资产及签名，不再附带兼容别名。

- Serve titles come from the current session service or a preview of the first message. The unused flash-title provider, `.session-titles.json` cache and mtime-only fallback are removed; current title edits and fork numbering remain unchanged.
- Serve 标题读取当前会话服务或首条消息预览；删除无生产调用的 Flash 标题生成器、`.session-titles.json` 缓存及仅按 mtime 命中的旧兜底，保留当前改名和 Fork 编号规则。
- History displays the current `todo_write` structured result, including empty lists and concurrent in-progress items. Retired `complete_step` results and old textual receipts do not repair or advance a task list.
- 历史任务列表展示当前 `todo_write` 的结构化结果，保留空列表与多个进行中事项；退役的 `complete_step` 结果和旧文本回执不再修复或自动推进任务状态。
- Failed manual creations remain terminal until an explicit retry of their observed revision. Error text does not restart an old failure; current pending-creation recovery and temporary workspace-unavailability retries remain supported.
- 手动创建失败后，只有对已观察版本的显式重试才会重新执行，不再根据旧错误文本自动重启；保留当前未完成创建的恢复及目录暂时不可用时的重试。
- Shell and Git discovery use the complete configured shell selection. The unused path-only wrappers that implicitly selected Bash are removed.
- Shell 和 Git 探测使用完整的 shell 配置；删除无调用、隐式选择 Bash 的旧路径包装。

- Controller persistence uses only the explicitly bound host Runtime. Path-derived shared writers, JSONL snapshot conflict recovery and display-checkpoint sidecars are removed; ephemeral controllers keep only resident state. Save, shutdown and rewind consumers use `Snapshot`. Current session rotation resets review context and refuses to clear a source while cancelled background jobs still own it.
- Controller 只通过显式绑定的宿主 Runtime 持久化；删除按旧路径派生的共享写入器、JSONL 快照冲突恢复和显示检查点 sidecar，临时控制器只保留内存状态。保存、关闭及回退调用统一使用 `Snapshot`；当前会话切换重置审查上下文，取消中的后台任务未退出前不清除源会话。
- `doctor` counts current session directories and their owned files through read-only metadata inspection. It no longer scans JSONL transcripts or conflict logs, creates query caches, or repairs data during collection.
- `doctor` 只读检查当前会话目录的元数据并统计所属文件，不再扫描 JSONL transcript 或旧冲突日志；收集诊断时不创建查询缓存、不修复数据。
- Lifecycle envelopes use the current in-memory ledger. The unused `.turns.jsonl` reader/writer, torn-tail repair, orphan replay, file compaction and their disk-only metrics are removed. Interrupted tool evidence comes from the current Session projection; message ordering, prompt identity and terminal acknowledgement remain.
- 生命周期事件使用当前内存账本；删除无生产调用的 `.turns.jsonl` 读写、残尾修复、孤立回合重放、文件压缩及其磁盘指标。中断工具证据读取当前 Session 投影，保留事件顺序、提示身份和终止确认。
- Goal updates and file checkpoints use the current host session. Removed Goal sidecar readers/writers, path rebinding and their duplicate rollback snapshot; current durable Goal transactions, idempotency and paused-state preservation remain covered by regressions.
- Goal 更新及文件检查点使用当前宿主会话；删除旧 Goal sidecar 读写、路径重绑定及重复的回滚快照，保留当前持久化 Goal 事务、幂等和暂停状态。
- Desktop no longer owns a second JSONL file lease. Removed unused acquire/reclaim/retry entry points, lease mirrors and detached/shutdown transfers. Current runtime bindings retain execution ownership across detach and rebuild; failed snapshots propagate instead of swallowing retired JSONL conflict errors. Busy responses use the current writer-owned error and omit unwritten legacy holder fields.
- Desktop 不再持有第二套 JSONL 文件租约；删除无生产调用的获取、接管、重试入口，以及租约镜像、后台转交和关闭清理。当前 Runtime 绑定保留后台转移与重建的执行权；保存失败直接返回，不再吞掉旧 JSONL 冲突错误。占用响应统一使用当前写入权错误，删除没有当前写入方的旧持有者字段。
- Desktop session routes accept only validated current IDs. Removed JSONL filename resolution, path spelling aliases, duplicate saved-tab paths and automatic controller rebuilding to repair old workspace bindings. Archive guards, background navigation, cold task targets and remote response fences use current identity; route comparisons preserve ID case and reject invalid or missing identities.
- Desktop 会话路由只接受经过校验的当前 ID；删除 JSONL 文件名解析、路径别名、保存标签页中的重复路径，以及自动重建控制器来修复旧工作区绑定的逻辑。归档校验、后台导航、未启动会话的任务目标及远端响应校验使用当前身份；ID 区分大小写，缺失或无效身份不能匹配。

- Session history indexes are disposable caches created from one complete current schema. The v1-to-v10 upgrade chain and read-time index installation are removed. Interrupted tools write only the typed `runState` evidence; current crash recovery still distinguishes unstarted calls from calls with an unknown outcome.
- 会话历史索引直接创建完整的当前结构，删除 v1→v10 升级链及读取时补建索引。中断工具只写入有类型的 `runState`；当前崩溃恢复仍区分未开始调用与结果未知的调用。

- Topic names come from current session metadata and workspace presentation. The separate topic-state SQLite store, old path-metadata rebinding, unused `DeleteTopic` API and recovery-copy display fields are removed. Current session navigation owns workspace switching and terminal teardown.
- 主题名称读取当前会话元数据与工作区展示状态；删除独立 topic-state SQLite、旧路径元数据重绑定、无调用的 `DeleteTopic` 及恢复副本展示字段。当前会话导航负责工作区切换和终端关闭。
- Project metadata no longer mirrors topic lists, pins or tombstones. Forks publish current workspace identity before starting their controller; cold startup validates saved identity instead of migrating an inconsistent historical workspace binding.
- 项目元数据不再镜像主题列表、置顶或删除标记。Fork 在启动控制器前发布当前工作区身份；冷启动只校验保存的身份，不再迁移不一致的历史工作区绑定。

- Guardian reviews retain the current controller-lifetime conversation, delta cursor, compaction and circuit breaker. Removed unused JSONL/cursor persistence, old-session normalization and the `Review` wrapper that discarded review errors; callers use `ReviewVerdict`.
- Guardian 保留当前控制器生命周期内的审查对话、增量游标、压缩和熔断；删除无当前调用的 JSONL／游标持久化、旧数据修复及丢弃审查错误的 `Review` 包装，调用统一使用 `ReviewVerdict`。

- Desktop metadata status and explicit rebuild use the current Query owner. The separate session-catalog SQLite package, legacy directory watches, reconciliation/repair queues and schema/fallback paths are removed. Current metadata repair preserves source bytes, joins duplicate requests, supports cancellation and clears failed-attempt state after a successful retry. CLI diagnostics no longer inspect the retired database.
- Desktop 元数据状态和显式重建使用当前 Query 所有者；删除独立的 session-catalog SQLite 包、旧目录监听、对账／修复队列及结构／降级路径。当前修复不改写源数据，合并重复请求，支持取消，并在重试成功后清除失败状态。CLI 诊断不再检查已退役的数据库。

- New sidebar topics are current durable sessions before any controller starts. Workspace membership, titles, creation time, pins and archive/restore use the current session and registry. The old metadata-only placeholder removal/recovery path, its separate trash identities and topic-index repair writes are removed. Current archive confirmations, duplicate-request receipts, cross-process rename checks and interrupted-operation recovery remain.
- 侧栏新会话在控制器启动前即写入当前会话存储。归属、标题、创建时间、置顶和归档恢复使用当前会话及工作区注册表；删除旧元数据占位项的删除／恢复路径、独立回收站身份和 topic 索引修复写入。保留当前归档确认、重复请求幂等、跨进程改名校验和操作中断恢复。

- Context maintenance commits the current model workset and its receipt atomically. Cold reopen restores the receipt, projection version and blocked-view fingerprint from current events. The old context sidecar, path binding, schema constants, recovery-copy propagation and no-op prune/snip APIs are removed. Benchmarks read live Agent state or use the current Session Service; the obsolete snip flag and report placeholders are removed.
- 上下文维护在同一事件批次中提交当前模型工作集和维护记录；冷启动从当前事件恢复压缩版本及失败退避指纹。删除旧上下文 sidecar、路径绑定、格式版本常量、恢复副本传播和空操作 prune/snip API。基准工具改为读取当前 Agent 状态或使用当前 Session Service，删除无效 snip 参数和报告占位字段。

- Protocol recovery receipts live in current session events and are restored only into the executor's local state. Reopening retains pending manual recovery and consumed retry accounting; copying a session cannot reuse the source's recovery authorization. Provider cache scope, protocol recovery and read cursors use the current owner's identity. The Agent `SessionPath()` getter, unused `LastCacheState` mirror and always-unknown mutable cache state are removed. Deterministic current-store persistence regressions run in the default test suite.
- 协议恢复凭据保存在当前会话事件中，仅恢复到执行器本地状态，不发送给模型。重开保留待处理的手动恢复及已消耗的重试次数；复制会话不能复用源会话的恢复授权。供应商缓存作用域、协议恢复和读取游标使用当前所有者身份。删除 Agent 的 `SessionPath()`、无读取的 `LastCacheState` 镜像和始终为 unknown 的可变缓存状态；当前存储的确定性持久化回归纳入默认测试集。

- Boot requires an explicit host-owned Session Service before assembling providers or plugins. An attached runtime must be the exact instance published by that service; a missing or foreign owner is rejected instead of falling back to the old path runtime. The unused `SessionHostID` option is removed; immutable session identity supplies the host.
- Boot 在组装供应商和插件前要求宿主显式提供 Session Service；传入的 Runtime 必须是该服务发布的同一实例。缺少或传错所有者直接拒绝，不再进入旧路径运行时。删除无读取的 `SessionHostID` 选项，宿主身份由不可变会话身份提供。

- Bound Desktop history readers require a current SessionID and storage generation. Window, outline, location, search and lazy content share a cancellable reader; old file-only tabs are rejected. The `ReadSessionHistorySlice` RPC, native bound search/navigation/content implementations and unused handle capability/backend fields are removed. Location cursors now target the current bidirectional window API; missing turns and unreadable search cursors return typed statuses. The packaging history fixture creates current sessions in a separate writer process.
- Desktop 绑定历史读取必须携带当前 SessionID，并绑定存储代次；分页、目录、定位、搜索和延迟正文共享可取消的读取句柄，拒绝仅带旧文件路径的标签页。删除 `ReadSessionHistorySlice`、旧绑定搜索／导航／正文实现以及无读取的句柄能力和后端字段。定位游标直接用于当前双向窗口；缺失回合和不可读取的搜索游标返回明确状态。打包历史夹具改由独立写入进程创建当前格式会话。

- Desktop history lists use current workspace membership and Session Query metadata. Content search includes current child catalogs, applies membership before ranking, and reads each matching transcript once per frozen result set. Context requests carry catalog, session ID and content digest; lifecycle changes and storage replacement revoke cursors, while appends preserve frozen pages. Missing index revisions are queued through the current catalog. The old JSONL search roots, path-based context reader and unused target-search mock contract are removed.
- Desktop 历史列表读取当前工作区成员及 Session Query 元数据；正文搜索包含当前子代理目录，在排序前限制成员范围，每份固定结果只读取每个命中会话一次。上下文请求携带目录类别、会话 ID 和内容摘要；生命周期变化和存储替换使旧游标失效，追加内容保留固定分页。缺失的索引版本通过当前目录补排。删除旧 JSONL 搜索目录、按路径读取上下文的入口及无调用的目标搜索 mock 契约。

- CLI, Electron desktop, Serve, ACP, bots, and SDK remain current product surfaces.
- Child task and skill transcripts use the host-owned `.subagents` current session catalog. Exact message and context commits replace terminal JSONL rewrites; continuation restores model context, while copies and search retain full history through compaction. Search rechecks the parent's current membership before ranking and body reads. Current parent deletion and Desktop purge remove only their owned children and refuse live child writers. Child metadata is no longer paired with old BranchMeta or transcript sidecars.
- Subagent continuation verifies parent ancestry through the current session catalog. Stale-running cleanup holds the current parent's writer exclusion across metadata rereads. A newly opened parent can clean its own leftovers before its first execution binding; previously bound and foreign writers prevent cleanup. Maintenance does not open or rewrite the parent transcript. The old Desktop parent-path probe and Boot option are removed.
- The unused recovery-copy GC, guarded trash/reparent APIs and preferred-copy setter are removed. Desktop topic opening accepts current identities and does not redirect old filenames to a covering recovery copy.
- Desktop no longer exposes recovery-copy version management or persists an in-log head selection. Forks keep independent current session identities, inherit durable pinned context, and reject another session's search cursor. Startup loads pinned context by SessionID, and ordinary history pagination remains available.
- Serve and Desktop publication no longer bind the old transcript-file lease keeper. Current Session Service ownership still guards background execution, reattachment, handoff and controller replacement. Rotation completion notices carry the newly published session ID. The unused agent head fork/switch/select/retire/rename APIs and boot recovery metadata hooks are removed.
- Formal sessions own durable composer input and attachments. Workspace-draft databases and APIs are removed.
- Session import validates the current archive's storage identity before publication; archives missing that identity are rejected without repair. Remapping a valid current archive still creates its own storage generation and supports history reads before a runtime opens.
- Desktop navigation uses scoped tickets; cancellation returns current receipts.
- Topic creation times come only from recorded fields; blank-tab reuse and sidebar reads do not infer or repair timestamps from historical topic IDs. Current formal and pending sessions are excluded from automatic draft reuse.
- CLI and Serve branch pickers query the current session catalog, with full pagination and current host membership. Switching validates the saved model and rechecks membership before attaching; it accepts current IDs, unambiguous ID prefixes and names, never transcript paths or old in-log heads. New forks record their parent identity even without Desktop metadata. The old JSONL branch scanner and sidecar-shaped picker response are removed.
- History retrieval uses the production asynchronous index; the unused synchronous file-scan tool is removed. Desktop search-context requests require the hit's content digest and reject missing or stale identities instead of falling back to a path-only read.
- The `history` tool reads current Session Query catalogs and rechecks host membership before reading a hit. Its context operation accepts opaque `catalog_id`, `session_id` plus `content_digest`, removing the old `session_path` input. Membership is applied before ranking/limit; cold reads do not acquire writers. The shared index can ingest current snapshots without JSONL/branch sidecar reads.
- Desktop removes the unused `History`, `HistoryPage`, `HistoryPageForTab`, `HistoryCheckpointTurnsForTab`, `TurnEventsForTab`, `HistorySliceForTab`, `HistorySliceForTarget`, `HistoryContentForTab` and `HistoryContentForTarget` RPCs. The old path pager, display-index rebuild queue and native preparation cache are removed. Cold and live history use the current identity-bound window/content APIs; appends preserve immutable content references. Browser fixtures supply private data delegates rather than advertising retired host commands.
- Desktop 同时删除旧事件重放、未绑定的历史分页及正文包装，以及旧文件分页器、展示索引重建队列和准备缓存。冷会话与活跃会话均按当前身份读取；追加消息不会使已有不可变正文引用失效。浏览器夹具通过内部数据委托提供内容，不再暴露旧宿主命令。
- `catalogs reindex history` reads the same current catalog roots as `sessions reindex`, including each root's child-session catalog. It does not scan old JSONL or archive directories, and rebuilding the disposable search index does not rewrite source transcripts.
- CLI 的 `catalogs reindex history` 与 `sessions reindex` 使用同一组当前目录，并包含各自的子会话目录；不再扫描旧 JSONL 或旧归档目录，重建搜索索引不改写源会话。
- Desktop also removes `HistoryForTab`, `ResumeSession`, `ResumeSessionForTab`, `ResumeSessionPage`, `ResumeSessionPageForTab`, `OpenChannelSessionForTab` and `OpenChannelSessionPageForTab`. Recovery consumers adopt through the current transcript endpoint; channel opens retain read-only admission. Cold history reads current session fields without creating a controller or acquiring execution ownership.
- CLI, Desktop, Serve and ACP model, effort and settings rebuilds retain the current Session Runtime. The carried-history path adapter is removed; model-context changes commit with execution ownership. Desktop clear rotates the current identity only after cancelled work has stopped, with generation fences for stale startup and tab callbacks. Deferred Desktop startup retries the current writer admission without probing a legacy file lease.
- `list_sessions` and `read_session` use the current host Session Query and opaque session IDs. Desktop resolves their access from current workspace membership on each request, including archive/restore changes. Old JSONL filenames, path reads, filename-derived models and title sidecar writes are removed from these tools; current title writes use session events.
- Controller loading uses `OpenSession` with immutable identity; the unused transcript/path `Resume` entry and its cold-resume config are removed. Current runtime saves retain extension policy veto and successful-save notification. Successful identity opens retain the advisory load policy; failed and same-runtime opens do not repeat it.
- Fresh creation uses `BindFreshSession`; the unused `SetFreshSessionPath` wrapper is removed. Serve extension reload and detached model refresh retain the exact current runtime without rebinding file leases. A failed replacement preserves the owner, and paused inbox work survives a successful reload.
- Machine session/task commands enumerate the current catalog, retaining opaque IDs, full pagination, task status and output checks. Background jobs bind to the current session directory and survive cold reopen/export; no transcript-sidecar binding is used. Unsupported task evidence formats are rejected without conversion, and missing log names are not inferred. Boot no longer scans old cleanup markers or repairs old trash staging directories.
- Session catalog diagnosis/reindex uses current stores and rebuildable metadata; the old recovery-copy cleanup command is removed. Quality reports and support bundles resolve exact current identities, reject old file references and retain current privacy boundaries. Support bundles use the shared fixed-prefix diagnostic exporter without writer ownership or source repair. The historical in-place credential rewrite command and its DAG rewrite implementation are removed. Serve spectator history reads current events without transcript-file fallback.
- Lazy creation uses `EnsureSession` and propagates failures without allocating an old transcript path. CLI `/web` resumes the durable current ID; ACP guidance uses the current inbox. Current inbox queues and receipts live inside the session directory, survive controller closure, and export with queued attachments. Serve new/clear publishes the identity barrier before its runtime state and keeps switched-away busy sessions reattachable.
- Desktop AI rename uses `AIRenameSessionTarget` with a structured session selector; the unscoped string RPC is removed. Remote resume publishes commit/replay and failure/rollback through their current route-fenced transactions.
- Fork and Branch create independent current sessions without the old JSONL writer or BranchMeta sidecars. Checkpoint actions resolve stable TurnIDs before the selected turn; completed-reply forks retain the selected turn. Isolated forks publish their workspace with the child history and reject retries that change the cut or workspace. Rewind keeps the parent intact, restores files, and invalidates file undo when a new turn starts. Edited-prompt metadata is persisted by message ID.
- Serve executes branch, switch and rewind commands under its binding lock and publishes the new immutable identity before releasing runtime state. `/fork` returns `sessionId`; its title comes from current session metadata, with no file-title cache. The old Controller.Rewind interface is removed: command consumers use prepared plans and refuse unconfirmed partial coverage. The unused Desktop GetSessionVersionState wrapper and DTO are removed.
- The unused `FileBranchesOnly` option, in-log head undo, `afterHeadSwitch`, `SetPreEditHook` and checkpoint `Snapshot` wrapper are removed. File capture uses MutationObserver and the explicit capture API.
- File checkpoints belong to the current session's `file-checkpoints` directory and persist their turn IDs. Controller replacement and cold reopen retain file undo and conversation fork boundaries. Export freezes file checkpoints with the event log and import preserves their preimages. Old preimage `blobRef` and workspace-encoding fallback reads are removed; transaction blobs still support crash recovery and undo.
- Remote handshakes require `deepzora-baseline-v1`. Capabilities still express features provided by the active controller.
- Electron owns the tray. The Go host has no native tray fallback.
- Official releases use sealed candidates, immutable source identity, signatures, checksums, and provenance. Interrupted current releases can resume.
- Product-document embedding uses an explicit allowlist. Research and implementation notes are development material.

- 保留 CLI、Electron 桌面端、Serve、ACP、机器人及 SDK。
- 子代理续跑通过当前会话目录验证父会话及祖先关系。遗留运行状态清理在当前父会话写锁保护下重读元数据；刚打开的父会话可在首次绑定执行前清理自己的遗留状态，已绑定过执行的 Runtime 和其他进程的写入者会阻止清理。维护不打开或改写父会话 transcript。删除旧 Desktop 父路径探测和 Boot 选项。
- 子任务和技能 transcript 由宿主持有的 `.subagents` 当前会话目录保存。消息和模型上下文按事件提交，不再在结束时重写旧 JSONL。续跑恢复模型上下文，复制与搜索保留压缩前的完整历史；搜索在排序和正文读取前重查父会话成员权限。当前会话删除和 Desktop purge 只清理所属子代理，并拒绝仍在运行的子代理写入者。不再配套写入旧 BranchMeta 和 transcript sidecar。
- 删除无生产调用的恢复副本 GC、带锁回收及重设父分支 API 和首选副本写入接口。Desktop topic 打开仅接受当前身份，不再按旧文件名跳转到覆盖它的恢复副本。
- 正式会话持久化输入和附件，删除工作区草稿数据库及 API。
- 会话导入在发布前验证当前归档的存储身份，缺少该身份的旧归档直接拒绝，不再补写修复。有效当前归档仍可重映射为新 ID，并在未启动 Runtime 时读取历史。
- 桌面导航使用带作用域的票据，取消操作返回当前回执。
- topic 创建时间只取已记录字段，空白会话复用和侧栏读取不再从旧 ID 推导或补写时间；已登记的正式会话及创建中的会话不能被自动复用为空白草稿。
- CLI 和 Serve 分支列表读取当前会话目录，保留完整分页及宿主成员范围。切换前校验已保存模型并复核成员关系；仅接受当前 ID、无歧义 ID 前缀及名称，不接受 transcript 路径或旧日志内部 head。新 Fork 即使没有 Desktop 元数据也会记录父会话身份。删除旧 JSONL 分支扫描器和带 sidecar 字段的列表返回结构。
- 历史检索统一走生产异步索引，删除无生产调用的同步文件扫描工具。Desktop 搜索上下文必须携带命中时的内容摘要，缺失或过期时拒绝读取，不再回退到仅按路径读取。
- `history` 工具读取当前 Session Query 目录，在读取命中前复核宿主成员范围；上下文操作使用 `catalog_id`、`session_id` 和 `content_digest`，删除旧 `session_path` 参数。权限过滤先于排序和数量限制，冷读取不占用写锁；共享索引支持直接读取当前快照，无需 JSONL 或 branch sidecar。
- Desktop 删除无生产调用的 `History`、`HistoryPage`、`HistoryPageForTab` 和 `HistoryCheckpointTurnsForTab` RPC；浏览器及回归夹具提供当前历史切片和显式延迟正文委托。当前历史页直接从本次消息快照计算摘要，不读取 branch sidecar。
- Desktop 同时删除 `HistoryForTab`、`ResumeSession`、`ResumeSessionForTab`、`ResumeSessionPage`、`ResumeSessionPageForTab`、`OpenChannelSessionForTab` 和 `OpenChannelSessionPageForTab`。恢复消费者使用当前 transcript 入口，频道打开仍保持只读；冷历史直接读取当前会话字段，不创建控制器或取得执行权。
- CLI、Desktop、Serve 和 ACP 的模型、推理强度及设置重建保留当前 Session Runtime。删除按路径接管历史的适配器，模型上下文变更随执行权切换提交。桌面清空会话会等待已取消的工作退出后再切换当前身份，并阻止过期启动构建及 tab 回调覆盖新状态；延迟启动直接重试当前写入权获取，不再探测旧文件锁。
- `list_sessions` 和 `read_session` 通过当前宿主的 Session Query 和会话 ID 读取。Desktop 每次请求均依据当前工作区成员关系限制范围，并反映归档、恢复状态。这两个工具不再读取旧 JSONL 文件名或路径，也不再从文件名推导模型；标题工具仅写当前会话事件，不再写旧 sidecar。
- Controller 通过 `OpenSession` 和不可变身份加载会话，删除无生产调用的 transcript/path `Resume` 入口及冷恢复配置。当前 Runtime 保存仍支持扩展策略阻止保存和成功保存通知；成功切换身份后保留加载策略提示，失败或同一 Runtime 的重复打开不重复触发。
- 新建会话使用 `BindFreshSession`，删除无生产调用的 `SetFreshSessionPath` 包装。Serve 扩展重载和后台模型刷新保留原有 Runtime，不再重绑文件租约。替换失败保留原控制器，成功重载保留暂停的 Inbox 队列。
- 机器会话/任务命令枚举当前会话目录，保留脱敏 ID、完整分页、任务状态及输出校验。后台任务绑定当前会话目录，支持关闭后重新读取及导出，不再绑定 transcript sidecar。非当前任务证据格式直接拒绝，不转换或推测缺失日志名。Boot 不再扫描旧清理标记或修复旧回收站暂存目录。
- 会话目录诊断、重建使用当前存储及派生元数据，删除旧恢复副本清理命令。质量报告和支持包按精确当前身份解析，拒绝旧文件引用并保留隐私边界。支持包复用固定事件前缀的诊断导出器，不占用写入权、不修复源文件。删除历史凭据就地改写命令及其 DAG 改写实现；Serve 旁观历史直接读取当前事件，不再回退到 transcript 文件。
- 延迟创建统一使用 `EnsureSession`，返回创建错误，不再分配旧 transcript 路径。CLI `/web` 通过已持久化的当前 ID 恢复，ACP 指导使用当前 Inbox。当前队列及回执保存在会话目录内，关闭控制器后仍可读取，导出包含排队附件。Serve 新建、清空会话先发布身份切换，再发布运行状态；切换前仍在运行的会话保留在后台并可重新附着。
- 桌面 AI 命名通过 `AIRenameSessionTarget` 接收结构化会话选择器，删除旧字符串 RPC；远端恢复通过当前路由锁内的事务完成提交与重放、失败与回滚。
- Fork 和 Branch 创建独立的当前会话，不再写旧 JSONL 或 BranchMeta sidecar。检查点操作通过稳定 TurnID 截取选中回合之前的历史；完成回复的分叉保留选中回合。隔离分叉同时发布子会话的工作区与历史，重试不能更换截取位置或工作区。Rewind 保留父会话并恢复文件，新回合开始后文件撤销失效；编辑提示词的标记按消息 ID 持久化。
- Serve 在绑定锁内执行分支、切换和回退命令，先发布新会话身份，再放行运行时状态。`/fork` 返回 `sessionId`，标题读取当前会话元数据，不再使用旧文件标题缓存。删除 Controller.Rewind 旧接口，命令消费者统一预览并提交计划，拒绝未经确认的部分覆盖恢复；同时删除无生产调用的 Desktop GetSessionVersionState 包装和 DTO。
- 删除无读取的 `FileBranchesOnly`、旧日志 head 撤销、`afterHeadSwitch`、`SetPreEditHook` 和 checkpoint `Snapshot` 包装。文件捕获统一使用 MutationObserver 及显式捕获 API。
- 文件检查点由当前会话的 `file-checkpoints` 目录持有，并保存稳定 TurnID。替换控制器和冷启动重开保留文件撤销及会话分叉边界；导出同时冻结检查点与事件日志，导入保留文件原始内容。删除旧文件快照 `blobRef` 和按工作区现状猜测编码的回退；撤销事务自身的 blob 存储继续支持崩溃恢复与 undo。
- 远程握手必须声明 `deepzora-baseline-v1`；能力标识仍表达当前控制器实际提供的功能。
- Desktop 删除恢复副本版本管理界面及旧日志 head 选择字段。Fork 使用独立的当前会话身份，继承持久化的固定文件上下文，并拒绝其他会话的搜索游标；启动按 SessionID 加载固定文件，普通历史列表仍支持分页。
- Serve 和 Desktop 发布控制器时不再绑定旧 transcript 文件租约管理器。后台执行、重新附着、接管和控制器替换仍由当前 Session Service 管理；会话切换完成通知携带新身份。删除无生产调用的 Agent head 分叉、切换、选择、退役、重命名接口及 Boot 恢复元数据钩子。
- 托盘由 Electron 管理，Go 主机不再提供原生托盘回退。
- 正式发布保留封存候选、不可变来源、签名、校验和与来源证明，允许恢复当前发布的中断。
- 产品文档通过明确清单嵌入，研究和实现记录仅供开发使用。
