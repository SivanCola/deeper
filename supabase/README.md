# Supabase 反馈与崩溃报告接收服务

复用桌面端 `ReportCrash`、排队崩溃上传及 CLI `report send` 的 JSON POST
协议。只接入 `DEEPZORA_REPORT_ENDPOINT`；启动 ping、功能统计和 CLI 遥测
端点属于独立服务，不要指向本函数。

数据流：桌面／CLI → `report-ingest` Edge Function →
`submit_deepzora_report` RPC 校验令牌摘要 → `ingest_deepzora_report` RPC
完成去重及限流 → `deepzora_reports` 表。
客户端无须安装 Supabase SDK，也不需要 Supabase Auth 登录。

## 部署

使用 Supabase CLI 2.81.3 或更新版本。先通过 `supabase --help` 和相应
子命令的 `--help` 确认当前版本参数，再运行：

```sh
supabase login
supabase link --project-ref <project-ref>
supabase db push --dry-run
supabase db push
supabase functions deploy report-ingest
```

迁移只创建本项目的报告、限流、令牌配置三张表和两个 RPC。现有项目上部署前，检查 dry-run
包含的迁移；不要把其他未应用的迁移一并推送。

复制客户端配置示例，生成独立报告令牌（`openssl rand -hex 32`）并填写
`DEEPZORA_REPORT_TOKEN`。原始令牌只保存到本地客户端配置，不需要配置
Edge Function Secret。生成摘要 SQL 后，使用管理员身份应用到目标数据库：

```sh
node scripts/configure-report-ingest-token.mjs supabase/report-client.env.local
supabase db query --linked --file supabase/.temp/report-token.sql
```

`SUPABASE_URL`、服务器密钥由 Supabase 注入。函数优先读取
`SUPABASE_SECRET_KEYS.default`，兼容旧项目的 `SUPABASE_SERVICE_ROLE_KEY`。
这些数据库权限密钥只留在函数内。`verify_jwt = false` 是因为现有客户端
没有用户 JWT；函数计算 `X-Deepzora-Report-Token` 的 SHA-256 摘要，数据库在
事务入口核对受限配置表中的摘要。请求中的原始令牌不传入数据库。配置表为空时
返回 503，令牌不匹配时返回 401；错误令牌不会写报告或消耗限流额度。

## 配置客户端

复制 `supabase/report-client.env.example` 为
`supabase/report-client.env.local`，填写 endpoint 和报告令牌，设置文件权限
`chmod 600 supabase/report-client.env.local`。启动前导入进程环境：

```sh
set -a
source supabase/report-client.env.local
set +a
go run ./cmd/deepzora
```

桌面服务需在同样环境中启动。端点在进程初始化时读取，所以设置后必须重新启动；
仅把端点写进模型服务商的 `.env` 不会生效。无报告令牌的已有自定义接收服务仍
支持原来的 JSON 协议。配置令牌后，上传要求 HTTPS，本机回环地址除外。
上传不跟随重定向，避免报告正文和自定义令牌被转发。

报告令牌仅保护提交入口，不授予报告读取权，也不能用作用户身份认证。
分发到客户端的令牌可能被提取；泄露后替换本地令牌并重新运行摘要配置步骤。
函数接受 32–256 字符；本仓库的配置脚本要求使用上述 64 位十六进制随机令牌。

## 存储与响应

- 接受 `schemaVersion` 2、3、4；当前 CLI 发 2，桌面发 4。
- 接受 `crash`、`exception`、`feedback`、`performance`、`bot`。
- 请求上限 512 KiB，同时限制单字段、设备信息及最多 30 条 breadcrumbs。
  只存储协议允许的字段；原有客户端负责文本脱敏。
- `eventId` 是唯一事件：首次提交返回 202，重试返回 200，不覆盖首次正文。
- `dedupKey` 用于同类崩溃聚合，多个不同事件不会被合并删除。
- 数据库在事务中限制整个接收服务每分钟 120 个新事件；重复事件不占额度。
  超额返回 429 和 `Retry-After: 60`。额度跨函数实例共享，不存储客户端 IP。
- 数据库异常或未配置返回 503。非 2xx 在客户端仍视为失败；桌面已排队的
  崩溃报告保留到下次启动重试，CLI 本地报告也保留。手动反馈维持原有报错行为。
- 三个表均启用 RLS；`anon`、`authenticated` 无读写权限，两个 RPC 也不可调用。
  管理员通过 Dashboard／SQL 读取；客户端没有报告列表 API。

在 Supabase SQL Editor 查询最新反馈：

```sql
select received_at, event_id, version, source, payload ->> 'message' as message
from public.deepzora_reports
where kind = 'feedback'
order by received_at desc
limit 50;
```

同类崩溃聚合：

```sql
select dedup_key, version, count(*) as occurrences, max(received_at) as last_seen
from public.deepzora_reports
where kind in ('crash', 'exception')
group by dedup_key, version
order by last_seen desc;
```

报告默认不会自动删除。运营方应明确保留期限；例如每日通过自己的维护任务执行
`delete from public.deepzora_reports where received_at < now() - interval '90 days';`。
限流计数在接收新事件时清理十分钟前的记录。Supabase 平台仍可能保留访问日志和
数据库备份，清理报告表不等于清理所有平台副本。

## 验证

```sh
go test ./internal/reporttransport ./internal/crashreport
cd desktop
go test . -run 'Test(PostCrash|ReportCrash|CrashReport|FlushPendingCrash|ConcurrentFlushPendingCrash|WritePending|PendingCrash)'
```

函数没有第三方运行时依赖。使用 Deno 2.x：

```sh
deno task --config supabase/functions/report-ingest/deno.json check
deno task --config supabase/functions/report-ingest/deno.json test
```

`supabase/tests/report_ingest.sql` 验证 RLS／权限、重试、同类事件及限流，
只允许在名为 `deepzora_report_test` 的一次性本地 PostgreSQL 数据库中执行，
要求先创建 Supabase 的 `anon`、`authenticated`、`service_role` 角色，
后者需 `BYPASSRLS`，并应用迁移。测试事务最后回滚。

线上验收需发送一条明确标记的测试反馈和一条测试崩溃，再重发相同 `eventId`，
确认分别收到 202、202、200，且表中只新增两个事件。

可使用真实 Go 上传通道发送合成测试事件（不会读取用户会话或崩溃队列）：

```sh
go run ./tools/reportingestprobe --env-file supabase/report-client.env.local
```

工具输出两个事件 ID，用 SQL Editor 查询确认表中只有这两条。
