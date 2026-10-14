# 模型配置完成页验收（方案 1）

## 视觉基准与状态

- Source visual truth: `/Users/work/.codex/generated_images/01a13926-08c3-7ec0-9653-5a4d4b77fdc6/exec-29399bb9-256f-406e-b552-e9a386428ec9.png`，本轮用户选择的第一个展示结果。
- 本地参考副本：`desktop/build/provider-onboarding-acceptance/reference.png`，原图 1644 × 957 px。
- 目标状态：首次模型引导已保存 API Key；选中 DeepSeek/deepseek-flash；无未保存编辑；详情底部显示保存状态与“完成配置，开始对话”。
- 实现测试窗口：1240 × 720 CSS px，Retina 2x，截图 2480 × 1440 px；另验 800 × 600 CSS px 与深色主题。
- 对比均归一到 1240 × 720；完整图 `comparison.png`，底部核心区 `footer-comparison.png`，均位于上述验收目录。原生窗口装饰不属于页面截图，忽略交通灯差异。

## 问题与修复历史

- [P1] 原顶部提示与表单保存操作分离，且从第一个配置连接取默认模型。已移到当前连接的编辑器底部，使用该连接的已保存默认模型；多连接回归以 A/B 两个连接确认 B 不会误用 A。
- [P1] 用户修改 Key 后仍可能看到旧配置的引导完成操作。已区分未保存与已保存状态；有新 Key 或模型编辑时回到“保存更改”，保存失败不进入会话。普通设置不展示引导完成操作。
- [P1] 完成操作在写默认模型后到会话创建结束之间可能重复点击。已增加覆盖整个流程的忙碌状态及同步互斥；失败可重试，“稍后开始”不写默认值、不创建会话。
- [P1] 保存并重新读取设置后，旧的初始定位意图可能把当前连接 B 切回 A。手动选择连接时释放旧定位意图；以新的设置对象模拟真实读回，确认 B 的选择与默认模型保持。
- [P2] 对话模型标识在模型行内换到下一行。证据：`zip-flow/failure.png`。已用同一行 flex 对齐，`zip-final-flow/06-configured.png` 确认修复。
- [P2] 继承的详情标题/编辑器间距造成密度偏离方案 1，兼容设置落到首屏下方。证据：早期 `comparison.png` 对比以及 `visual-flow/06-configured.png`。已取消首次引导的重复上下边距、收紧表单间距，并把高级能力控制放入兼容设置折叠区；普通编辑页面保留原结构。
- [P2] 小字号成功状态使用原绿色时，浅色主题对比度不足。最终文字混合 `--ok` 65% / `--fg` 35%；在实际包中按真实背景测得浅色 Key 状态 6.25:1、模型标识 4.93:1，深色分别 10.86:1、9.75:1，均超过 4.5:1。证据：`zip-release-flow/contrast.json`、`dmg-release-flow/contrast.json`。

## 必需视觉表面

| 表面 | 实现与对比要求 |
| --- | --- |
| 字体与排版 | 使用产品系统字体，14px 表单与主按钮、14px/600 保存状态标题、12px 模型说明；模型名、标识、复选框保持同一行。 |
| 间距与布局 | 保留设置导航、连接列表、详情三栏；详情正文独立滚动，完成区固定在详情底部，间距与按钮高度对齐方案 1。小窗口允许正文滚动和完成区自然换行。 |
| 色彩与 tokens | 使用已有 Graphite 浅/深色 token，品牌橙主按钮，`--ok` 表示保存成功；不把保存状态写成网络验证成功。 |
| 资源与图标 | DeepSeek 品牌使用现有 `/provider-icons/deepseek.svg`，状态使用现有 Lucide 图标；不绘制近似 Logo、不生成替代品牌资源。 |
| 文案与内容 | “API Key 已保存”“配置已保存”“将使用 deepseek-flash 开始对话”“完成配置，开始对话”“稍后开始”。模型标识采用“对话模型”，避免提前暗示已经写入全局默认。 |

保留产品真实完整请求地址及其说明，替代概念图中的简写 Base URL；这是用户配置协议所需的说明。连接名称中的计数和密钥状态来自实际数据。常规设置与其他页面布局不在本轮重设计范围内。

## 行为与打包验收

已通过代码回归：当前连接与模型、未保存 Key、原模型默认值保留、保存失败、稍后开始、重复点击保护、编辑器并发保存、模型编辑/取消、连接导航草稿保留。模型选择器 55/55，生产构建含 TypeScript、Hook/边界、CSS、主题 token 和包体积门禁。

最终 ZIP 解包与 DMG 挂载的原生应用均通过实际运行流程，含真实 UI Key 输入、配置保存、替换 Key 再保存、进入已就绪会话、布局展开/收起保持、重启恢复、选择实际项目文件夹。三个最终场景各 10 项通过，均无页面异常：`zip-release-flow/acceptance.json`（浅色 / 收起）、`dmg-release-flow/acceptance.json`（深色 / 保持展开）、`visual-release-flow/acceptance.json`（官方预设品牌对比）。绑定模型以服务的 Settings 默认值与持久化会话模型共同确认。

最终同状态截图为 `visual-release-flow/06-configured.png`；`comparison.png` 和 `footer-comparison.png` 已用此截图重新生成并逐项检查。另检查 `dmg-release-flow/06-configured-compact.png`：800 × 600 窗口内完成区换行，按钮始终可见，正文可滚动。当前实现与方案 1 的层级、布局、资源和交互一致，无遗留 P0/P1/P2 视觉问题。

包版本 `v0.0.0-onboarding1`，macOS arm64 本地 ad-hoc 签名测试包。完整构建后仅最终 CSS 变化，复用未变更 Go/CLI，通过支持的打包入口重新执行全部前端门禁、签名、ZIP/DMG 生成；见 `build-install.log`、`repack-final.log`。最终 ZIP 结构检查、DMG 校验和、深度严格签名检查与原生服务启动/正常退出通过，见 `verify-final.log`、`startup-release.log`。没有发布或覆盖系统安装。

前序图标修复也随最终 DMG 复验：系统 IconServices 渲染 32–1024 px Retina 图标全部通过，橙色背景覆盖四边，无灰色兼容底板；见 `icon-release.log` 及 `icon-release/system-icon-128.png`。

最终产物 SHA-256：

- `dist/Deepzora-darwin-arm64.zip`: `0c8e8e0fc5b7c7e5fb47a3e1769af1b5fc7ac7d8b731c573aed9ccd2e13b97a1`
- `dist/Deepzora-darwin-arm64.dmg`: `a4fb9358f290bde2f353e2ade18db44bac38a6bf6179d5bfe5d3d6cbacef6238`

默认流程使用隔离数据目录、虚拟 Key 和本地不可达端点；品牌对比流程保留官方预设 URL，仍使用虚拟 Key，可能发生模型目录/余额读取，但没有发送对话或使用真实凭据。不证明远端模型可用性或推理成功。

## Implementation checklist

- [x] 方案 1 的详情底部完成区与 Key 保存反馈。
- [x] 默认模型来自当前选中连接；保存状态与未保存编辑分离。
- [x] 完成操作互斥、失败重试与稍后开始。
- [x] 修复模型行对齐与首次引导信息密度。
- [x] 最终产物动态复验与修复后同状态截图对比。
- [x] 实际渲染文字对比度与小窗口 / 深色主题。
- [x] 原生测试预览打开于配置完成页，使用隔离数据与测试 Key。

final result: passed

上一轮首次启动页的验收报告保留在 `desktop/build/provider-onboarding-acceptance/previous-first-launch-qa.md`。
