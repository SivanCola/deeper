# Current session storage / 当前会话存储

Deepzora is a new project with one supported baseline. Main conversations use
the v4 framed codec, schema 4, and storage revision 3. The session registry uses
immutable SessionRef identities. Old JSONL/schema-1 main sessions, v3/v3.1
stores, historical import receipts, and downgraded registry writers are not
supported or automatically migrated.

Deepzora 按新项目处理。主会话只使用 v4 framed codec、schema 4、storage revision 3，
由不可变的 SessionRef 标识。旧主会话文件、v3/v3.1 存储和旧注册表不会自动导入或续写。

## Current Agent worksets / 当前 Agent 工作集

Production main and subagent runtimes persist through the current session
service. Agent model worksets and context-maintenance receipts belong to that
session owner. Follow requires a bound current runtime and returns its fixed-cut
history window; it neither selects an Agent DAG backend nor allocates a session
for an unbound reader. Active records outside the tail carry canonical positions
so subsequent paging preserves their order.

主会话和子 Agent 运行时通过当前会话服务持久化，模型工作集及上下文维护记录归属
同一会话。Follow 必须绑定当前运行时，返回固定快照的历史窗口；不再选择 Agent DAG
后端，也不为未绑定的读取创建会话。尾页之外的活跃记录携带权威历史位置，保证后续
分页保持顺序。旧路径式内部读写 API、Agent JSONL/DAG 存储和旧 sidecar 路径工具均已删除。

## Current format boundaries / 当前格式边界

- Durable images use ordered `image_inputs`. `Message.Images` is only a
  transient provider encoding; serialization writes current image inputs and
  reading the old `images` field is rejected.
- Persisted Anthropic reasoning replays the current `thinking_blocks` array.
  The current extension wire can supply a single signed proof; saving it writes
  a current block instead of a serialized `reasoning_signature` mirror.
  DeepSeek/OpenAI/Responses retain their current provider-issued reasoning
  fields and proofs; those fields are not historical application compatibility.
- Tool calls require explicit, unique IDs and names. No positional pairing,
  inferred identity, or missing-name migration is performed. Interrupted valid
  calls keep the current recovery behavior.
- Local presentation records use `local_only`; no orphan-tool sentinel is
  written for older binaries. Current provider projections exclude them.
- Task snapshots require schema 1, explicit `job_id`, and a known
  `runtime_state`; missing state is not converted to `unknown`. Audit events
  also carry explicit job identity. `unknown` remains a valid explicit state.
- Heartbeat configuration requires schema 2 and a positive revision. Run
  history uses schema-1 revision-matched sidecars; unversioned or embedded
  history is rejected. The previous sidecar generation remains solely for
  crash consistency during the two-file commit.
- Session organization belongs to the workspace registry. The project file
  keeps project appearance, pins and topic placeholders without mirroring
  session membership, groups or manual order.

图片、工具调用和任务记录必须使用当前身份和字段。定时任务历史只从版本匹配的
sidecar 读取；保留前一代数据用于崩溃恢复。侧栏会话分组与排序只使用当前 workspace 注册表，项目文件保留项目外观和 topic 占位，
不再读写第二份会话组织数据。

## Protocol and API baseline / 协议和 API 基线

Serve requires `deepzora-baseline-v1`; canonical remote history additionally
requires `history-window-v1`. There is no protocol-7 window fallback. Browser
authentication uses `#token=` plus POST `/auth/token`, or an authorized bearer
or cookie. Query tokens do not establish authentication.

MCP requires protocol `2026-07-28` or newer over stdio or Streamable HTTP.
The legacy HTTP+SSE transport and push-only elicitation registry are removed.
Elicitation belongs to the current request context. Forms use `oneOf`/`anyOf`
constant options; old `enum`/`enumNames` forms are unsupported.

CC Switch import reads SQLite with `enabled_deepzora`; old JSON/backups and
`enabled_codex` schemas are not import sources. Provider presets resolve by
explicit `PresetID`, and Gemini signatures use `extra_content.google`.

Permissions use `Tool`, `Tool(glob)`, `Bash(prefix:*)`, or a JSON-quoted exact
subject such as `Bash("rm *.log")`. `Tool=literal`, space-star shell prefixes,
and bare PowerShell deny cmdlets are rejected. Explicit PowerShell shell
prefix rules retain case-insensitive matching.

Desktop submission requires tab identity and submission ID. Rewind uses the
preview/commit transaction contract, and delivery acceptance is tab-scoped.
Native window operations use Electron's dedicated host channels. Runtime
observations use current snapshots, without RuntimeStatus reconstruction.

远端、MCP、权限和 Desktop 调用只接受上述当前契约。旧输入不会由兼容包装转换，
失败也不会自动选择另一个数据源。当前供应商协议、子 Agent、图片、权限、恢复、
定时任务及侧栏功能继续保留。

The current storage/request-envelope JSON and its internal fingerprints change
once. Both Unix and PowerShell request goldens are updated; system prompts,
tool schemas and prefix-shape goldens are unchanged. Current provider adapters
continue to receive their original image encodings and signed thinking proofs.

持久化及内部请求封装的 JSON 和指纹有一次变化。Unix、PowerShell 请求基线已更新；
系统提示词、工具 schema 和 prefix shape 不变。供应商适配器继续收到原有图片编码
和完整的 thinking 证明。
