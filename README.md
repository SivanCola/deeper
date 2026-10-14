# Deepzora

Deepzora is an AI coding assistant with a CLI, an Electron desktop app, a remote Serve service, ACP integration, and an SDK. It is derived from DeepSeek-Reasonix; upstream authorship is recorded in [NOTICE](NOTICE), and the original MIT license is preserved in [LICENSE-MIT](LICENSE-MIT).

Deepzora 是一个 AI 编程助手，提供 CLI、Electron 桌面端、远程 Serve 服务、ACP 集成和 SDK。项目源自 DeepSeek-Reasonix，上游署名见 [NOTICE](NOTICE)，原 MIT 许可完整保留在 [LICENSE-MIT](LICENSE-MIT)。

## License and brand / 许可与品牌

Deepzora is distributed as a combined work under [AGPL-3.0-only](LICENSE).
Commercial use and independent forks are allowed subject to the license,
including its requirements for Corresponding Source on distribution and modified
network services. Upstream MIT permissions and explicit component licenses remain
in effect; see [LICENSING.md](LICENSING.md). The name, logos and official release
identity have a separate [brand policy](TRADEMARK_POLICY.md).

Deepzora 整体作品采用 [AGPL-3.0-only](LICENSE)，允许遵守协议的商用与独立 fork，
分发及修改后的网络服务须履行适用的对应源码提供义务。上游 MIT 权限与组件独立
许可继续有效，详见[许可说明](LICENSING.md)。名称、Logo 与官方发行身份另受
[品牌政策](TRADEMARK_POLICY.zh-CN.md)约束。

This repository treats the current implementation as its first-version baseline. Historical Reasonix data formats, old desktop interfaces, and migration paths are unsupported. The first Deepzora release is being prepared; see [Releases](https://github.com/SivanCola/deepzora/releases) for published artifacts.

本仓库以当前实现作为首版基线，不支持 Reasonix 历史数据格式、旧桌面接口及迁移路径。Deepzora 首版正在准备中，实际发布产物以 [Releases](https://github.com/SivanCola/deepzora/releases) 为准。

## Code signing policy / 代码签名政策

- [Code signing policy](CODE_SIGNING_POLICY.md) · [代码签名政策](CODE_SIGNING_POLICY.zh-CN.md)
- [Privacy policy](PRIVACY.md) · [隐私政策](PRIVACY.zh-CN.md)
- [Independent SignPath review](docs/SIGNPATH_REVIEW.md) · [独立 SignPath 审核材料](docs/SIGNPATH_REVIEW.zh-CN.md)

The independent SignPath application is pending review; signing authorization and CI integration are not yet in place.

独立 SignPath 申请仍待审核，尚未获得签名授权或完成 CI 接入。

## Build / 构建

Use the Go version in [go.mod](go.mod), the Node version in [.node-version](.node-version), and the pnpm version in [desktop/frontend/package.json](desktop/frontend/package.json).

使用 [go.mod](go.mod) 指定的 Go、[.node-version](.node-version) 指定的 Node，以及 [desktop/frontend/package.json](desktop/frontend/package.json) 指定的 pnpm 版本。

```sh
npm install -g pnpm@10.34.5
go build -o deepzora ./cmd/deepzora
cd desktop
pnpm install --frozen-lockfile
pnpm --dir frontend build:electron
go run . -emit-contract frontend/src/generated
go build -o build/bin/deepzora-desktop-service .
pnpm --dir electron build
```

The npm package name is `@sivancola/deepzora`. Install it only after a corresponding release is available.

npm 包名为 `@sivancola/deepzora`，请在对应版本实际发布后再安装。

## Documentation / 文档

- [English guide](docs/GUIDE.md) · [中文指南](docs/GUIDE.zh-CN.md)
- [CLI](docs/CLI.md) · [CLI 中文](docs/CLI.zh-CN.md)
- [Remote sessions](docs/REMOTE_SESSIONS.md) · [远程会话](docs/REMOTE_SESSIONS.zh-CN.md)
- [Current baseline / 当前基线](docs/current-version-baseline.md)
- [Optional Supabase feedback and crash-report backend (中文)](supabase/README.md)
- [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)
- [Release candidates](docs/RELEASE_CANDIDATES.md) · [发布候选](docs/RELEASE_CANDIDATES.zh-CN.md)

Run `go test ./...` at the root and in `desktop`, and `pnpm --dir desktop/frontend test:all` for frontend checks. Research and browser benchmarks remain available as explicit development commands.

在根目录与 `desktop` 内运行 `go test ./...`，前端运行 `pnpm --dir desktop/frontend test:all`。研究工具和浏览器基准通过显式开发命令运行。
