# Current baseline / 当前基线

Deepzora starts with the current implementation as its first supported version. It does not inherit upstream release numbering or historical migrations. LICENSE and NOTICE preserve attribution.

Deepzora 将当前实现作为第一个受支持版本，不继承上游发布编号和历史迁移逻辑，LICENSE 与 NOTICE 保留署名。

- CLI, Electron desktop, Serve, ACP, bots, and SDK remain current product surfaces.
- Formal sessions own durable composer input and attachments. Workspace-draft databases and APIs are removed.
- Desktop navigation uses scoped tickets; cancellation returns current receipts.
- Remote handshakes require `deepzora-baseline-v1`. Capabilities still express features provided by the active controller.
- Electron owns the tray. The Go host has no native tray fallback.
- Official releases use sealed candidates, immutable source identity, signatures, checksums, and provenance. Interrupted current releases can resume.
- Product-document embedding uses an explicit allowlist. Research and implementation notes are development material.

- 保留 CLI、Electron 桌面端、Serve、ACP、机器人及 SDK。
- 正式会话持久化输入和附件，删除工作区草稿数据库及 API。
- 桌面导航使用带作用域的票据，取消操作返回当前回执。
- 远程握手必须声明 `deepzora-baseline-v1`；能力标识仍表达当前控制器实际提供的功能。
- 托盘由 Electron 管理，Go 主机不再提供原生托盘回退。
- 正式发布保留封存候选、不可变来源、签名、校验和与来源证明，允许恢复当前发布的中断。
- 产品文档通过明确清单嵌入，研究和实现记录仅供开发使用。
