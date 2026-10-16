# 会话所有权、回溯与工作区隔离

<a href="./SESSION_OWNERSHIP.md">English</a>

Deepzora 通过带主机作用域的 `SessionRef` 定位会话。当前会话只使用 framed v4 codec、
schema 4、storage revision 3。详见[存储契约](session-storage-v4.zh-CN.md)与
[Desktop 会话所有权](desktop-session-v5.md)。

## 会话写者

会话服务负责 writer lease、运行时绑定和持久化提交。每次修改都校验当前会话身份及
writer generation；另一个进程必须通过当前 takeover 协议取得写入权。只读历史查询
不会取得写入权，也不会创建会话。

替换控制器时先准备候选实例，验证通过后才转移运行时所有权并发布。准备失败保留原
控制器及其历史。Inbox 回执、后台任务、文件检查点、固定上下文和子 Agent 都属于当前
会话；未绑定会话的控制器不能取消其他会话的后台任务。

## 分叉与回溯

对话分叉和回溯从验证过的 durable 边界创建独立的当前会话，保留源会话。存储及工作区
注册成功后才发布子会话身份，不再在共享 JSONL 文件内选择 head。工作区回滚使用文件
检查点，并独立校验身份和冲突；只修改对话不会回滚文件。

## 恢复

只发布完整提交的 batch。写者先保留不完整尾部的证据再修复。重启恢复会关闭中断的
工具和交互，不重复副作用。部分输出仍可查看，提供给模型的投影只包含有效工具调用
及结果和当前恢复摘要。未知格式或不匹配的 storage revision 会被拒绝，不导入、升级
或发现历史恢复副本。

## Worktree 回退

从消息分叉时可以选择两种工作区策略。**仅分叉对话（共享工作区）**继续使用源工作区，
因此会保留并继续看到当前未提交文件。**隔离 worktree** 则从仓库已提交的 `HEAD`
创建持久的 `deepzora/delivery-*` 分支，把新分叉注册为独立项目，并保持源 checkout
不变。Git worktree 不会复制本地改动，所以组合分叉要求源 checkout 干净；检测到
未提交或未跟踪文件时，Deepzora 会拒绝创建，并提示先 commit/stash，或改用共享分叉。

如果当前目录不是 Git 项目，或环境不满足 worktree 前提，Deepzora 会在共享工作区中
完成会话分叉并明确提示已回退。如果 worktree 创建后，会话创建或标签页挂载失败，
自动清理只会删除分支、`HEAD` 和状态仍与创建结果完全一致的未使用 worktree；一旦
检测到任何变化，就会保留现场以便恢复。成功挂载的 worktree 会作为项目持久注册，
关闭标签页或重启后仍可发现。新建 allocation 还会在 checkout 旁以 `0600` 权限写入
v1 `metadata.json`，绑定原始 source checkout、目标分支、创建时 `HEAD`、受管
worktree 根和临时分支。缺少该元数据的 worktree 无法使用 Merge-Back，
因为 Deepzora 不会猜测目标分支；界面会保留现场并给出手动合并指引。未知元数据版本
同样按失败关闭处理。

Merge-Back 是“合并、清理分离”的失败原子流程。预检会验证受管路径和仓库身份、精确
分支与 `HEAD`、source 干净且没有进行中的 Git 操作、全部可见或 detached Desktop
活动任务、工作区写租约、integrated terminal、ahead/behind、diff 和冲突。取得双
workspace lease 后，Desktop 会
短暂封闭 turn start 和 controller publication，再为 canonical source/worktree 两个根登记
贯穿 Git 变更的 reservation。项目 runtime owner、新 turn 以及 terminal create/write 都
经过同一 admission；子目录和 symlink 别名受保护，prefix sibling 与无关项目不受影响。
worktree 有未提交改动时默认禁止合并；只有用户显式开启自动提交才会继续，并在精确新
提交上重新做冲突预检。确认 token 使用 NUL-safe 状态，同时绑定真实 index entries、
每个脏路径的类型、mode、文件内容或 symlink 目标。自动提交从确认的 `HEAD` 创建 `0600`
临时 index，`git add -A` 只作用于该副本。若真实 index 含有当前完整工作区未表示的
staged/index-only 内容，Deepzora 会停止，真实 index 和两个版本都保持原样。否则通过无
hook、单父提交的 `commit-tree` 创建精确提交，对确认的 worktree branch 做 compare-and-swap，
并仅在真实 index 字节仍一致时通过独占 `index.lock` 安装准备好的 index。branch CAS 后的
任何失败都返回 recovery-required；目标分支、`HEAD`、index 或内容发生漂移时不会继续。
source 合并使用带 Deepzora 命令级提交身份的
`git merge --no-ff --no-commit --no-verify`，不依赖用户 Git identity，也不运行 commit hook，
并把实际 index tree 与重新计算的 merge-tree 精确绑定；准备前及安装 ref 前都会重新验证
worktree root、Git common-dir、symbolic branch、branch ref、`HEAD`、Git operation 和内容
token。只有这些身份、目标分支、原始 `HEAD`、精确 `MERGE_HEAD` 和 prepared tree 都仍
一致时，才通过无 hook 的 `commit-tree` 创建固定 parents/tree 的提交。短生命周期 source
mutation fence 会持有真实 index、`HEAD` 和 `MERGE_HEAD` lockfile，并比较三者准确快照。
这些 checkout 局部锁保持期间，Git 通过指向同一 common ref store 的 detached 管理视图
只取得 branch ref 锁。单个
`update-ref --stdin` transaction 会同时验证 worktree branch ref，并用原目标 `HEAD` 对
target ref 做 compare-and-swap，避免任一 ref 检查部分生效。提交后还会复核两个 checkout、
commit tree、真实 index tree、parents、refs、干净状态和 Git operations。安装后使用
`git merge --quit` 只清理辅助 merge state，不直接更新 `MERGE_HEAD` pseudoref，也不 reset
prepared index。CAS 前只有仍能证明 prepared state 完整的失败才会 abort；target ref 漂移、
CAS 后漂移或无法证明恢复成功的状态返回 recovery-required，同时保留所有 worktree 资源和
外部状态。

合并成功后，Deepzora 先通过正常 Desktop 生命周期切换到记录的 source checkout。每次
前端导航都会向后端登记 opaque intent token；关闭请求在快照前和实际移除 Tab 的线性化
点都必须仍持有该 token。因此更新导航会停止关闭和清理并保留资源；稳定时后端也只会在
精确 source Tab 仍 active、精确 worktree Tab 仍 idle 时关闭页面和终端。

独立、可重试的 finalization 会 reservation 包含原 canonical worktree 与固定 recovery 子树的整个
allocation，并扫描可见及 detached runtime；项目 runtime 创建、恢复、删除/归档 fallback
和重定向都经过同一 admission gate。symlink 与子目录受保护，allocation 外的 prefix
sibling 和其他 allocation 不受影响。只有临时提交已包含在目标分支、身份一致且包含 ignored 文件的
完整 status 为空时，Deepzora 才会先以 `0600` 原子写入 v2 `cleanup-state.json`，记录原路径、
allocation 内随机 recovery 路径、branch、`HEAD` 和 `planned` 阶段。随后使用普通
`git worktree move`，再次验证 common-dir、symbolic branch、branch ref、`HEAD`、Git operation、
完整 status 和注册路径，再把 journal 推进到 `retained`。任一阶段崩溃都按 journal 与 Git
worktree 注册表的精确身份重试；多候选或未知状态失败关闭。

recovery checkout 会继续保持 registered，并继续检出其 `deepzora/delivery-*` 分支。Deepzora
不会注销 worktree、删除临时分支、逐文件 unlink 或递归删除任何路径。因此移动前已经打开的文件
描述符会跟随 checkout，晚到写入仍可恢复；原公开路径重新出现的内容也会原样保留并报告。恢复回执
持久化后，Desktop 只移除原 managed worktree 的陈旧项目注册，保持 source project active，且
不会把隐藏 recovery 路径加入侧栏；注册表写入失败可以借助 journal 重试。

cleanup 只接受当前 v2 journal；未知版本失败关闭并保留 checkout。managed metadata
要求版本 1，不执行旧 journal 转换。

Delivery worktree 仍是可选能力。非隔离目录使用 workspace lease（`filelock`）。
路径型写入对祖先锁和目标路径层级分片加 shared 锁、对具体文件分片加
exclusive 锁，且只在该次 tool 期间持有。整区写入会独占精确根锁和对应层级分片，
因此父工作区与直接打开的嵌套仓库能够互斥，而两个会话仍可同时写不同文件（包括同一
仓库）。`bash`/MCP 的写操作只在该命令期间独占整区；若配置的 tool hook 可能写入
未声明路径，任何 tool 调用都会改用整区锁。文件和层级身份都映射到有界锁分片；哈希
碰撞最多让无关工作串行，不会削弱保护。只读 bash 不拿写锁。冲突卡片会说明正在写的
文件或整区。macOS 使用当前锁协议中的折叠身份协调大小写别名。Git 不是运行
前提；对话结束后不继续占锁。需要长期隔离工作树时再用 worktree。
