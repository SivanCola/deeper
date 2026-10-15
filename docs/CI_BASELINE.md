# CI baseline / CI 基线

Ordinary PR and main-push CI has twelve validation job definitions,
plus a non-gating timing evidence collector. Root and desktop checks cover
Linux, macOS, and Windows. Full PRs and main each execute 15 validation jobs.
The seven protected check contexts
remain `lint`, `root`, `desktop`, `race`, and the three `test (OS)` contexts.

普通 PR 与主分支推送的 CI 有12类验证任务，并有独立耗时采集。
根模块与桌面模块在相关输入变化时各运行三平台验证；全量 PR 与 main 均执行15个验证任务。
七个受保护检查名称保持不变：`lint`、`root`、`desktop`、`race` 和三平台 `test`。

| Owner / 检查 | Coverage / 范围 |
| --- | --- |
| `changes` | Detect relevant inputs; failures cannot silently approve skipped checks / 识别输入，失败时不能靠跳过检查获准合并 |
| `test` | Linux/macOS root Go build and full tests / Linux/macOS 根模块构建和全量测试 |
| `windows-root` | Dynamically discovered complete root build/test coverage and both Windows PowerShell runtimes / 动态发现根模块全部包、完整构建与测试、两种 PowerShell |
| `test-windows` | Preserve `test (windows-latest)` and verify complete package evidence / 保持必需检查名称并校验全部包覆盖证据 |
| `root` | Nested stdlib-only SDK tests and race checks; workflow and release-control contracts / 独立 SDK 测试、并发检查、工作流和发布控制约束 |
| `race` | Concurrency owners including agent, control, bot, session, inbox, and serve / 代理、控制器、机器人、会话、收件箱和服务等并发所有者 |
| `desktop-frontend` | Host contract drift check and one identified stable frontend build / 生成协议漂移校验和带身份的单次前端构建 |
| `desktop-tests` | Deduplicated frontend suites, Electron types/tests/build, packaging unit tests / 去重前端单测、Electron 和打包单测 |
| `desktop-browser` | Verify producer artifact; approval and transcript Chromium regressions / 校验产物身份后运行审批与 Transcript 浏览器回归 |
| `desktop-go` | Independent complete three-platform Go suites and lint; focused macOS races / 独立三平台整包验证，保留 macOS 并发检查 |
| `desktop` | Validate the versioned execution plan and each required result; pure UI may skip native Go / 按版本化计划核验结果，纯 UI 可跳过原生 Go |
| `lint` | Formatting, repository standards, pinned Go linter, and root platform build tags / 格式、仓库规范、固定版本 Go lint 和根模块平台构建标签 |

Windows desktop PRs execute the full module once after runner-volume temporary
storage made desktop sharding no longer cost-effective. Root PRs and main each
use one runner: the complete CLI package runs first, then the complete control
package, followed by every remaining package with at most two package processes.
The protected aggregate verifies the versioned report, source/toolchain identity,
execution policy and complete package coverage. Windows root and desktop tests
use `-count=1` to execute tests and avoid expensive result-cache input processing;
the compilation cache remains enabled. Both PowerShell runtime checks run before
the root suite with their unchanged two-minute limit. Root packages retain their
15-minute limit and share a 30-minute build/test budget within the existing
35-minute job. All seven protected contexts stay intact.
Windows 桌面采用 runner 工作卷临时目录后恢复整包，避免不再划算的分片开销。
根模块 PR 与 main 均采用一路全量执行：先独立运行完整 CLI 包，再运行完整 control
包，其余全部包最多两个包并发。必需汇总核验版本化报告、来源、工具链、执行策略和
完整包覆盖。Windows 根模块与桌面使用 `-count=1` 实际执行测试，避免测试结果缓存的
昂贵输入处理；编译缓存保持启用。两种 PowerShell 检查先于根模块执行，仍各限两分钟。
根模块包级期限仍为15分钟，构建与测试共享30分钟预算，任务总上限仍为35分钟。
七个必需检查名称不变，失败或缺失证据不能通过。

The manual root benchmark runs three paired baseline/optimized samples on the
same source and exact existing Windows build cache, with fresh module downloads.
The baseline retains default package scheduling and result caching; optimized
execution uses the production policy above. Qualification requires identical
package and top-level test outcomes. Build/test timings exclude setup; use the
Actions timing collector for complete runner cost, dependency waits and the
required gate. Report medians and ranges, and keep failed cohorts separately.

手动根模块基准在同一源码、同一精确 Windows 编译缓存上运行三组前后对照，依赖模块
重新下载。基线保留原有默认包调度与测试结果缓存，优化组使用上述生产策略。包级和
顶层测试结果必须一致才可验收。构建/测试计时不包含准备阶段；完整 runner 成本、
依赖等待和门禁耗时以 Actions 耗时采集为准，报告中位数和范围，失败批次单列保留。

Remove obsolete historical-release partitions and credential-migration
acceptance, duplicate per-package selectors, stable/canary build duplication,
ordinary-CI installer packaging, and the large native capture/browser matrix.
Full Go suites replace the selectors; dedicated release workflows retain
signing, packaged startup, installer acceptance, and publication checks.
Manual diagnostic and app-memory workflows remain available. A green ordinary
CI run is not packaged release qualification.

删除旧版本分片与旧凭据迁移验收、重复包选择器、稳定版与 canary 重复构建、
普通 CI 中的安装包构建，以及大型原生截图和浏览器矩阵。全量 Go 测试替代包
选择器；发布工作流继续承担签名、安装包启动、安装器验收和发布检查。
手动诊断与应用内存工作流仍可使用，普通 CI 通过不代表安装包已经完成发布验收。

Current platform tests use the registered native shell (`pwsh` on Windows),
the canonical `Deepzora.exe` entrypoint, and explicitly owned test services.
Remove fixtures requiring historical launcher migration, sandbox deny-marker
repair, or legacy sandbox mutexes. Keep current shortcut ownership, credential
ACL protection, durable-write errors, and full Windows package coverage.
Attachment integration tests await staging completion instead of assuming a
fixed delay is enough on a CI runner.
Native Windows desktop tests have a 35-minute package deadline and a 65-minute
job budget. Other desktop targets retain 15 minutes within a 35-minute job budget.

当前平台测试使用已注册的原生 Shell（Windows 为 `pwsh`）、规范入口
`Deepzora.exe` 和由测试明确管理的会话服务。删除要求迁移旧启动器、修复旧
沙箱拒绝标记或等待旧沙箱互斥锁的测试。保留当前快捷方式归属、凭据 ACL
保护、持久化写入错误及 Windows 全包覆盖。附件集成测试等待暂存完成，
避免用固定延时假定 CI 机器已经完成异步操作。
Windows 原生桌面全量测试包级期限为 35 分钟，任务预算为 65 分钟；
其他桌面平台包级期限仍为 15 分钟，任务总预算为 35 分钟。

The ProjectTree file budget increases by exactly two lines for explicit shared
query invalidation: refresh must observe the current title after a committed
identity-based rename. Oversized test files are split by responsibility instead
of increasing their budgets. No other repository budget is raised.

ProjectTree 文件预算仅增加两行，用于明确使共享查询失效，确保以不可变身份
重命名后能刷新已提交的标题。超长测试文件按职责拆分，没有提高它们的预算；
其余仓库预算没有上调。
