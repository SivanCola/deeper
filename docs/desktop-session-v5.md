# Desktop Session v5

Desktop Session v5 makes `SessionRef{HostID: "local", SessionID}` the only
runtime identity for local conversations. Workspace membership is presentation
state, not a storage locator, and a tab is only a disposable view over that
identity.

## Authoritative state

- `desktop-sessions-v5/by-id/<session-id>` owns immutable headers, manifests,
  events, and content references.
- `desktop/workspace-state-v1.json` owns Workspace and Session ordering,
  groups, visibility, archives, and in-flight create reservations.
- Canonical title, model, and turn metadata remain events. Query indexes are
  disposable projections and cannot add or remove Workspace members.
- Local Desktop uses one `SessionService` with host ID `local`. A Workspace
  path is recorded in a new session header but is never needed to open it.
- Workspace attachment validates the candidate SessionID against its immutable
  header `cwd`; the ordered registry is the membership authority, while the
  header prevents one Session from being attached to the wrong Workspace.

Creation and rotation reserve a SessionID in `pendingCreates`, seed and flush
the canonical session, attach the ID to its Workspace, and only then expose a
ready tab. Startup completes an interrupted attach when the session exists and
drops the reservation when it does not. Tab pruning is refused if snapshot,
metadata, or registry publication has not completed.

## Opening and recovery

The Workspace browser lists registry IDs even while their projections are
being rebuilt. `OpenSession` first reads history without a controller and never
creates a replacement for a missing or damaged ID. A navigation sequence fences
late results so only the newest selection can replace the visible runtime.
Frontend transcript, draft, operation, paging, and hydration fences use the
complete SessionRef.

When a saved model no longer exists, Desktop retries the same SessionID with
the configured default model, appends the replacement `session/config` event,
and reports a non-blocking warning. History remains independent of projection
and controller startup.

## Organization and history consumers

The workspace registry is the single owner of session groups and manual session
order. Group members are explicit session keys. There is no imported-membership
map or organization sidecar. The projects file retains project appearance,
project ordering, pins, and topic placeholders, without a second copy of session
organization.

Workspace listings and isolated worktree discovery use the project registry.
`desktop-workspace` only remembers the active working directory. The retired
`desktop-workspaces.json` list is neither read nor written, and there is no MCP
migration scan over historical workspace lists. Intentional topic creation and
restore clear tombstones atomically; batch migration/import prepends are absent.

Saved-session preview reads canonical history by session route. Prompt recall
and memory/skill suggestions enumerate active sessions in the selected workspace
registry and read canonical history. Recall prioritizes the current session,
pages through older sessions, and invalidates its cursor when the workspace or
current session changes. Host-generated messages are excluded; display text and
timestamps come from current message fields. Archived sessions are excluded from
recall and suggestions but remain explicitly previewable.

Markdown export captures a canonical export snapshot and uses the shared
`sessionexport` renderer. Desktop has no second Markdown formatter over
`HistoryMessage` arrays. Remote resume tests and callers use the current
selection-aware admission path, including its stale-selection fences.

## Format boundary

Desktop does not import historical JSONL files or older canonical directories.
There is no startup source migration, conversion ledger, old organization
sidecar overlay, or old physical Desktop trash restoration path. Unsupported
historical inputs are not rewritten or adopted.

JSONL is also a serialization format used by current event storage. The current
Agent DAG and canonical session codecs, their write leases, and crash recovery
for current clear/recovery operations remain supported. Removing historical
readers does not remove these active storage mechanisms. Canonical archive,
restore, purge, fork, and workspace ownership continue through the current
session and lifecycle services.

## 中文说明

本地会话以完整的 `SessionRef` 为运行时身份。会话正文与事件存放在
`desktop-sessions-v5/by-id/<session-id>`，工作区注册表统一管理成员、排序、
分组、归档及创建事务。项目配置仍保存项目外观、项目顺序、置顶与话题占位，
不再重复保存会话分组和手动排序，也不读取旧组织 sidecar 或导入标记。

工作区列表及隔离 worktree 发现读取项目注册表，`desktop-workspace` 只记住当前
工作目录；不再读写旧 `desktop-workspaces.json`，也不扫描旧列表进行 MCP 迁移。
主动创建与恢复话题仍原子清除删除标记，旧批量迁移/导入入口已移除。

预览按 canonical route 读取；输入历史召回和记忆/技能建议只枚举所属工作区的
活动会话。召回优先当前会话，跨会话分页，切换工作区或会话后更换游标身份；
跳过宿主生成消息，使用当前消息中的显示正文与时间。归档会话仍可显式预览，
但不会进入召回和建议。

Markdown 导出通过 canonical 快照和共享 `sessionexport` 渲染器完成，不再保留
另一套基于 `HistoryMessage` 数组的格式化器。远端恢复及其回归统一使用带选择
身份的当前入口，继续拒绝过期选择。

启动不导入历史 JSONL 或旧 canonical 目录，不执行旧版本迁移、转换台账、
组织 sidecar 覆盖或旧 Desktop 物理回收站恢复。当前事件存储本身仍可采用
JSONL；当前 DAG、canonical codec、写租约及清空/恢复操作的崩溃恢复继续保留。
归档、恢复、永久删除、Fork 与工作区归属继续由当前会话及生命周期服务负责。
