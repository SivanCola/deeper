# Releasing Deepzora / 发布 Deepzora

The first release is prepared as `1.0.0`; it has not been published. Review bilingual notes in `release-notes/releases.json` before preparing a candidate. Only official `MAJOR.MINOR.PATCH` versions are supported.

首版以 `1.0.0` 准备，尚未发布。构建候选前需审阅 `release-notes/releases.json` 中的双语发布记录，仅支持正式 `MAJOR.MINOR.PATCH` 版本。

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
