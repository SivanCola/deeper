# Releasing Deepzora / 发布 Deepzora

The first public release is `0.0.1`. Review bilingual notes in `release-notes/releases.json` before preparing a candidate. Only official `MAJOR.MINOR.PATCH` versions are supported.

首个公开版本为 `0.0.1`。构建候选前需审阅 `release-notes/releases.json` 中的双语发布记录，仅支持正式 `MAJOR.MINOR.PATCH` 版本。

## Desktop download names / 桌面下载命名

All public Desktop downloads use `Deepzora-VERSION-OS-ARCH[-PURPOSE].EXT`, for example `Deepzora-0.0.2-macOS-arm64.dmg`, `Deepzora-0.0.2-Windows-x64-Setup.exe`, `Deepzora-0.0.2-Windows-arm64-Portable.zip`, and `Deepzora-0.0.2-Linux-x64.deb`. `x64` includes Intel and AMD; macOS `arm64` means Apple Silicon. The shared catalog is `internal/releaseassets/desktop.json`.

所有版本的桌面产物均使用 `Deepzora-版本号-系统-架构[-用途].扩展名`。`x64` 包括 Intel 和 AMD，macOS `arm64` 对应 Apple Silicon。发布说明自动生成按设备分类的下载表，普通用户优先使用表中链接。

Every release, including v0.0.1, publishes only versioned Desktop filenames. The build stages use internal build names and finalize them with `scripts/desktop-downloads.mjs prepare` before signing. `latest.json` requires `artifact_naming: versioned-v1`; platform and native-package routes point directly at the same signed assets listed in downloads. Unversioned download aliases and historical updater routing are unsupported.

包括 v0.0.1 在内的所有版本只发布带版本号的桌面文件。构建阶段的内部文件名在签名前通过 `scripts/desktop-downloads.mjs prepare` 重命名。更新清单必须声明 `artifact_naming: versioned-v1`，平台及原生安装包路由直接指向下载表中的同一份签名资产；不再生成无版本号的发布别名，也不支持旧更新器路由。

Homebrew currently installs the **CLI** via `Casks/deepzora.rb`; its lowercase `deepzora-OS-ARCH.tar.gz` archives belong to `vVERSION`, not `desktop-vVERSION`. Desktop naming does not change CLI archive names, npm package names or internal platform keys (`darwin-amd64`, etc.). Postflight checks all four Homebrew URLs and SHA-256 values against the published CLI `SHA256SUMS`.

Homebrew 当前安装的是 **CLI**，引用 `vVERSION` 下的小写 `deepzora-OS-ARCH.tar.gz`，不能替换为桌面 DMG 或 `desktop-vVERSION`。此次桌面命名调整不改变 CLI 归档名、npm 包名及内部平台键。发布后检查会核对 Homebrew 四个平台的 URL 与公开 CLI 校验和。

1. Review and merge the release notes on protected `main`.
2. Run **Prepare release candidate** with the reviewed version. It freezes source identity, builds current platforms, signs desktop payloads, verifies native startup and installers, and seals artifact hashes and provenance.
3. Run `./scripts/release-stable.sh CANDIDATE_ID` from an authenticated maintainer checkout. Review the immutable candidate and approve its `release` environment.
4. **Publish release candidate** creates CLI `vX.Y.Z`, npm `npm-vX.Y.Z`, and Desktop `desktop-vX.Y.Z` tags atomically, then publishes the exact sealed files.
5. Require successful public artifact, package, pointer, and Homebrew verification and retain the publication ledger.

1. 在受保护的 `main` 审阅并合并发布记录。
2. 使用已审阅版本运行 **Prepare release candidate**，固定来源，构建当前平台、签名桌面产物、验证原生启动和安装器，封存哈希与来源证明。
3. 维护者运行 `./scripts/release-stable.sh CANDIDATE_ID`，审阅不可变候选并批准 `release` 环境。
4. **Publish release candidate** 原子创建 CLI、npm 和 Desktop 三个正式标签，发布封存文件。
5. 确认公开产物、npm 包、更新指针和 Homebrew 验证成功，保存发布账本。

For an interrupted publication, run `./scripts/release-stable.sh CANDIDATE_ID recover`. Recovery queries actual public state, reuses only matching content, uploads missing content, and preserves newer pointers. Partial tag sets, conflicting hashes, expired unpublished candidates, or revoked candidates stop publication. Never move an existing release tag or rebuild a sealed payload during recovery.

发布中断时运行 `./scripts/release-stable.sh CANDIDATE_ID recover`。恢复查询实际公开状态，仅复用匹配内容、补齐缺失内容，并保留更新的指针。标签不完整、哈希冲突、未发布候选过期或候选撤销时停止发布。不要移动已存在标签或在恢复时重构封存产物。

See [candidate contracts](RELEASE_CANDIDATES.md) ([中文](RELEASE_CANDIDATES.zh-CN.md)) and [Windows signing](CERTUM_SIGNING.md). Manual signing and notarization diagnostics remain available. SDK releases retain their separate `sdk/go/vX.Y.Z` tags.

详见候选契约和 Windows 签名文档。保留手动签名与公证诊断；SDK 发布继续使用独立标签。
