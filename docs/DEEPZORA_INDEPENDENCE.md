# Deepzora independence / Deepzora 独立维护

Deepzora is independently maintained at [SivanCola/deepzora](https://github.com/SivanCola/deepzora).
It retains the MIT license and upstream authorship in [NOTICE](../NOTICE).
`release-notes/`, `CHANGELOG.md`, and recorded historical evidence describe upstream
history; retaining them does not authorize publishing a Deepzora release with the
same historical identity. Prepare and review new Deepzora release notes first.

Deepzora 在自己的仓库独立维护；原 MIT 许可证、提交作者及历史记录完整保留。
历史发布说明仍属于上游历史，不代表 Deepzora 已发布相同版本。

| Surface / 项目 | Deepzora identity / 独立身份 |
| --- | --- |
| Repository / 仓库 | `SivanCola/deepzora`, branch `main` |
| Product / 应用名称 | `Deepzora` |
| CLI / 命令 | `deepzora` (distinct from the Nix package manager) |
| Desktop application ID | `com.deepzora.desktop` |
| macOS/Linux home / 数据目录 | `~/.deepzora` |
| Windows home / 数据目录 | `%APPDATA%/deepzora` |
| Explicit home / 自定义目录 | `DEEPZORA_HOME`, `DEEPZORA_STATE_HOME`, `DEEPZORA_CACHE_HOME` |
| Project settings / 项目设置 | `.deepzora/`, `deepzora.toml`, `DEEPZORA.md` |
| npm root / npm 主包 | `@sivancola/deepzora` |
| npm platform packages / 平台包 | `@sivancola/deepzora-cli-{darwin,linux,win32}-{arm64,x64}` |
| Homebrew release tap / 发布 tap | `SivanCola/homebrew-deepzora` |

Deepzora does not read `REASONIX_*` overrides or migrate `~/.reasonix` automatically.
Configuration serialization remains compatible for an intentional manual copy,
but configuration, credentials, sessions, app locks, shortcuts, caches, update
helpers, protocol names, and desktop browser profiles have distinct identities.
Install both applications separately; do not point their home overrides at the
same directory. Windows MSI/NSIS upgrade identity and Linux Polkit policy use the
Deepzora namespace.

Deepzora 不自动读取 Reasonix 环境变量或搬迁其用户数据。两者可以分别安装。
如需主动复制配置，应先备份并人工选择文件，不共享运行中的数据目录。

### Historical session formats / 历史会话格式

The session storage owner recognizes the exact supported `reasonix.session.*`
`deeper.session.*`, and `deepzora.session.*` codec identities. Codec compatibility does not scan or
open Reasonix's home directory. Only data explicitly placed in Deepzora's chosen
directories or submitted to its existing import API is considered.

会话存储层兼容三组已知格式标识，不扫描或自动打开 Reasonix 数据目录。
只有用户主动复制到 Deepzora 目录、或通过既有导入接口提供的数据才会被读取。

| Format / 格式 | Deepzora behavior / Deepzora 行为 | Previous reader / 旧读取器 |
| --- | --- | --- |
| `reasonix.session.events/v3`, `linear/v3`, `linear/v3.1` | Read and append with the original codec; explicit import creates a separate Deepzora store / 原格式读取和续写，明确导入才创建独立 Deepzora 会话 | Original codec and event representation remain intact / 原标识和事件表达保留 |
| `reasonix.session.linear/v4`, revisions 1–3 | Read checksummed frames and external content; append preserves the original codec / 校验帧和外部内容，续写保留原标识 | Uses the same supported v4 format; older revision limits still apply / 格式仍为原 v4，旧版本的修订上限仍适用 |
| New `deepzora.session.*` stores | Continue to use Deepzora identities / 新会话继续使用 Deepzora 标识 | Reasonix does not read Deepzora's new namespace / Reasonix 不读取 Deepzora 的新命名空间 |
| Unknown codecs, mismatched schemas, future revisions | Reject without rewriting data / 拒绝并保留数据 | No downgrade conversion is implied / 不隐式进行降级转换 |

Existing log bytes, transaction hashes, optional events, and content references
are preserved. A writer rejects a manifest whose codec disagrees with its v4
log; mixed identities in one log are also rejected. The original tagged fixtures
retain their provenance and digests, including historical `main-v2-pr…` names.

既有日志字节、事务哈希、可选事件和内容引用保留。写入器拒绝 manifest 与 v4
日志标识不一致的数据，也拒绝同一日志混用标识。历史测试文件保留真实来源、校验值
及 `main-v2-pr…` 等历史名称。

Before opening a v4 writer, codec validation opens only the fixed log leaf
inside the selected session directory through Go's rooted filesystem API.
Relative log symlinks staying inside that directory remain supported; links
escaping it, including dangling outside targets, fail before manifest mutation.

打开 v4 写入器前，格式校验通过 Go 的目录约束 API 读取会话目录内的固定日志文件。
仍支持指向本目录内部的相对日志链接；指向目录外的链接（包括不存在的外部目标）
会在修改 manifest 前被拒绝。

## CI and release setup / CI 和发布配置

The frontend and Electron use the shared `desktop/pnpm-lock.yaml`; dependency
updates must refresh that workspace lockfile. Keep TypeScript 6 while the
architecture checks depend on its compiler API. React and its type packages
stay on the 19.2 patch line until a newer runtime passes the existing startup
bundle budget. Dependency upgrades do not relax performance or security gates.

前端和 Electron 共用 `desktop/pnpm-lock.yaml`，升级依赖时必须同步更新工作区锁文件。
架构检查仍依赖 TypeScript 6 的编译器 API；React 及其类型包暂时保持 19.2 补丁版本，
后续运行时通过现有首屏体积预算后再升级。依赖升级不放宽性能或安全检查门槛。

The root and desktop modules use Go 1.26.9 and `golang.org/x/net` 0.60.0
to address the reachable standard-library and HTTP/2 vulnerabilities reported
by the release validation scanner. When a local Go configuration disables
automatic toolchain selection, run validation with `GOTOOLCHAIN=go1.26.9`.

根模块和桌面模块使用 Go 1.26.9 与 `golang.org/x/net` 0.60.0，修复发布验证
扫描器发现的可达标准库与 HTTP/2 漏洞。本地 Go 配置若关闭自动工具链选择，
验证时应显式设置 `GOTOOLCHAIN=go1.26.9`。

Windows controller tests run in two complementary `A-M` and `N-Z` partitions.
Each partition retains the 8-minute Go timeout; CI verifies the native test,
example and fuzz-seed inventory has exactly one execution owner before running.
Both partitions must pass for the `root` gate.

Windows 控制器测试分为互补的 `A-M` 与 `N-Z` 两组，每组保留 Go 的 8 分钟超时。
CI 在执行前验证当前平台的测试、示例和 fuzz 种子清单，保证每项只属于一组；
两组都通过后 `root` 检查才可通过。

Push and PR CI runs against `main`. CI checks do not require publishing keys.
Protect `main` and create a GitHub environment named `release` with required
reviewers. The candidate/promotion workflows preserve protected-branch checks,
immutable tag creation, artifact verification, and release approval.
Do not disable these checks to compensate for missing credentials.

CI 对 `main` 的推送和 PR 生效，不需要发布凭据。正式发布前，启用 `main`
分支保护并配置带审批人的 `release` Environment；候选构建和发布保留来源校验、
原子标签、产物校验与审批要求。

Configure the following in this repository's Actions settings, using your own
accounts and keys. Never copy credentials from the upstream project.

For the standard release pipeline, create the separate `SivanCola/homebrew-deepzora`
repository and obtain publishing rights for the npm `@sivancola` user or organization.
普通 CI 和本次代码推送不需要这些发布凭据；首次正式发布前需要准备自有 tap 仓库、npm scope、
签名证书，以及下表中的 Secrets 和 Variables。

| Secret / 凭据 | Required for / 用途 |
| --- | --- |
| `MINISIGN_PRIVATE_KEY` | Deepzora's encrypted artifact signing key / 加密产物签名私钥 |
| `MINISIGN_PASSWORD` | Password for that key / 私钥密码 |
| `APPLE_CERT_P12` | Base64 Developer ID Application certificate / macOS 签名证书 |
| `APPLE_CERT_PASSWORD` | P12 password / 证书密码 |
| `APPLE_API_KEY_P8` | Base64 App Store Connect API private key / 公证 API 私钥 |
| `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID` | Notarization identity / 公证 API 身份 |
| `CERTUM_USERNAME`, `CERTUM_OTP_URI`, `CERTUM_KEY_ID` | Your Certum SimplySign account, OTP URI and certificate SHA-1 thumbprint / Windows 签名 |
| `NPM_TOKEN` | Publishing rights for the seven `@sivancola/deepzora*` packages / npm 发布权限 |
| `HOMEBREW_TAP_TOKEN` | Write access to your separate `homebrew-deepzora` tap / 自有 tap 写入权限 |
| `RELEASE_TAG_TOKEN` | Dedicated token allowed by your tag rules and allowed to read attestations / 发布标签身份 |

### Release identity and protection rules / 发布身份和保护规则

Use a dedicated fine-grained token owned by `SivanCola`, restricted to
`SivanCola/deepzora`, with **Contents: Read and write**, **Actions: Read-only**,
**Attestations: Read-only**, and required **Metadata: Read-only**. Save it as
`RELEASE_TAG_TOKEN`; set the repository variable `RELEASE_TAG_ACTOR` to `SivanCola`.
No account, administration, secret-management, or workflow-write permission is
granted. The current token expires on **2027-01-08**; replace it before expiry.
Contents permission covers repository content as well as tags, so branch and tag
rules remain essential. Never reuse a broadly scoped local GitHub credential.

专用 fine-grained Token 仅授权 `SivanCola/deepzora`：Contents 读写，Actions、
Attestations 和 Metadata 只读。保存在 `RELEASE_TAG_TOKEN`，发布身份变量填
`SivanCola`。不授予账号、管理设置、Secrets 或工作流写入权限；当前 Token
于 **2027-01-08** 到期。Contents 权限也涵盖仓库内容，因此必须保留分支和标签规则。

The `main` ruleset requires pull requests, resolved review conversations, and
up-to-date CI checks from GitHub Actions: `lint`, `root`, `desktop`, `race`, and
`test (ubuntu-latest)`, `test (macos-latest)`, `test (windows-latest)`. It blocks
deletion and force pushes, with no bypass actors. The existing `desktop`
aggregate gates its Go, frontend, browser, native, and packaging jobs; it must
not be replaced by a fabricated or narrower check name. Single-maintainer mode
requires zero external PR approvals; CI and conversation resolution still apply.

`main` 必须通过 PR 合入，解决所有评审讨论，并通过绑定 GitHub Actions 的七项检查；
合入前需与主分支同步。禁止删除、强推，没有绕过名单。桌面检查使用现有 `desktop`
汇总项，覆盖它依赖的 Go、前端、浏览器、原生和打包任务。单人维护模式不强制他人
批准 PR，但仍要求 CI 和讨论解决。

Two active tag rulesets cover `v*`, `npm-v*`, and `desktop-v*`. The creation
ruleset authorizes only the `SivanCola` user to create matching tags. A separate
immutable ruleset blocks all updates and deletions with no bypass actors,
including the publisher. This permits atomic first publication without granting
permission to rewrite an existing release identity.

发布标签使用两套规则：仅 `SivanCola` 可创建，另一套无绕过名单的规则禁止任何人
修改或删除既有标签，包括发布身份本身；因此可原子创建，但不能改写已有发布身份。

The `release` environment accepts only the `main` branch and requires approval
by `SivanCola`. Single-maintainer mode permits the workflow initiator to approve
their own run; it still pauses for explicit manual approval. Administrators
cannot bypass the environment's configured approval. When a second maintainer
joins, enable prevention of self-review and require an independent PR approval.
Credential and policy setup does not publish tags, packages, or releases.

`release` 仅允许 `main` 分支部署，由 `SivanCola` 人工审批。单人维护时允许批准
自己发起的流程，但仍需明确手动批准；管理员不能跳过环境审批。增加第二位维护者后，
可开启禁止自审和独立 PR 审批。配置凭据与规则本身不会创建发布标签或发布软件。

### npm account and token / npm 账号与 Token

The npm user `sivancola` owns the personal scope `@sivancola`; a separate npm
organization is not needed for the configured seven public CLI packages. Save a
dedicated granular access token as the repository Actions secret `NPM_TOKEN` in
`SivanCola/deepzora`. Limit package access to the `@sivancola` scope for initial
publication, leave organization access disabled, and set an expiration. Scope
access covers its current and future packages; after the first real release, a
replacement token can instead select only the seven Deepzora packages.

npm 账号 `sivancola` 的个人 scope 是 `@sivancola`，当前七个公开 CLI 包无需另建组织。
创建专用 granular access token，并保存在 Deepzora 仓库的 `NPM_TOKEN` Secret。
首次发布前可只授权 `@sivancola` scope，组织权限保持 No access，并设置有效期。
scope 授权涵盖其中现有及未来的包；首次真实发布后，可换成仅授权七个 Deepzora 包的 Token。

The current publisher runs `npm publish` and manages dist-tags. It requires
**Read and write (publish and stage)**; a **stage only** token cannot publish new
versions through this workflow. Unattended token-based publication requires
explicit authorization for the token's **Bypass two-factor authentication**
permission. This permission applies to that token's package automation, not a
request to disable account 2FA. Never commit or paste the token into chat.

现有工作流直接执行 `npm publish` 并管理 dist-tags，需选择
`Read and write (publish and stage)`。`stage only` 无法完成这条流程的新版本发布。
无人值守的 Token 发布还需明确授权该 Token 的 `Bypass two-factor authentication`；
这不是关闭账号 2FA。不要把 Token 提交进代码或贴到聊天中。

npm has announced removal of direct publication with granular access tokens in
January 2027. Before that transition, migrate the release workflow to trusted
publishing or staged publishing; renewing this token alone will not preserve the
current direct-publish behavior. Configuring credentials does not publish or
reserve a package. See [npm token documentation](https://docs.npmjs.com/about-access-tokens/)
and [trusted publishing](https://docs.npmjs.com/trusted-publishers/).

npm 已公告将在 2027 年 1 月停止 granular token 的新版本直发能力；届时需要将
发布流程迁移到 trusted publishing 或 staged publishing，仅续期 Token 无法解决。
配置凭据不会发布或占用 npm 包名。

### Homebrew token / Homebrew 专用 Token

Use a fine-grained GitHub personal access token owned by `SivanCola`, restricted
to **only** `SivanCola/homebrew-deepzora`, with **Contents: Read and write** and the
automatically required **Metadata: Read-only** permission. No account, Actions,
administration, or workflow permission is needed. Set an expiration (for example,
90 days) and replace the secret before it expires. Save the token as the repository
Actions secret `HOMEBREW_TAP_TOKEN` in **SivanCola/deepzora**, where the release
workflows run. Do not save it in the tap repository or commit it to either repository.

创建 fine-grained PAT：资源所有者选 `SivanCola`，只选 `homebrew-deepzora` 仓库；
权限仅需 `Contents: Read and write` 和自动附带的 `Metadata: Read-only`。
设置有效期，在过期前更换 Deepzora 仓库中的 Secret。凭据保存在
`SivanCola/deepzora` 的 Settings → Secrets and variables → Actions，名称为
`HOMEBREW_TAP_TOKEN`，不要贴到聊天或提交进代码。

The tap's `main` branch must exist. The first stable release creates
`Casks/deepzora.rb`; later releases update that file. A tap with only a README and
license is ready for setup but does not offer an installable cask yet.

tap 需要已有 `main` 分支。首次正式稳定发布会创建 `Casks/deepzora.rb`，之后更新
该文件；仅完成 tap 和 Token 配置时，还不能通过 Homebrew 安装尚未发布的版本。

Repository Variables / 仓库变量:

- `MINISIGN_PUBLIC_KEY`: the single base64 key line from `deepzora.pub`, matching the
  private key above. It is public, not a secret. Desktop and privileged update
  helpers share the committed [Deepzora public key](../desktop/internal/update/deepzora.pub).
  Builds embed this key by default; `MINISIGN_PUBLIC_KEY` can override it at link
  time. Empty or invalid keys reject updates; the upstream key is never used.
- `RELEASE_TAG_ACTOR`: GitHub login for `RELEASE_TAG_TOKEN`.
- `SIGNPATH_RELEASE_SIGNING_ATTESTATION`: optional fingerprint only after your
  standalone Windows signing check succeeds; orchestrated same-run checks can
  provide their own verified evidence.

### Update signing key / 更新签名密钥

Deepzora's update signing key ID is **`98F231C2F91824B7`**. Its public key is
committed in `desktop/internal/update/deepzora.pub` and embedded by the shared
verification package, including local builds. Configure the same single base64
key line as the repository variable `MINISIGN_PUBLIC_KEY`; the CI signer checks
that its private key matches this public key before writing a signature.

Deepzora 更新签名密钥 ID 为 **`98F231C2F91824B7`**。公钥随项目提交，应用和更新助手
默认使用共享校验模块中嵌入的公钥，本地构建也生效。Actions 的 `MINISIGN_PUBLIC_KEY`
变量填同一把公钥的单行 base64；签名工具会先检查私钥与该公钥是否匹配。

Save the complete encrypted `deepzora.key` text as `MINISIGN_PRIVATE_KEY` and its
password as `MINISIGN_PASSWORD` in this repository's Actions Secrets. Use a
cryptographically random password. Keep a recovery copy of both outside Git in
owner-only storage (directory mode `0700`, private files `0600`) and back them up
in a secure password manager or offline store. GitHub does not return secret
values after saving them. Never commit or print private material.

将加密的 `deepzora.key` 完整文本保存到 `MINISIGN_PRIVATE_KEY`，随机密码保存到
`MINISIGN_PASSWORD`。恢复备份放在 Git 仓库外，仅允许当前用户读取；还应备份到
安全密码管理器或离线介质。GitHub 保存后无法取回 Secret 原文，私钥和密码不提交、不输出。

The repository's `desktop/cmd/sign` tool can generate a pair with
`go run ./cmd/sign genkey /a/private/directory`, with `MINISIGN_PASSWORD` supplied
only through the process environment. Do not generate a replacement for a
released key without a migration plan: existing clients trust the key embedded
in their installed version. MINISIGN authenticates update artifacts; Apple
notarization and Windows code signing still require their separate credentials.

可通过项目签名工具生成密钥，密码只经进程环境传入。首次发布后，不应随意重生成密钥：
已安装客户端只信任其内嵌公钥，换钥需要更新迁移方案。MINISIGN 负责更新产物验签，
macOS 公证与 Windows 代码签名仍需各自的凭据。

品牌重命名会改变发送给模型的静态系统提示和工具说明，因此第一次请求可能需要
重新建立缓存；新的静态前缀与 schema 已重录并通过稳定性测试，逐回合保持不变。

## GitHub updates / GitHub 更新

All releases and update artifacts use `SivanCola/deepzora`. The CLI reads
`cli-latest.json` from GitHub latest, falling back to the repository's Releases
API. Desktop reads `latest.json` from the same GitHub latest release. Stable
promotion validates the Desktop manifest, attaches both pointers to the matching
CLI release, and marks that release latest. Recovery cannot move latest backward
or overwrite a conflicting immutable manifest.

发布与更新不依赖上游域名、R2 或 Cloudflare。完成正式发布后，GitHub latest 同时
提供 CLI 与桌面更新指针；恢复旧版本不会回退最新版本或覆盖冲突文件。

`GITHUB_TOKEN` is provided by GitHub automatically. Optional maintenance secrets
are `DEEPSEEK_API_KEY` (live E2E and AI issue labels), `ACKNOWLEDGMENTS_TOKEN`
(acknowledgments PRs), and `STAR_HISTORY_GITHUB_TOKEN` (optional chart quota override;
otherwise the chart uses GitHub's automatic token). They are not
needed for ordinary compilation and tests.
Optional mirror diagnostics require your own `CLOUDFLARE_API_TOKEN` and
`CLOUDFLARE_ZONE_NAME`, `CLOUDFLARE_DOWNLOAD_HOST`, `CLOUDFLARE_MANIFEST_URL`
variables; mirror publishing additionally needs `R2_ACCESS_KEY_ID`,
`R2_SECRET_ACCESS_KEY`, `R2_ACCOUNT_ID`, `R2_BUCKET`, and `R2_PUBLIC_BASE`.
Leave `ENABLE_R2_MIRROR` unset for the GitHub-only update channel. Upstream artifact-run reuse and unsigned legacy exceptions are rejected.
The legacy R2 mirror is opt-in only
and is not required by Deepzora's GitHub update channel.

Crash/metrics uploads are disabled until explicitly configured via
`DEEPZORA_REPORT_ENDPOINT`, `DEEPZORA_TELEMETRY_ENDPOINT`, `DEEPZORA_PING_ENDPOINT`, or
`DEEPZORA_METRICS_ENDPOINT`. These optional backends are yours to operate; no
upstream collector or guessed Deepzora domain is contacted by default.

崩溃报告仍可本地保存；未配置自有服务时不上传。此代码修改不等于发布已签名软件：
证书、npm/tap 权限、受保护分支及跨平台正式产物验收须在首次发布前完成。

## Deepzora rename and CI / 改名与 CI

The canonical homepage is [deepzora.com](https://deepzora.com). See
[domain setup](DOMAIN.md) for Tencent Cloud DNSPod and the pending hosting target.
Releases and update pointers stay on GitHub until a download service exists.

Root and desktop Go modules, commands, npm package names, installers, app ID,
project files, user directories, protocol handlers, release links, and tap links
use Deepzora. The existing independent MINISIGN public key is retained byte for
byte. Secret names and the owner scope `@sivancola` remain unchanged.

明确导入的旧 Deeper 会话及 `deeper-plugin.json` / `deeper.io/plugin/v2`
仍可读取；新数据使用 Deepzora 标识，不自动搬迁旧用户目录。中断的 Deeper
会话清理仅接受精确匹配会话 ID 的旧凭据，未知格式仍拒绝。

Ordinary CI retains the seven protected check names, functional regressions,
platform checks, startup budgets, CodeQL, and update/signing/publication guards.
The duplicate full-Go coverage report (without a threshold or report consumer)
and standalone timing job were removed. PR-body cache/docs formatting guards
and upstream v1/v2/v3 labeling were removed. App memory soak and diagnostic
performance monitoring remain available by manual dispatch; they no longer run
on every PR, main push, or daily schedule. Functional ownership regressions stay
in ordinary CI. Automatic star-history commits were removed.
