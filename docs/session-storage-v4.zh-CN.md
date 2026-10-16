# Deepzora 会话存储 v4

状态：`main` 的唯一生产写入格式。实现位于 `internal/session`，只接受当前 storage revision。

## 身份与目录

```text
<数据根>/sessions-v4/
  <session-id>/
    manifest.json
    events.frames
  .content-v1/              # 按 SHA-256 寻址的不可变内容
  .query-cache/<session-id>/history-v1.sqlite
  .trash/
```

唯一有效的最终 manifest 组合是：

- `schemaVersion: 4`
- `codec: deepzora.session.linear/v4`
- `storageRevision: 3`

缺少或不匹配的 storage revision 会被拒绝。Controller 和客户端使用带主机作用域的
SessionRef；没有迁移适配器或按路径打开旧会话的读取器。Desktop 在注册的
`desktop-sessions-v5/by-id` 根目录下使用同一 codec。

## 事务与帧契约

每个逻辑提交编码为 `batch/begin`、一个或多个 `batch/event` 和 `batch/end`。每条物理记录都是独立、带 CRC 的 Zstandard 帧，帧头包含 `RX4F`、压缩长度和解压长度。结束记录用 SHA-256 校验有序的开始/事件记录；只有完整结束且校验通过的提交才对读取者可见。

写端和读端共用 8 MiB 单帧预算。事件载荷超过 64 KiB 时，必须先发布到内容存储，再接受引用，因此用户内容大小不是单帧限制，也不是会话限制。16 MiB 待写预算只用于可取消背压；大事务仅占有界热内存，不会被当作超限事务拒绝。

accepted 与 durable 水位严格分离。模型请求和有副作用工具调用前必须 `Flush`。写入结果不确定时，系统使用磁盘暂存证据逐字节核对，不按文件大小猜测成功，也不直接重复执行。

## 内容与历史

内容对象保存精确原始字节，以 SHA-256 标识。独立的 1 MiB 分块完整性索引支持校验后的范围读取。只有会话历史索引能证明引用属于该会话 durable 视图时，正文接口才允许读取。导出会复制该快照引用的完整对象闭包，并将归档改写为自包含 `.content-v1`。

SQLite 历史库是可重建派生数据；丢失、版本变化或日志 revision 改变时从事实日志重建。历史分页：

- 首次返回最新的 durable 消息，游标继续向更早历史移动；
- 游标绑定会话身份、存储 revision、投影版本和 durable 快照水位；
- 每页最多 500 条，并遵守约 2 MiB 编码响应预算；
- 巨大单条消息只返回预览和 `ContentRef`。

搜索沿用固定快照游标，只返回命中预览，不返回完整正文。由 SessionService 管理的运行时仅保留 accepted UI 尾部和当前 provider 模型投影；durable UI 正文统一从历史服务读取。

## 支持基线与恢复

schema 4、v4 codec 和 storage revision 3 必须同时匹配。旧 JSONL、schema-1/2 日志、
v3/v3.1 以及先前的 v4 revision 都被拒绝，不执行导入或升级。当前归档的导出和导入
遵循相同的当前存储契约。

- 末尾残缺帧或 batch 视为未提交；写者保留证据后才修复到最后一个完整事务。
- 未知必需事件、完整帧损坏和摘要不一致必须失败关闭。
- 冷浏览、当前归档导入和分叉不恢复批准，也不 arm Goal。重启恢复关闭活动工具和交互，
  不重复副作用。
- 当前归档导入验证暂存目录后原子发布；失败不改源文件。
- 删除会话不得破坏分叉所引用的内容。

## 资源预算与产品上限

系统不设置累计字节数、消息数、事件数或 Goal 轮数上限。64 KiB、1 MiB、2 MiB、8 MiB、16 MiB 分别用于决定存放位置、传输块、响应大小、单次分配和背压。磁盘不足、权限失败、非法输入或单次 provider 请求无法装入工作预算仍会明确报错，但不得因此删除、静默截断历史或提前改变模型上下文。

## 容量验收

容量程序走真实生产调用链，以 JSON 输出耗时、磁盘占用、Go 内存高水位，以及 Unix 上的进程峰值 RSS。默认参数用于快速烟测；发布规模必须显式传入，因此不会变成运行时准入上限：

```sh
go run ./tools/sessioncapacity

go run ./tools/sessioncapacity \
  -root /absolute/path/to/evidence/sessions-v4 \
  -history-messages 50000 \
  -history-bytes 1073741824 \
  -attachment-bytes 1073741824 \
  -workset-bytes 16777216
```

每条历史消息产生一个 `message/complete` 和一个用于约束模型工作集的 `model/context-replace` 事件；最后再写入工作集事件，因此完整命令会略多于 100,000 个事件。附件对象由确定性流生成，不分配附件等大的内存缓冲。程序随后关闭会话、冷启动、重建历史索引、验证保留的模型工作集，并测量有索引的最新页延迟。发布验收应显式传 `-root` 保留证据；省略 root 时，烟测数据会在运行结束后删除。
