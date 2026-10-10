# Deeper independence / Deeper 独立维护

Deeper is independently maintained at [SivanCola/deeper](https://github.com/SivanCola/deeper).
It retains the MIT license and upstream authorship in [NOTICE](../NOTICE).
`release-notes/`, `CHANGELOG.md`, and recorded historical evidence describe upstream
history; retaining them does not authorize publishing a Deeper release with the
same historical identity. Prepare and review new Deeper release notes first.

Deeper 在自己的仓库独立维护；原 MIT 许可证、提交作者及历史记录完整保留。
历史发布说明仍属于上游历史，不代表 Deeper 已发布相同版本。

| Surface / 项目 | Deeper identity / 独立身份 |
| --- | --- |
| Repository / 仓库 | `SivanCola/deeper`, branch `main` |
| Product / 应用名称 | `Deeper` |
| CLI / 命令 | `deeper` (distinct from the Nix package manager) |
| Desktop application ID | `io.github.sivancola.deeper` |
| macOS/Linux home / 数据目录 | `~/.deeper` |
| Windows home / 数据目录 | `%APPDATA%/deeper` |
| Explicit home / 自定义目录 | `DEEPER_HOME`, `DEEPER_STATE_HOME`, `DEEPER_CACHE_HOME` |
| Project settings / 项目设置 | `.deeper/`, `deeper.toml`, `DEEPER.md` |
| npm root / npm 主包 | `@sivancola/deeper` |
| npm platform packages / 平台包 | `@sivancola/deeper-cli-{darwin,linux,win32}-{arm64,x64}` |
| Homebrew release tap / 发布 tap | `SivanCola/homebrew-deeper` |

Deeper does not read `REASONIX_*` overrides or migrate `~/.reasonix` automatically.
Configuration serialization remains compatible for an intentional manual copy,
but configuration, credentials, sessions, app locks, shortcuts, caches, update
helpers, protocol names, and desktop browser profiles have distinct identities.
Install both applications separately; do not point their home overrides at the
same directory. Windows MSI/NSIS upgrade identity and Linux Polkit policy use the
Deeper namespace.

Deeper 不自动读取 Reasonix 环境变量或搬迁其用户数据。两者可以分别安装。
如需主动复制配置，应先备份并人工选择文件，不共享运行中的数据目录。

## CI and release setup / CI 和发布配置

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

For the standard release pipeline, create the separate `SivanCola/homebrew-deeper`
repository and obtain publishing rights for the npm `@sivancola` user or organization.
普通 CI 和本次代码推送不需要这些发布凭据；首次正式发布前需要准备自有 tap 仓库、npm scope、
签名证书，以及下表中的 Secrets 和 Variables。

| Secret / 凭据 | Required for / 用途 |
| --- | --- |
| `MINISIGN_PRIVATE_KEY` | Deeper's encrypted artifact signing key / 加密产物签名私钥 |
| `MINISIGN_PASSWORD` | Password for that key / 私钥密码 |
| `APPLE_CERT_P12` | Base64 Developer ID Application certificate / macOS 签名证书 |
| `APPLE_CERT_PASSWORD` | P12 password / 证书密码 |
| `APPLE_API_KEY_P8` | Base64 App Store Connect API private key / 公证 API 私钥 |
| `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID` | Notarization identity / 公证 API 身份 |
| `CERTUM_USERNAME`, `CERTUM_OTP_URI`, `CERTUM_KEY_ID` | Your Certum SimplySign account, OTP URI and certificate SHA-1 thumbprint / Windows 签名 |
| `NPM_TOKEN` | Publishing rights for the seven `@sivancola/deeper*` packages / npm 发布权限 |
| `HOMEBREW_TAP_TOKEN` | Write access to your separate `homebrew-deeper` tap / 自有 tap 写入权限 |
| `RELEASE_TAG_TOKEN` | Dedicated token allowed by your tag rules and allowed to read attestations / 发布标签身份 |

### npm account and token / npm 账号与 Token

The npm user `sivancola` owns the personal scope `@sivancola`; a separate npm
organization is not needed for the configured seven public CLI packages. Save a
dedicated granular access token as the repository Actions secret `NPM_TOKEN` in
`SivanCola/deeper`. Limit package access to the `@sivancola` scope for initial
publication, leave organization access disabled, and set an expiration. Scope
access covers its current and future packages; after the first real release, a
replacement token can instead select only the seven Deeper packages.

npm 账号 `sivancola` 的个人 scope 是 `@sivancola`，当前七个公开 CLI 包无需另建组织。
创建专用 granular access token，并保存在 Deeper 仓库的 `NPM_TOKEN` Secret。
首次发布前可只授权 `@sivancola` scope，组织权限保持 No access，并设置有效期。
scope 授权涵盖其中现有及未来的包；首次真实发布后，可换成仅授权七个 Deeper 包的 Token。

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
to **only** `SivanCola/homebrew-deeper`, with **Contents: Read and write** and the
automatically required **Metadata: Read-only** permission. No account, Actions,
administration, or workflow permission is needed. Set an expiration (for example,
90 days) and replace the secret before it expires. Save the token as the repository
Actions secret `HOMEBREW_TAP_TOKEN` in **SivanCola/deeper**, where the release
workflows run. Do not save it in the tap repository or commit it to either repository.

创建 fine-grained PAT：资源所有者选 `SivanCola`，只选 `homebrew-deeper` 仓库；
权限仅需 `Contents: Read and write` 和自动附带的 `Metadata: Read-only`。
设置有效期，在过期前更换 Deeper 仓库中的 Secret。凭据保存在
`SivanCola/deeper` 的 Settings → Secrets and variables → Actions，名称为
`HOMEBREW_TAP_TOKEN`，不要贴到聊天或提交进代码。

The tap's `main` branch must exist. The first stable release creates
`Casks/deeper.rb`; later releases update that file. A tap with only a README and
license is ready for setup but does not offer an installable cask yet.

tap 需要已有 `main` 分支。首次正式稳定发布会创建 `Casks/deeper.rb`，之后更新
该文件；仅完成 tap 和 Token 配置时，还不能通过 Homebrew 安装尚未发布的版本。

Repository Variables / 仓库变量:

- `MINISIGN_PUBLIC_KEY`: the single base64 key line from `deeper.pub`, matching the
  private key above. It is public, not a secret. Packaged Desktop and privileged
  update helpers embed it at build time. An unconfigured build rejects updates;
  it never inherits the upstream public key.
- `RELEASE_TAG_ACTOR`: GitHub login for `RELEASE_TAG_TOKEN`.
- `SIGNPATH_RELEASE_SIGNING_ATTESTATION`: optional fingerprint only after your
  standalone Windows signing check succeeds; orchestrated same-run checks can
  provide their own verified evidence.

Generate your signing pair locally from `desktop/` with
`go run ./cmd/sign genkey /a/private/directory`, setting `MINISIGN_PASSWORD` in
the process environment. Keep `deeper.key` private. The public variable must
contain only the base64 key line, without the comment or whitespace.

私钥只保存在安全位置和 GitHub Secrets。公钥变量填 `.pub` 文件中的单行 base64。
构建时嵌入公钥，更新时保留签名、哈希、版本和来源校验。

品牌重命名会改变发送给模型的静态系统提示和工具说明，因此第一次请求可能需要
重新建立缓存；新的静态前缀与 schema 已重录并通过稳定性测试，逐回合保持不变。

## GitHub updates / GitHub 更新

All releases and update artifacts use `SivanCola/deeper`. The CLI reads
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
and is not required by Deeper's GitHub update channel.

Crash/metrics uploads are disabled until explicitly configured via
`DEEPER_REPORT_ENDPOINT`, `DEEPER_TELEMETRY_ENDPOINT`, `DEEPER_PING_ENDPOINT`, or
`DEEPER_METRICS_ENDPOINT`. These optional backends are yours to operate; no
upstream collector or guessed Deeper domain is contacted by default.

崩溃报告仍可本地保存；未配置自有服务时不上传。此代码修改不等于发布已签名软件：
证书、npm/tap 权限、受保护分支及跨平台正式产物验收须在首次发布前完成。
