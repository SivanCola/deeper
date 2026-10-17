# 智能体预设

[English](AGENT_PRESETS.md)

智能体预设把一段人设提示词和可选的工具白名单保存下来，供会话使用。例如，
可以创建代码审查员、文档作者等常用角色。模型、权限模式、规划和验证策略仍由
各自的设置管理。

## Desktop 使用方式

打开 **设置 → 智能体预设**，创建、编辑、复制或删除预设。保存到“用户”范围可跨
项目复用，保存到“项目”范围则随当前工作区使用。内置“默认”预设只读，可以复制后
再修改。

在空白会话的输入框旁选择预设。第一轮开始后，当前会话的选择会锁定；需要不同
角色时新建会话。保存预设文件不会立即改变正在运行的会话提示词或工具。

远程标签页管理的是远程工作区的预设文件。远程服务需要支持 `agent-preset-v1`；
旧服务会提示此功能不可用。保存包含 `complete: true` 或
`include-runtime-context: false` 的预设还需要支持 `agent-preset-complete-v1`。
仅支持 `agent-preset-v1` 的旧主机仍可保存关闭完整模式、启用运行时上下文的普通预设。

## CLI 使用方式

```sh
deepzora preset new reviewer --scope user --prompt "审查改动，说明可执行的问题。" --tools read_file,grep,glob
deepzora preset list
deepzora preset show reviewer
deepzora --preset reviewer
deepzora run --preset reviewer "审查当前工作区的改动"
```

聊天中输入 `/preset` 或 `/preset current` 查看当前选择，`/preset list` 查看列表。
输入 `/preset select reviewer` 在空白会话中选择预设，`/preset select default` 恢复
默认智能体。完整管理命令见 [CLI 参考](CLI.zh-CN.md)。

## 文件与作用范围

预设使用名为 `<id>.md` 的 Markdown 文件：

- 用户范围：`<Deepzora home>/presets/<id>.md`，通常为 `~/.deepzora/presets/`。
- 项目范围：`<工作区>/.deepzora/presets/<id>.md`。

设置 `DEEPZORA_HOME` 后，用户预设也会迁移到该根目录。同名预设优先使用项目文件。
ID 长度为 1–64 个字符，可用小写字母、数字和连字符，首字符必须为字母或数字。
`default` 是保留 ID。

```markdown
---
name: 代码审查员
description: 审查代码并报告可操作的问题
keep-coding-instructions: true
tools: [read_file, grep, glob, "mcp:github"]
---
审查用户指定的改动。每个问题都应说明具体触发条件、后果和文件位置。
修改文件前先询问用户。
```

支持的 frontmatter 字段：

| 字段 | 含义 |
| --- | --- |
| `name` | 显示名称，省略时使用文件 ID。 |
| `description` | 选择器和管理页中的说明。 |
| `keep-coding-instructions` | 默认为 `true`，将正文追加到基础人设；`false` 用正文替换基础人设，此时正文不能为空。除非 `complete` 为 `true`，否则仍保留核心策略。 |
| `complete` | 默认为 `false`。设为 `true` 时，非空正文成为完整系统提示词，省去核心策略、常驻文档、技能指令和 v4 系统文本。优先于 `keep-coding-instructions`。 |
| `include-runtime-context` | 默认为 `true`。设为 `false` 时，运行时上下文仅保留当前工作目录（cwd），完整模式下同样适用。 |
| `tools` | 省略表示允许所有可用工具；填写列表则仅允许匹配项；`[]` 表示不允许执行工具。 |

两个新增布尔字段均可省略；已有文件保持原来的提示词行为，并默认包含运行时上下文。
`complete: true` 会保留已保存的 `keep-coding-instructions` 值，关闭完整模式后该选择
再次生效。工具准入、权限检查、模型和执行策略仍独立于这些提示词选项。

以下预设使用完整人设，并只保留 cwd 运行时上下文：

```markdown
---
name: 翻译员
complete: true
include-runtime-context: false
tools: []
---
将用户的文本翻译为英文。只返回译文。
```

CLI 的 `preset new`、`edit`、`copy` 都接受 `--complete[=true|false]` 和
`--include-runtime-context[=true|false]`。edit/copy 时省略参数会保留已保存的值。
JSON 定义使用 `complete` 和 `includeRuntimeContext`；旧主机响应省略后者时，
解码默认包含运行时上下文。工具上限仍区分 `null`（不限制工具）与 `[]`（不允许执行工具）。

未知字段和格式错误会报错。工具名支持 `read_*` 等通配模式，`bash` 与 `pwsh` 被视为
跨平台别名。稍后动态加入的工具同样受白名单约束。

MCP 按服务控制：`mcp:github` 允许一个服务，`mcp:*` 允许所有已配置 MCP 服务。
不接受 `mcp__github__create_issue` 等单个 MCP 工具条目。`use_capability` 保留用于
发现和路由，但不会因此获得预设排除的工具或服务。允许的操作仍需通过既有权限检查。

## 会话生命周期

会话只记录预设 ID。重新打开保存的自定义预设会话时，会按 ID 读取最新文件，使用
更新后的人设和工具白名单。分叉会话继承源历史的预设。运行中的会话在多轮之间
保持已组装的系统前缀和工具 schema 稳定。完整模式改变新组装的系统文本，不会清除
恢复或分叉会话中已有的历史上下文，例如先前消息与摘要。

如果已记录的预设文件被删除，会话保留该 ID，给出提示，并在文件恢复前使用默认
智能体。显式要求使用不存在的 ID 创建会话会报错；已有文件格式错误时也会报错，
以便修正。

## HTTP 接口

通过身份认证的 Serve 客户端可以使用以下接口：

| 请求 | 请求体或返回值 |
| --- | --- |
| `GET /agent-presets` | 返回 `{presets, problems, current, locked}`，定义中包含正文和范围。 |
| `PUT /agent-presets/{id}` | JSON `{ "scope": "user", "content": "<完整 Markdown 定义>" }`，返回保存后的定义。 |
| `DELETE /agent-presets/{id}` | JSON `{ "scope": "user" }`，删除对应范围的文件。 |
| `POST /agent-preset` | JSON `{ "agentPreset": "reviewer" }`，切换空白会话的预设。 |
| `POST /new` | 可选 JSON `{ "agentPreset": "reviewer" }`，使用指定预设创建会话。 |

项目范围使用 `project`。`/new` 省略 `agentPreset` 时保留当前预设 ID，并读取其最新
定义；传入 `"default"` 可使用默认智能体。切换会话的请求沿用 Serve 的预期会话身份
校验，锁定后切换返回 HTTP 409。`/status` 返回 `agentPreset` 与 `agentPresetLocked`。
