# CI baseline / CI 基线

Ordinary PR and main-push CI has ten validation job definitions, reduced from 25,
plus a non-gating timing evidence collector. The
root and desktop matrices each run on Linux, macOS, and Windows, for 14 validation job
executions when all inputs are affected. The seven protected check contexts
remain `lint`, `root`, `desktop`, `race`, and the three `test (OS)` contexts.

普通 PR 与主分支推送的 CI 从 25 类任务缩减为 10 类验证任务，并有独立耗时采集。
根模块与桌面模块在相关输入变化时各运行三平台验证；全量时执行 14 个验证任务。
七个受保护检查名称保持不变：`lint`、`root`、`desktop`、`race` 和三平台 `test`。

| Owner / 检查 | Coverage / 范围 |
| --- | --- |
| `changes` | Detect relevant inputs; failures cannot silently approve skipped checks / 识别输入，失败时不能靠跳过检查获准合并 |
| `test` | Root Go build and full tests on three platforms; both Windows PowerShell runtimes / 三平台根模块构建和全量测试，Windows 两种 PowerShell |
| `root` | Nested stdlib-only SDK tests and race checks; workflow and release-control contracts / 独立 SDK 测试、并发检查、工作流和发布控制约束 |
| `race` | Concurrency owners including agent, control, bot, session, inbox, and serve / 代理、控制器、机器人、会话、收件箱和服务等并发所有者 |
| `desktop-frontend` | Host contract drift check and one identified stable frontend build / 生成协议漂移校验和带身份的单次前端构建 |
| `desktop-tests` | Deduplicated frontend suites, Electron types/tests/build, packaging unit tests / 去重前端单测、Electron 和打包单测 |
| `desktop-browser` | Verify producer artifact; approval and transcript Chromium regressions / 校验产物身份后运行审批与 Transcript 浏览器回归 |
| `desktop-go` | Independent three-platform Go suites and lint; focused macOS races / 独立三平台 Go 验证、lint 及 macOS 并发检查 |
| `desktop` | Validate the versioned execution plan and each required result; pure UI may skip native Go / 按版本化计划核验结果，纯 UI 可跳过原生 Go |
| `lint` | Formatting, repository standards, pinned Go linter, and root platform build tags / 格式、仓库规范、固定版本 Go lint 和根模块平台构建标签 |

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
