# 历史搜索 Catalog

Deepzora 将历史搜索投影保存到 `<cache root>/history-search/v1.sqlite`。
权威数据来自当前主会话及子会话目录中的 framed events。删除或重建搜索数据库不会
删除会话。每个索引根都必须提供当前会话 Query；路径本身不授予读取权限，不扫描或
导入旧 JSONL 文件。

Catalog 的 FTS5 只保存规范化检索 token，不保存完整消息正文。英文沿用现有小写与
代码符号语义，CJK 沿用重叠 bigram。SQLite 选出最终候选后，snippet 和 `around`
上下文才从权威文件读取。user、assistant、tool input、tool error、tool output 都会索引，
但普通 tool output 仍不属于默认搜索种类。

索引列举当前会话身份，对比内容摘要与元数据；缺失的版本进入合并去重后的来源刷新
队列。变化的来源从一致的当前快照重建检索词，不读取 display index 偏移或分支
sidecar。完整扫描、排队刷新、删除及容量维护共享同一写入顺序，避免交错的扫描代次
把仍存在的会话误标为缺失。排队的内容更新会保留来源的扫描代次。

Agent 的 `history` 工具与 Desktop 历史管理器共享该投影。搜索不会同步扫描目录；首次
索引未完成时立即返回已有结果和明确进度。open、running、current 等运行态只从内存
覆盖，SQLite 不能恢复陈旧所有权。面向 provider 的工具名、描述、schema、默认值和
顺序保持逐字节不变。

数据库遵循通用可丢弃投影策略：本地盘使用 WAL、`synchronous=NORMAL`、外键、用户
私有权限和短 busy timeout；远程或不可用缓存自动退化为内存。完整性或迁移失败只隔离
并重建缓存；遇到未来 schema 时保留原库并进入 degraded。

诊断与安全重建命令：

```sh
deepzora doctor catalogs [--json]
deepzora catalogs reindex history [--dir PATH ...] [--json]
```

诊断不会输出 query、token、snippet、消息、tool arguments 或 provider 内容；reindex
只替换该可丢弃投影。
