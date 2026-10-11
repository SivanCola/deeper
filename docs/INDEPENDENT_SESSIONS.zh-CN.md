# 独立会话

[English](INDEPENDENT_SESSIONS.md)

每个桌面会话通过 HostID 与 SessionID 定位。会话行、选择、未读、标题、菜单、运行时和通知使用同一个身份；主题关联不会让兄弟会话变成同一会话。子 Agent 与自动恢复副本保留各自的语义。

Fork 保存 ParentSessionID，继承父会话分组，默认不置顶；人工排序时紧随父会话，按活动排序的工作区保持活动排序。Fork 持久化和展示可恢复，重试打开同一个持久子会话，不重置用户后来调整的标题、置顶或分组。

请求捕获来源身份与世代。无效引用直接失败，本地解析器拒绝远程引用。远程写入使用远程 owner 与已协商标识，不退回主题级写入。激活票据沿用最初的导航意图，迟到打开不能超越更新的点击。

当前 workspace registry 管理成员关系与组织状态：revision、明确顺序、人工排序偏好和分组。置顶属于会话展示状态。`GetSessionOrganization` 返回快照，`UpdateSessionOrganization` 使用 expected revision 执行语义操作；可见局部页不能作为完整顺序提交。冲突返回最新快照，前端最多重试原操作两次，仍冲突则重读并提示。

只支持当前 registry schema，不导入旧主题 sidecar，不使用旧来源接管 journal，也不维护降级写入镜像。未知 schema 被拒绝，原文件不被覆盖。工作区归属与 transcript 存储分开管理。

列表在发布不可变结果前统一身份、去重、过滤和排序。生命周期、组织与展示共享成员投影。会话头信息和聚合通过有界 store/index 查询读取，transcript 由历史 owner 加载。迟到请求与回调不能更新已替换的会话。

参见[桌面架构](DESKTOP_ARCHITECTURE.zh-CN.md)、[当前基线](current-version-baseline.md)与[历史／滚动契约](TRANSCRIPT_SCROLL_CONTRACT.zh-CN.md)。
