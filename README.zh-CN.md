<p align="center">
  <img src="desktop/branding/logo-ribbon.png" alt="Deepzora 橙色缎带 Logo" width="112" />
</p>

<h1 align="center">Deepzora</h1>

<p align="center">
  <strong>Reasonix 1.x 系列的独立衍生版本，可以理解为 Reasonix Mini。</strong>
</p>

<p align="center">
  在桌面、终端与远程工作区中使用 AI 编程。<br />
  自选模型，阅读代码、完成修改、运行测试。
</p>

<p align="center">
  <a href="README.md">English</a> · <strong>简体中文</strong>
</p>

<p align="center">
  <a href="https://github.com/SivanCola/deepzora/releases"><img src="https://img.shields.io/github/v/release/SivanCola/deepzora?style=flat-square&amp;color=f97316&amp;label=release" alt="最新发布版本" /></a>
  <a href="https://www.npmjs.com/package/@sivancola/deepzora"><img src="https://img.shields.io/npm/v/%40sivancola%2Fdeepzora?style=flat-square&amp;color=ea580c&amp;logo=npm&amp;logoColor=white" alt="npm 版本" /></a>
  <a href="https://github.com/SivanCola/deepzora/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/SivanCola/deepzora/ci.yml?branch=main&amp;style=flat-square&amp;label=CI" alt="main 分支 CI" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-AGPL--3.0--only-64748b?style=flat-square" alt="许可证：AGPL-3.0-only" /></a>
</p>

<p align="center">
  <a href="#快速开始"><strong>快速开始</strong></a> ·
  <a href="#桌面版下载">桌面版下载</a> ·
  <a href="docs/GUIDE.zh-CN.md">使用指南</a> ·
  <a href="#文档导航">文档导航</a> ·
  <a href="https://github.com/SivanCola/deepzora/issues">反馈问题</a>
</p>

---

## 从 Reasonix 1.x 出发，独立演进

Deepzora 源自 **Reasonix 1.x 系列**（`main-v2`），使用独立品牌，持续维护并独立发布。你可以把它理解为 **Reasonix Mini**；这是对项目定位的说明，不是 Reasonix 官方的另一个产品版本，也不属于 Reasonix Studio 2.x 发布线。

选好项目，用自然语言描述任务，Agent 就能阅读文件、修改代码、执行命令和运行测试。你可以使用 Electron 桌面端，在终端里直接工作，或通过 SSH 连接远程工作区。内置 DeepSeek 预设，也支持自定义 OpenAI 兼容和 Anthropic 兼容供应商，自行选择模型连接。

Agent 在本机或你选择的远程主机上运行；模型请求所需的上下文会发送给你配置的供应商。数据存储、网络访问与可选诊断的说明见[隐私政策](PRIVACY.zh-CN.md)。

## 你可以用它做什么

| 理解项目并完成修改 | 掌握执行过程 | 接入自己的工作流 |
| --- | --- | --- |
| 阅读仓库、编辑文件、执行命令与测试，查看修改差异和检查结果。 | 先预览计划再实施，自选工具权限，按对话轮次回退受支持编辑工具的修改。 | 连接 MCP 工具、添加技能与扩展，通过 ACP 编辑器或 Go 扩展 SDK 集成。 |
| [回合结果](docs/TURN_RESULTS.zh-CN.md) | [权限模式](docs/TOOL_APPROVAL_MODES.zh-CN.md) · [检查点](docs/CHECKPOINTS.zh-CN.md) | [扩展](docs/EXTENSIONS.zh-CN.md) · [Go SDK](sdk/go/README.md) |

检查点覆盖受支持的文件编辑工具，不覆盖 Shell 命令产生的文件副作用。

## 选择你的工作方式

| 入口 | 适合场景 | 如何开始 |
| --- | --- | --- |
| **桌面端** | 在可视化工作区中管理对话、文件、修改和工具 | [下载 Electron 桌面端](#桌面版下载) |
| **终端** | 交互式编程与脚本任务 | `deepzora` · `deepzora run "任务"` |
| **浏览器与远程** | 在浏览器中工作，或连接远程 SSH 主机上的代码 | `deepzora web` · [远程会话](docs/REMOTE_SESSIONS.zh-CN.md) |
| **编辑器** | 将支持 ACP 的编辑器连接到 Agent | `deepzora acp` · [ACP 指南](docs/ACP.zh-CN.md) |

## 快速开始

### 桌面端

1. [下载并安装](#桌面版下载)适合你系统的 Deepzora。
2. 打开应用，连接模型供应商并填写 API Key。
3. 选择项目目录，输入任务，例如：**“给 HTTP 客户端增加失败重试，并运行相关测试。”**
4. 查看修改和测试结果，在出现审批请求时决定是否继续。

### 终端

安装[已发布的 npm 包](https://www.npmjs.com/package/@sivancola/deepzora)，然后配置模型：

```sh
npm install -g @sivancola/deepzora
deepzora setup
cd /path/to/your/project
deepzora
```

执行单个任务或用于脚本：

```sh
deepzora run "修复失败的测试，并说明修改内容"
deepzora -p "概述这个仓库的结构" --output-format json
```

也可以从 [最新 CLI 发布页](https://github.com/SivanCola/deepzora/releases/latest)下载原生二进制压缩包。会话恢复、模型选择和自动化用法见 [CLI 参考](docs/CLI.zh-CN.md)。

## 桌面版下载

**[下载 Deepzora 桌面版 →](https://github.com/SivanCola/deepzora/releases?q=%22Deepzora%20Desktop%22&expanded=true)**

在发布页选择适合你系统和架构的安装包。可用安装包、安装说明及各版本的签名状态以对应发布说明为准，另见[代码签名政策](CODE_SIGNING_POLICY.zh-CN.md)。

支持的数据格式与兼容范围见[当前基线](docs/current-version-baseline.md)。

## 文档导航

| 开始使用 | 配置与集成 | 开发与贡献 |
| --- | --- | --- |
| [使用指南](docs/GUIDE.zh-CN.md) | [模型设置](docs/MODEL_SETTINGS.zh-CN.md) | [贡献指南（English）](CONTRIBUTING.md) |
| [CLI 参考](docs/CLI.zh-CN.md) | [配置路径](docs/CONFIG_PATHS.zh-CN.md) | [架构与规范（English）](docs/SPEC.md) |
| [计划与目标](docs/COLLABORATION_MODES.zh-CN.md) | [远程会话](docs/REMOTE_SESSIONS.zh-CN.md) | [桌面端开发（English）](desktop/README.md) |
| [权限与沙箱](docs/TOOL_APPROVAL_MODES.zh-CN.md) | [ACP 编辑器集成](docs/ACP.zh-CN.md) | [Go 扩展 SDK（English）](sdk/go/README.md) |
| [检查点与回退](docs/CHECKPOINTS.zh-CN.md) | [扩展与 MCP](docs/EXTENSIONS.zh-CN.md) | [发布候选](docs/RELEASE_CANDIDATES.zh-CN.md) |
| [上下文与记忆](docs/SESSION_MEMORY_RETRIEVAL.zh-CN.md) | [主题包](docs/THEME_PACK.zh-CN.md) | [可选反馈接收服务](supabase/README.md) |

## 从源码构建

使用 [go.mod](go.mod) 固定的 Go 工具链、[.node-version](.node-version) 指定的 Node，以及 [desktop/package.json](desktop/package.json) 指定的 pnpm 版本。

```sh
git clone https://github.com/SivanCola/deepzora.git
cd deepzora
go build -o deepzora ./cmd/deepzora
./deepzora setup
./deepzora
```

桌面端开发：读取工作区声明的 pnpm 版本，安装依赖后启动应用。

```sh
cd desktop
npm install -g "$(node --print 'require("./package.json").packageManager')"
pnpm install --frozen-lockfile
pnpm dev:desktop
```

生产构建与原生安装包打包方式见[桌面端构建指南](desktop/README.md#build)。验证命令统一维护在[贡献指南](CONTRIBUTING.md#development-workflow)和[桌面端测试指南](desktop/README.md#test)中。

## 项目来源、许可与政策

Deepzora 基于 [DeepSeek-Reasonix 的 **1.x 系列**](https://github.com/esengine/DeepSeek-Reasonix/tree/main-v2)演进，感谢 Reasonix 作者与贡献者提供的基础。上游署名和来源完整保留在 [NOTICE](NOTICE)，原 MIT 许可原文保留在 [LICENSE-MIT](LICENSE-MIT)。Deepzora 使用独立的发布和更新渠道。

整体作品采用 [AGPL-3.0-only](LICENSE)，允许遵守协议的商用和独立 fork；分发及修改后的网络服务须履行适用的对应源码提供义务。继承的 MIT 权限与组件明确声明的许可继续有效，详见[许可说明](LICENSING.md)。独立的[品牌政策](TRADEMARK_POLICY.zh-CN.md)不限制软件许可证授予的权利。

[隐私政策](PRIVACY.zh-CN.md) · [代码签名政策](CODE_SIGNING_POLICY.zh-CN.md) · [SignPath 审核材料](docs/SIGNPATH_REVIEW.zh-CN.md) · [安全政策（English）](SECURITY.md)

---

<p align="center">
  <sub>源自 Reasonix 1.x，以 Deepzora 之名独立维护。</sub>
</p>
