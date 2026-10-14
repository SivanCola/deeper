# 隐私政策

[English](PRIVACY.md) · [代码签名政策](CODE_SIGNING_POLICY.zh-CN.md)

最后更新：2026-10-14。本政策适用于 <https://github.com/SivanCola/deepzora>
维护的 Deepzora CLI 和桌面应用，说明已审核源码及可选功能的行为，不涵盖
Reasonix 发布版本或独立运营的模型服务和集成。

## 本地数据和访问

Deepzora 在运行应用或代理的机器上保存配置、凭据、会话历史、草稿、日志、记忆、
索引及缓存。macOS/Linux 默认目录为 `~/.deepzora`，Windows 为
`%APPDATA%\deepzora`。`DEEPZORA_HOME`、`DEEPZORA_STATE_HOME`、
`DEEPZORA_CACHE_HOME` 可调整位置，详见[配置路径](docs/CONFIG_PATHS.md)。
桌面／浏览器配置及操作系统管理的应用数据还可能位于其他目录。

服务商凭据可能存入本地 `.env` 文件，访问受文件权限保护，但 Deepzora 不对该文件
加密。请保护账号、机器及备份。本项目不会自动导入旧应用凭据。

代理可以在获准范围内读取工作区文件、执行工具及命令，并将相关内容作为模型上下文。
上下文可能包含提示词、会话历史、文件全文或片段、附件和工具结果，并不限于手动
附加的文件。请按实际需要设置权限和工具。

支持材料可能包含敏感信息。尤其是 `deepzora doctor session` 可导出完整、未脱敏的
会话记录。分享报告、截图、日志及压缩包前，请先检查并脱敏。

## 网络接收方及用途

| 功能 | 接收方及数据 | 连接时机 |
| --- | --- | --- |
| 模型请求 | 你配置的模型服务商或代理接收任务上下文及请求元数据；辅助／评估模型也可能接收相关上下文 | 执行使用相应模型的任务时；也支持本地模型端点 |
| 工具及集成 | MCP 服务、插件、hooks、机器人、远程代理、shell 命令及浏览器目的地接收其操作所需的数据 | 你启用／调用，或代理在获准范围内调用时 |
| 更新及下载 | GitHub 及其发布／CDN 基础设施接收更新清单和下载请求的正常 HTTP 信息；可选组件下载连接其分发方 | 支持的桌面构建默认检查更新；手动检查及下载也会联网 |
| 可选诊断 | 已配置的接收端点接收下文列出的有限诊断数据 | 同时具备端点及相应收集／同意条件时 |
| 支持提交 | 你选择的目的地，例如 GitHub issue，接收你提交的内容 | 你分享反馈、报告或文件时 |

标准更新清单为 <https://github.com/SivanCola/deepzora/releases/latest/download/latest.json>。
更新检查不上传会话。网络服务可以看到 IP、请求资源及正常 HTTP 元数据。
远程代理的数据在远程主机上存储和处理，删除本地数据不会删除远程副本；使用前
请核查远程主机及 SSH 配置。

## 可选诊断及关闭方式

已审核源码未内置诊断接收 URL；以下进程环境变量用于配置目的地。
未设置或为空时，相应上传无法执行：

| 端点变量 | 数据及控制方式 |
| --- | --- |
| `DEEPZORA_PING_ENDPOINT` | 桌面启动 ping：安装 ID、版本、系统、架构、渠道及有限平台信息。受桌面 `telemetry` 控制，开发构建跳过自动上传。 |
| `DEEPZORA_METRICS_ENDPOINT` | 桌面功能／偏好／生命周期的聚合计数及安装、构建、设备信息。受桌面 `metrics` 控制，开发构建跳过自动上传。 |
| `DEEPZORA_REPORT_ENDPOINT` | 崩溃／错误堆栈、诊断轨迹及主动提交的反馈内容。桌面自动发送排队崩溃报告受 `telemetry` 控制；CLI 崩溃报告经本地审阅后发送。即使关闭 `telemetry`，主动提交桌面报告／反馈仍可发送至已配置端点。 |
| `DEEPZORA_TELEMETRY_ENDPOINT` | CLI 启动／聚合使用指标：安装、构建、平台信息及有限计数。须为符合条件的发布构建且启用 CLI 遥测；检测到 CI 或 CLI 环境变量退出设置时禁用。 |

桌面 `telemetry` 和 `metrics` 默认开启，但无端点时无法上传。安装 ID 跨启动保留，
属于假名标识，不保证完全匿名。聚合指标不包含提示词、文件内容和 API 密钥。
崩溃报告尽力清理令牌、邮箱及用户专属路径，仍可能遗漏敏感信息；请检查主动提交的
报告。反馈可以包含你输入或附加的内容。

首次满足条件的交互式 CLI 启动若未配置遥测，会询问同意并保存选择。
`auto` 限于符合条件的交互式会话；`on` 可用于符合条件的非交互运行；`off` 禁用。
CLI 遥测与桌面偏好分别控制。

如需关闭桌面自动检查更新及可选诊断收集，将以下设置合并进现有 `config.toml`
对应配置段，不要重复创建已有段：

```toml
[desktop]
check_updates = false
telemetry = false
metrics = false

[telemetry]
cli_metrics = "off"
```

也可运行 `deepzora config telemetry off`。非空 `DO_NOT_TRACK` 或
`DEEPZORA_TELEMETRY=off`（也接受 `0`、`false`、`no`）覆盖 CLI 指标设置。
这些环境控制只影响 CLI 指标，不关闭模型请求、桌面功能、工具或手动检查／下载／
报告。若不希望相应端点可用，请在启动前移除诊断端点环境变量。
关闭遥测会丢弃尚未发送的桌面自动崩溃报告；主动提交报告仍是独立操作。

启用自定义接收端点的运营方应向用户说明运营者、用途、保留期限、访问和传输安排。
正式版本新增数据收集前，项目须更新本政策，并提供安装告知及关闭非预期收集的方式。

本仓库提供可选的 [Supabase 报告接收服务](supabase/README.md)。仅在进程环境中
配置 `DEEPZORA_REPORT_ENDPOINT` 和独立的 `DEEPZORA_REPORT_TOKEN` 后使用；
不会自动启用或内置线上地址。令牌随报告请求发送给所配置的接收端，只有提交用途，
不包含 Supabase 数据库密钥。服务保存上表中的报告、事件 ID、错误分组标识和接收
时间，安装 ID 仍遵守现有发送条件。服务端只保存提交令牌的 SHA-256 摘要，三个
报告相关表对普通客户端关闭访问，报告默认
不会自动删除；运营方需制定保留及删除安排。报告表不存储 IP，Supabase 平台仍可
看到网络请求元数据并按其政策保留日志或备份。原有桌面及 CLI 同意／关闭方式继续适用。

## 保留、删除及第三方

本地数据保留至你主动删除或对应功能清理／轮换，不同历史、日志、缓存和备份没有
统一期限。卸载程序不一定删除用户数据。删除副本时需处理相应本地／远程目录及
备份；回收站和外部备份还可能保留副本。本地删除不会向已联系的服务商或目的地
请求删除其收到的数据。

配置的服务商各自决定数据保留、安全及隐私实践，可能在其他国家处理数据。
请查看实际端点的政策；代理与上游模型服务商可能有不同做法。示例包括
[DeepSeek](https://cdn.deepseek.com/policies/en-US/deepseek-privacy-policy.html)、
[OpenAI](https://openai.com/policies/privacy-policy/)、
[Anthropic](https://privacy.anthropic.com/en/) 和
[GitHub](https://docs.github.com/en/site-policy/privacy-policies/github-general-privacy-statement)。
Deepzora 不承诺其保留期限或不用于训练。当前未配置的诊断端点不代表已经存在
中央诊断服务，也不代表有固定的接收端保留期限。

## 联系及政策更新

当前项目维护者为 [SivanCola](https://github.com/SivanCola)。非敏感隐私问题可通过
[项目 issues](https://github.com/SivanCola/deepzora/issues)提出。私密安全或凭据问题
请使用 [SECURITY.md](SECURITY.md)中的报告渠道，不要在公开 issue 放入密钥或
私密会话。实质性数据流变更须在分发受影响版本前更新本政策；发布说明须链接本政策。
