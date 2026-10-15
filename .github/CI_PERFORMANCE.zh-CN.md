# CI 与发版执行

[English](CI_PERFORMANCE.md)

## Desktop PR 检查

`scripts/ci-paths.mjs` 是普通 CI 与内存 CI 共用的路径判定器，分别识别前端、
Go、生成协议、Electron、原生平台和打包输入。`desktop/AGENTS.md` 等明确的说明
文档不会启动构建和长测；前端目录中会进入产品的 Markdown 仍属于构建输入。
未知路径或无法获得可靠 diff 时保守失败。PR 使用 merge-base 差异，push 使用
`before..sha`；保留纯 release-notes 例外后，普通 `main` push 仍运行完整验证矩阵。

普通 CI 有 8 类验证任务及一个非门禁耗时采集任务，保留全部 7 个必需检查；完整范围见
[CI 基线](../docs/CI_BASELINE.md)。根模块和桌面 Go 模块各在 Linux、macOS、Windows
执行一次全量测试。`race` 检查根模块并发所有者，`desktop-go` 在 macOS 上执行
重点桌面生命周期并发回归，两者均在合并前运行。

Windows 桌面全量测试采用 35 分钟的测试进程期限和 65 分钟的任务预算，为冷编译、
vet、lint 与缓存收尾留出时间。此前整个测试进程的 25 分钟期限耗尽时，正在运行
的普通用例仅执行了 3 秒；扩大预算让全量测试有机会完成，不跳过用例。Linux 和
macOS 仍采用 15 分钟的测试进程期限与 35 分钟的任务预算。

`desktop-frontend` 重新生成桌面宿主契约（有漂移即失败），并在 Linux 上生成一次
`electron/stable` 前端。Go 服务不嵌入该产物，因此 Go 验证独立启动，无需下载前端。
发布流程中的消费者仍保留版本化产物身份与摘要校验。

required `lint` 检查格式、仓库规范和根模块 Go 平台构建标签。required `desktop`
要求前端与原生矩阵均成功，路径判断失败或意外跳过不能获准合并。
动画单测保留在统一前端计划中且只执行一次；同一个生产任务执行重点审批和
Transcript Chromium 回归。安装包构建、签名后启动及原生验收由发布流程负责。
内存筛查保留为独立手动工作流。

`node desktop/frontend/scripts/run-ci-tests.mjs --list` 可以查看单测清单。
它展开原有专用脚本和生命周期钩子，自动发现新增测试，并保留每个 TypeScript
测试原有的 loader，每个套件只执行一次。未知命令语法或冲突调用会直接失败。
CI 同时运行两个隔离进程，历史性能基准在它们结束后单独运行。
本地仍可使用原有的 `pnpm test:*` 专用命令。

## 耗时报告

手动内存工作流的 Summary 会分别显示不含排队的阶段执行时间、工作流总等待、
已记录的 job 排队时间之和及 runner 执行时长之和。普通 CI 保存原始 run/jobs、桌面
测试日志及耗时报告 14 天；采集失败不覆盖真实测试结果。缺失排队数据标记未知，
已知依赖等待独立列出。Go JSON 日志中的顶层测试和子测试分开统计，避免重复计时。

用 `node scripts/ci-collect.mjs --repository SivanCola/deepzora --run RUN_ID --output /tmp/ci-evidence`
采集既有运行。`scripts/ci-timings.mjs` 仍可渲染导出的 run/jobs 数据。
在不更新候选分支的情况下手动触发 `ci.yml` 三次，获得同一 SHA、只读缓存的全量样本；
手动运行互不取消。记录 SHA、事件、attempt、runner 镜像和缓存恢复信息，比较中位数
及范围，冷缓存和基础设施故障单列，不能把基线已具备的提速计入新改动。

## 内存筛查

协议 v4 会在 manifest、分片报告和汇总中记录筛查档位。手动触发 App memory
工作流使用 `full`：三个独立进程各执行 128 轮 full、128 轮 windowed、128 轮
safety 和 512 轮 mixed 往返。本地仍可运行 `short`：一个进程执行 32 轮 full、
32 轮 windowed、32 轮 safety 和 128 轮 mixed 往返。普通 PR 和 main push
不自动启动内存筛查。

两个档位都要求精确检查点、每进程五份堆快照、GC、稳定帧等待、源码与构建身份
及相同判定阈值。汇总会拒绝缺失分片、档位不一致和协议不一致。只有明确启用
mock 内存 soak 的 URL 会去掉夹具人为
设置的 1.5 秒 hydration 延迟；加载仍经过异步定时器任务。
普通浏览器和原生几何测试继续使用原来的延迟路径。

采样时鼠标停在会话行之外；布局切换后先完成一次会话往返，再记录预热基线，
避免采到菜单的临时状态。报告声明档位和协议版本，汇总拒绝旧协议。
`timings.json` 在测试宿主侧记录导航、稳定帧等待、GC、堆快照与解析的次数、
总耗时和最大耗时。汇总结果通过 `screeningLevel` 标明档位，短筛查通过不会被
误认为完整验收。筛查通过仍不等于完成离线堆引用归因。

## 签名发版产物

候选签名预检成功后，将完整原生平台矩阵的产物交给 desktop
publisher。后者重新验证授权、候选身份、签名契约及缓存和文档校验，再校验
并发布同一批签名字节，不重复构建和签名。CLI 与 npm 仍等待完整预检通过，
不会提前开始公开发布。

每个平台的 bundle 将文件大小、SHA-256 与候选及控制流程 SHA、版本、tag、
channel、签名契约指纹、run、调用和生产 attempt 绑定。
平台缺失或多余、身份冲突、符号链接、重复文件名及字节变化都会阻止发布。
这些传输哈希补充既有 Authenticode/minisign 校验，不能代替签名验证。

只重跑失败 job 时，可复用同一 run 和调用中此前成功的平台；重建的平台只
替换自身已重新验证的 bundle。新的 workflow run 重新准备整套产物。
独立恢复发版仍自行构建并验证完整矩阵。pnpm 下载依赖按锁文件缓存；签名
产物通过 artifact 交接，不放入依赖缓存。
