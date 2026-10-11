# 配置路径

CLI 与桌面端共用 Deepzora home：macOS/Linux 为 `~/.deepzora`，Windows 为 `%APPDATA%\deepzora`。便携安装、测试和隔离开发可通过 `DEEPZORA_HOME` 指定目录。

配置优先级为命令行参数、项目 `deepzora.toml`、全局 `config.toml`、内置默认值。Provider 密钥只保存在 `<home>/.env`，通过 `api_key_env` 引用。项目 `.env` 可以为 MCP/plugin 展开工作区变量，但不提供 Provider 密钥。没有旧凭据导入、系统 keyring 兜底或自动数据迁移。

`DEEPZORA_STATE_HOME` 可单独调整状态根目录，不改变配置和凭据的位置；`DEEPZORA_CACHE_HOME` 调整缓存目录。只设置 `DEEPZORA_HOME` 时，缓存放在 `<home>/cache`，否则使用系统用户缓存目录。工作区写入锁和修复锁始终使用共享的系统用户缓存，防止不同隔离配置同时取得同一工作区的写入权。

| 数据 | 当前路径 |
| --- | --- |
| 全局配置 | `<home>/config.toml` |
| Provider 密钥 | `<home>/.env` |
| 凭据事务 | `<home>/transactions/model-credentials/` |
| 模型设置回执 | `<home>/transactions/model-settings-receipts/` |
| 命令、技能、hooks | `<home>/commands/`、`<home>/skills/`、`<home>/settings.json` |
| 远程 SSH 主机密钥 | `<home>/remote/known_hosts` |
| CLI 会话记录 | `<state>/sessions/`、`<state>/sessions-v4/` |
| 桌面会话记录 | `<state>/desktop-sessions-v5/by-id/` |
| 工作区登记 | `<state>/desktop/workspace-state-v1.json` |
| 会话未发送输入 | `<state>/desktop/session-ui-v1.sqlite` |
| 主题元数据 | `<state>/desktop/topic-state-v1.sqlite` 或对应项目状态目录 |
| 归档与记忆 | `<state>/archive/`、`<state>/memory/`、`<state>/projects/` |
| 用量统计 | `<state>/stats/` |
| 可重建索引 | `<cache>/session-catalog/v6.sqlite`、`history-search/v1.sqlite`、`usage-catalog/v1.sqlite`、`task-catalog/v1.sqlite` |

桌面记录通过 SessionID 定位。工作区归属保存在登记表中，调整归属不会搬动 transcript。未发送输入和图片暂存与已提交 transcript 分开保存。

凭据通过模型设置事务写入，不得进入项目文件、日志或发布产物。索引可以重建，会话、工作区归属、输入、密钥和事务回执属于持久数据。

目录数字后缀代表当前数据结构。首版基线不会重命名这些目录，也不会把旧格式解释成新格式；只支持当前格式。参见[当前基线](current-version-baseline.md)和[配置规范](SPEC.zh-CN.md#5-配置toml)。
