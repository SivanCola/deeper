# Code signing policy / 代码签名政策

[English](CODE_SIGNING_POLICY.md) · [隐私政策](PRIVACY.zh-CN.md) · [SignPath 审核材料](docs/SIGNPATH_REVIEW.zh-CN.md)

最后更新：2026-10-14。

## 项目及授权范围

Deepzora 是采用 MIT 许可证、独立维护的 AI 编程助手，项目地址为
<https://github.com/SivanCola/deepzora>。CLI、桌面应用和发布渠道属于本项目。
项目源自 DeepSeek-Reasonix，上游版权及来源见 [NOTICE](NOTICE)。Deepzora
签名不表示它是 Reasonix 官方发布。

独立 SignPath Foundation 申请已提交，仍待审核。提交申请不等于获得签名授权。
截至本政策审核日，Deepzora 尚无已开通的 SignPath 项目，也未接入 SignPath
发布流程。现有 Windows 工作流使用 Certum，仍需要有效的独立凭据。
不得使用 Reasonix 的 SignPath 项目、令牌或授权为 Deepzora 签名。

只有获批并实际使用服务后，相应发布页和下载页才会添加：
“Free code signing provided by SignPath.io, certificate by SignPath Foundation.”
签名用于标识发布者及保护已签名字节，不保证软件没有缺陷。

## 角色及审查

| 角色 | 职责 | 当前维护者 |
| --- | --- | --- |
| 提交者／作者 | 准备源码变更和候选构建 | [SivanCola](https://github.com/SivanCola) |
| 审查者 | 审查贡献、依赖、来源和发布内容 | SivanCola |
| 签名批准者 | 逐次人工批准符合要求的生产签名请求 | SivanCola |

目前由同一维护者承担上述职责，不宣称存在独立的双人复核。外部贡献必须在发布前
经过审查。参与签名的 GitHub 和 SignPath 账号必须启用多因素认证；服务启用前
必须核查成员、权限及 MFA。新增维护者或签名批准者时，应先更新本政策再授予生产权限。

## 签名及发布流程

1. 取得独立 Deepzora 项目、证书、制品配置和签名策略的授权；CI 提交身份仅限本项目。
2. 从受保护的 `main` 上已记录的提交构建，完成必要检查及贡献审查。当前 GitHub
   要求的批准审查人数为零，不能据此证明已完成人工审查；需另外记录审查及发布批准。
3. 所有制品生产任务使用 GitHub 托管 runner，先上传 GitHub Actions 制品，再携带
   仓库、工作流、提交和分支来源提交签名。每次生产签名请求均须人工批准。
4. 只签名经批准且产品名、版本、构建来源与候选一致的 Deepzora 制品。项目代码包括
   启动器、服务、CLI、更新辅助程序，以及最终 Windows 安装包和符合条件的项目生成
   卸载程序。首次使用前须与 SignPath 确认具体制品路径和产品元数据约束。
5. 保留未经修改的第三方二进制已有签名，包括 Electron 运行时；不得将它们作为
   Deepzora 自有代码签名。未签名依赖可按获批制品配置打入安装包。修改过的上游代码
   须核查来源、许可证并接受 Foundation 审核，本政策不自动授权此类代码。
6. 验证返回的已签名载荷字节，将其打入最终安装包，再单独申请安装包签名。
   发布前验证签名、时间戳、哈希及原生安装／更新行为；不得重新构建替换已验证载荷。
7. 保存源码 SHA、工作流运行、制品摘要、签名请求 ID、批准记录、验证结果及封存候选。
   仅通过[发版流程](docs/RELEASE_CANDIDATES.zh-CN.md)发布已批准候选。

授权、来源验证、签名或验收失败时，不能作为已签名版本发布。测试证书和开发构建
须与正式版本明确区分。现有 Certum 递归签名所有 PE 文件的行为不能直接视为
Foundation 已批准的制品配置。

## 隐私及事件处理

[隐私政策](PRIVACY.zh-CN.md)说明本地存储、模型服务商、更新检查和可选诊断。
数据收集发生实质变化时，须在分发受影响版本前更新政策并提供适用的安装告知。

发现凭据泄露或未授权签名时，通过 [SECURITY.md](SECURITY.md)私密报告。
调查期间暂停受影响签名和发布，必要时轮换凭据或撤销证书，并说明受影响版本及
替代版本。不要在公开问题或构建日志中放入凭据。

本政策参照 [Foundation 条款](https://signpath.org/terms)、
[GitHub 来源要求](https://docs.signpath.io/trusted-build-systems/github)及
[SignPath 项目策略](https://docs.signpath.io/projects)维护。是否批准及服务端限制由
SignPath 审核决定。
