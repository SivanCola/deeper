# 会话恢复与并行工作

会话持久化和工作区写入分别管理所有权。一个当前 SessionRef 只有一个写者，独立会话
可以并行运行。只读工具和互不重叠的文件写入可以并行；无法明确声明写入路径的 shell
或 MCP 操作，在执行期间持有工作区写锁。需要独立文件时可使用 Git worktree。

## 当前恢复

完整提交的 framed batch 是权威记录。重启恢复会标记中断回合并关闭未完成的交互，不
重复副作用。部分输出继续供用户检查；当前 provider 投影排除不完整的工具调用配对。
文件回滚仍要求匹配当前检查点身份并通过冲突检查。

分叉和回溯创建独立的当前会话身份，保留源会话并使用当前注册表。不再创建或选择旧
JSONL 恢复副本，也不扫描旧会话文件、导入旧 head 或迁移以前的 schema/storage revision。

## 所有权与诊断

Takeover 使用当前 writer 协议。控制器替换和取消操作始终将后台任务、子 Agent 限定
在所属会话内。工作区锁通过当前协议协调重叠目录、文件声明和大小写别名，没有旧版本
锁协议桥接。

`sessions diagnose`、`sessions reindex` 和 `doctor session <id>` 只处理当前存储。
重建派生的历史缓存不属于源数据迁移。详见[会话所有权](SESSION_OWNERSHIP.zh-CN.md)、
[存储契约](session-storage-v4.zh-CN.md)和[支持基线](NATIVE_SESSION_STORAGE.md)。
