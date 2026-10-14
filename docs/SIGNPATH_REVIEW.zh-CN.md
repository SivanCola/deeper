# Deepzora 独立 SignPath 审核材料

[English](SIGNPATH_REVIEW.md) · [代码签名政策](../CODE_SIGNING_POLICY.zh-CN.md) · [隐私政策](../PRIVACY.zh-CN.md)

准备日期：2026-10-14。源码基线为 `SivanCola/deepzora` main 的
`a1ccceef537b34e52b572d685eb2a9baefd2cee4`。本材料用于补充已提交的独立预审申请，
不代表获批，也不是重复申请。

## 项目事实及公开审核入口

| 项目 | 审核信息 |
| --- | --- |
| 名称／许可证 | Deepzora，公开 MIT 源码；本仓库无商业双重许可 |
| 首页／源码 | <https://github.com/SivanCola/deepzora> |
| 下载／发布页 | <https://github.com/SivanCola/deepzora/releases> |
| 维护者及角色 | SivanCola：作者、审查者和拟定签名批准者；目前为单维护者 |
| 产品 | AI 编程助手，含 CLI、Electron 桌面界面、本地／远程代理及集成接口 |
| 申请状态 | 独立预审申请已提交，Foundation 审核及服务开通仍待完成 |
| 发布／使用证明 | 准备时暂无公开 GitHub Release；不宣称独立使用量数据 |

申请联系资料保留在私有申请记录中，不放入公开材料。不能将上游星标、下载或使用量
作为 Deepzora 的声誉证明。接受申请前可能仍需首个可下载版本及更多维护证据。

## 来源及许可证

[NOTICE](../NOTICE)及中英文 [README](../README.zh-CN.md)明确说明 DeepSeek-Reasonix
来源、初始源码提交 `bddb48202a92e4a20b728d1b4485242137ea615d`、保留的 MIT 版权
及独立发布身份。GitHub 将本仓库标记为 `fork=false`：它通过源码／历史迁移创建，
没有使用 GitHub fork 功能。当前来源记录及明显的衍生项目标识是否满足 Foundation
对 fork 的要求，须由审核方判断；MIT 许可不等于签名服务批准。

衍生代码审核还需证明上游已有签名版本、Deepzora 发布分支与通常产生签名构建的
上游分支之间的关系，并提供继承代码变更的审查记录。这些条件仍待确认，单有来源
署名不足以证明合规。

签名发布前，须提供对应候选的准确源码提交、依赖／许可证清单和制品内容，包括上游
二进制来源及已有上游签名。这些是逐次发布证据，不宣称所有打包依赖已获批准。

## 独立服务配置建议

| 配置 | 建议要求／待办 |
| --- | --- |
| 项目 | 新建 Deepzora 项目，建议 slug `deepzora`；不通过 Reasonix 项目提交 |
| 服务 ID／证书 | 尚未签发或授权，开通后记录实际 ID 和限制 |
| 源码仓库 | 仅 `https://github.com/SivanCola/deepzora.git` |
| 可信构建 | GitHub App 授权本仓库；制品生产任务使用 GitHub 托管 runner；签名前上传制品 |
| 生产策略 | 受保护 `main`、验证源码来源、每次生产签名请求人工批准 |
| 测试策略 | 独立测试证书／策略，并明确标记非生产制品 |
| CI 身份 | 新建仅限 Deepzora 及获准策略的令牌／提交身份，与 Reasonix secret 分离 |
| 载荷配置 | 明确批准项目生成的启动器、服务、CLI、更新辅助程序；校验产品名 `Deepzora`、候选版本和架构 |
| 安装包配置 | 使用返回并验证的签名载荷构建 NSIS 包；按批准的路径及元数据签名最终安装包和符合条件的项目生成卸载程序 |
| 第三方二进制 | 保留上游签名；不将未修改的运行时文件声明为项目自有代码 |
| 发布证据 | 封存候选记录保存提交／运行／来源、哈希、签名 ID、人工批准、签名／时间戳验证和原生验收 |

这些是配置建议，不代表已可使用 SignPath。现有工作流使用 Certum，尚无 `.signpath`
配置。其递归签名所有 PE 的清单不能原样用于 Foundation：Electron 等上游文件须
分别处理。获批后应接入独立制品配置及载荷／安装包两阶段签名，并保留候选及发布门禁。

## 政策对应的实现证据

| 内容 | 源码证据 |
| --- | --- |
| 构建／发布及原生验收 | [release-desktop.yml](../.github/workflows/release-desktop.yml)、[发版候选](RELEASE_CANDIDATES.zh-CN.md) |
| 产品身份及 Windows 文件布局 | [packaging/lib.mjs](../desktop/packaging/lib.mjs)、[desktop-build.sh](../scripts/desktop-build.sh) |
| 当前 PE 清单及 Certum 签名 | [signing-files.mjs](../desktop/packaging/signing-files.mjs)、[sign-certum.ps1](../scripts/sign-certum.ps1) |
| 本地路径及默认偏好 | [config/paths.go](../internal/config/paths.go)、[config/config.go](../internal/config/config.go) |
| 更新端点及控制 | [updater.go](../desktop/updater.go)、[desktop_preferences.go](../internal/config/desktop_preferences.go) |
| 桌面可选诊断及排队报告 | [telemetry_app.go](../desktop/telemetry_app.go)、[metrics_app.go](../desktop/metrics_app.go)、[crash_app.go](../desktop/crash_app.go)、[crash_pending.go](../desktop/crash_pending.go) |
| CLI 同意、退出及接收端点 | [cli.go](../internal/cli/cli.go)、[telemetry/policy.go](../internal/telemetry/policy.go)、[telemetry/client.go](../internal/telemetry/client.go) |
| CLI 本地审阅报告／支持包 | [crashreport/cli.go](../internal/crashreport/cli.go)、[指南](GUIDE.zh-CN.md) |

源码允许配置诊断端点，但没有内置诊断接收 URL；桌面更新检查默认开启。模型和集成
可向已配置接收方传输任务上下文。双语隐私政策说明这些数据流、持续安装 ID、本地
明文凭据、未脱敏支持包及独立的 CLI／桌面控制，不承诺绝不联网或上传数据。

## 仍需补充的证据及启用条件

- 合并政策后，`main` 上的规范链接才公开可用。README 及发布说明生成器已配置政策
  入口，实际发布／下载页仍需公开版本。获批且实际使用后才添加 Foundation 赞助说明。
- 按审核要求提供首个可下载版本及独立维护／声誉证据；未发布项目能否接受由 Foundation 决定。
- 在签名修改过的上游代码前，向审核方确认衍生项目及 `fork=false` 的来源问题。
- 核实 MFA、最小权限成员及贡献审查／逐次签名批准记录。当前分支保护要求 PR 和检查，
  但批准审查人数为零，不构成独立审查证据。
- 取得独立项目 ID、证书限制及获批制品配置，接入 CI 后验证签名返回值及原生运行行为。
- 若正式构建启用诊断接收端，先说明运营者和保留期限，并更新隐私及安装告知。

审核依据：[Foundation 条款](https://signpath.org/terms)、
[申请入口](https://signpath.org/apply)、
[GitHub 可信构建](https://docs.signpath.io/trusted-build-systems/github)、
[项目与签名策略](https://docs.signpath.io/projects)。
