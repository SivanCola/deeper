# Session archive and recovery / 会话归档与恢复

## Current behavior / 当前行为

Archive removes a current session from the sidebar and exposes it in Trash.
The canonical history stays in place. Restore opens the same SessionRef in its
original workspace. A missing project directory does not authorize moving the
session into the currently selected project.

归档将当前会话从侧栏移入回收站，正文原位保留。恢复使用同一 SessionRef 和原工作区；
项目目录离线不允许把会话改挂到当前项目。

Trash supports read-only preview, restore, permanent deletion and empty-trash.
There is no historical recovery-import entry or discovery of old `.trash`,
JSONL or DAG files. Current archive import/export uses the supported storage
revision without a historical conversion adapter.

回收站保留只读预览、恢复、彻底删除和清空能力。不再提供历史恢复导入入口，也不发现
旧 `.trash`、JSONL 或 DAG 文件。当前归档导入导出要求支持的存储 revision，不做历史转换。

## Authority / 权威状态

| Owner / 所有者 | Responsibility / 职责 |
| --- | --- |
| Current session store | Immutable identity, workspace header, events and content / 不可变身份、工作区头、事件及内容 |
| Workspace registry, schema 3 | Membership, lifecycle, grouping and manual order / 工作区成员、生命周期、分组与手动排序 |
| Rebuildable indexes | Bounded discovery and history queries / 有界列表与历史查询 |

The current registry rejects earlier versions. It has no migration ledger,
source/head mapping, converted-source receipts or second session-organization
store. Current lifecycle operations continue to carry their own operation IDs
and generations so retries do not create duplicate sessions.

当前注册表拒绝以前的版本。不再维护迁移账本、来源/head 映射、转换回执或第二份会话
组织数据。当前生命周期操作仍使用 operation ID 和 generation，使重试保持幂等。

## Admission and retry / 准入与重试

Lifecycle mutations resolve an explicit current session before admission.
Workspace identity, generation and active runtime ownership are rechecked
before publishing a change. A snapshot or registration failure must preserve
the existing runtime and report the failure. A successful restore followed by a
list refresh failure remains a successful restore; retry refreshes the same
session instead of creating another one.

生命周期修改先解析明确的当前会话，再检查工作区身份、generation 和活动运行时所有权。
发布前再次校验。快照或注册失败应保留现有运行时并报告错误；恢复已持久化、随后列表
刷新失败时，仍视为恢复成功，重试只刷新原会话，不重复创建。

Permanent deletion checks the resolved session root and writer ownership. It
must not follow client-supplied paths or delete another session's content.
Interrupted current operations retain their own recovery receipts; recovery
revalidates identity and ownership instead of migrating an old source format.

彻底删除检查解析得到的会话根目录和 writer 所有权，不跟随客户端提供的任意路径，
也不删除其他会话的内容。当前操作中断后保留自身恢复回执；重试重新校验身份和所有权，
不会迁移历史源格式。

See [the supported baseline](NATIVE_SESSION_STORAGE.md),
[Desktop ownership](desktop-session-v5.md) and
[session storage](session-storage-v4.md).
