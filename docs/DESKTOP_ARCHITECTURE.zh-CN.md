# 桌面端架构

[English](DESKTOP_ARCHITECTURE.md)

Deepzora 桌面端由 React 界面、Electron 宿主和 Go 桌面服务组成，作为同一个版本单元发布。Electron 管理原生窗口和浏览器视图，Go 管理业务状态与运行时执行。

```text
React 界面 → 类型化 preload IPC → Electron main → 私有 stdio JSON-RPC → Go 服务
                                     │                              │
                                     ├─ 浏览器视图                   ├─ 控制器、会话
                                     ├─ 窗口、托盘、对话框            ├─ 租约、审批、恢复
                                     └─ 剪贴板、通知                 └─ SSH、设置、扩展
```

| 层 | 职责 |
| --- | --- |
| React | 渲染状态投影，通过类型化桥接提交用户意图 |
| Electron | 原生生命周期、浏览器、菜单、托盘、对话框、剪贴板与通知 |
| Go 桌面服务 | 命令、控制器归属、会话、审批、设置、终端、SSH 与更新协调 |
| 共享 Go 内核 | Provider、工具、持久化、租约、恢复与计费 |
| 远程适配器 | 通过受管理的 SSH 通道转发已获会话授权的宿主能力 |

反射生成的 `DesktopContract` 统一管理命令表、DTO 与握手摘要。事件携带序号、世代、名称和参数。Go 通过 `NativeHost` 委托原生操作，浏览器工具通过宿主能力登记和会话授权执行。参见[宿主协议](DESKTOP_HOST_PROTOCOL.zh-CN.md)、[浏览器契约](DESKTOP_BROWSER.zh-CN.md)和[生成的接口清单](desktop-contract/INVENTORY.md)。

会话未发送输入与已提交 transcript 分开保存。运行时和附件操作绑定来源会话及 revision，已替换 tab 的迟到任务不能取得当前会话的权限。Transcript 保持单一 viewport writer 和有界历史窗口，参见[滚动契约](TRANSCRIPT_SCROLL_CONTRACT.zh-CN.md)。

## 验证

构建并测试两个 Go 模块，生成宿主契约，执行前端与 Electron 测试，并核对生成的接口清单。浏览器测试覆盖新建会话、输入恢复、审批与 transcript 导航。发布候选还必须通过每个支持平台的原生签名和验收；本机浏览器测试或交叉编译不能证明其他平台的原生行为。

开发命令见[贡献指南](../CONTRIBUTING.md)，发布门槛见[发布候选](RELEASE_CANDIDATES.zh-CN.md)。首版基线只支持当前 Electron 宿主和当前通信、存储契约。
