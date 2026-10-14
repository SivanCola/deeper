# Deepzora licensing / Deepzora 许可说明

License change date: 2026-10-14 / 许可变更日期：2026-10-14。

## Project license / 项目许可证

Deepzora is distributed as a combined work under the **GNU Affero General Public
License, version 3 only (`AGPL-3.0-only`)**. Unless a file or component explicitly
states a different license, this is the license for Deepzora's original code and
new contributions. The unmodified license text is in [LICENSE](LICENSE).
Copyright holders remain the respective authors; this change does not transfer
copyright or grant the maintainer exclusive relicensing rights.

Deepzora 整体作品采用 **GNU Affero General Public License 第 3 版，仅此版本
（`AGPL-3.0-only`）**。除文件或组件另有明确许可外，Deepzora 原创代码和新贡献
采用此协议，完整原文见 [LICENSE](LICENSE)。版权仍属于各作者；此次变更不转移
版权，也不赋予维护者独占的再许可权。

## Upstream and component licenses / 上游与组件许可

Deepzora derives from DeepSeek-Reasonix. Its original MIT copyright notice and
license are preserved **verbatim** in [LICENSE-MIT](LICENSE-MIT), and provenance
is recorded in [NOTICE](NOTICE). The inherited upstream portions remain
available under MIT. The AGPL license for the combined work does not revoke
those permissions or permissions already granted for earlier MIT releases.
It does not make Deepzora's AGPL-only additions available under MIT.

Deepzora 源自 DeepSeek-Reasonix，其原 MIT 版权与许可声明**原文保留**在
[LICENSE-MIT](LICENSE-MIT)，来源记录见 [NOTICE](NOTICE)。继承的上游部分仍可按
MIT 使用；整体作品采用 AGPL 不撤销这些权限，也不撤销旧 MIT 版本已经授予的
权限。Deepzora 新增的 AGPL 专属代码不因此获得 MIT 授权。

Explicit component licenses remain in effect, including:

- Harness chat upstream notices in
  [its LICENSE](desktop/frontend/src/components/harness-chat/LICENSE).
- Provider icons under [their license notices](desktop/frontend/public/provider-icons/SOURCE.md),
  and Seti file icons under [their notices](desktop/frontend/src/assets/file-icons/seti/THIRD_PARTY_NOTICES.txt).
- Official theme artwork and theme-pack data explicitly licensed MIT as
  documented in [THEME_ASSETS.md](docs/THEME_ASSETS.md), and other theme packs
  under their declared licenses. This exception does not cover application code
  merely because it renders or generates themes.
- Dependencies and bundled runtimes under their respective licenses; their
  notices must be retained when required.

各组件明确声明的许可继续有效，包括 Harness 上游 MIT 声明、供应商和 Seti 图标
许可、明确标注 MIT 的官方主题素材与主题包数据，以及依赖和运行时各自的协议。
主题素材例外不延伸到仅因渲染或生成主题而相关的应用代码。分发时须保留适用的
第三方声明；本清单不替代逐次发布的依赖与许可核查。

## Distribution and network use / 分发与网络使用

Commercial use, paid redistribution and independent forks are allowed subject
to the applicable licenses. When distributing covered binaries, provide the
Corresponding Source using a method permitted by AGPL section 6, including the
necessary build and installation scripts and applicable notices. An unrelated
or newer source snapshot is not a substitute for the source of the binary.
For official releases, identify the exact source commit and provide clear
source-download directions alongside the binary downloads.

If you modify the covered program and users interact with it remotely over a
network, AGPL section 13 requires a prominent offer to those users to obtain
the Corresponding Source of that version, at no charge, through a standard
means of copying software. A clearly visible source link in the service's
interface is one way to do this. Publishing changes as a pull request to
Deepzora is welcome but is not required by the license.

商用、收费分发与独立 fork 均可在遵守适用许可的前提下进行。分发受协议覆盖的
二进制时，须按 AGPL 第 6 条提供对应源码，包含必要的构建、安装脚本与许可声明；
不相关或较新的源码不能替代该二进制的对应源码。正式发布须标明准确源码提交，
并在二进制下载入口旁提供明确的源码下载指引。

修改受协议覆盖的程序后，若用户通过网络远程交互，须按 AGPL 第 13 条向这些
用户显著提供免费获取该版本对应源码的方式，例如服务界面中的显著源码链接。
欢迎向 Deepzora 提交修改，但协议不要求必须向本仓库提交 PR。

The software is provided without warranty as stated in its licenses. This
summary does not amend the license texts. The separate
[brand policy](TRADEMARK_POLICY.md) ([中文](TRADEMARK_POLICY.zh-CN.md)) governs
brand use and does not add restrictions to the software license.

软件按许可证约定不提供担保。本说明不修改许可证原文。
[品牌政策](TRADEMARK_POLICY.zh-CN.md)独立约束品牌使用，不对软件许可追加限制。
