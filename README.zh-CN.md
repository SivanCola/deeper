# Deepzora

Deepzora 是一个 AI 编程助手，提供 CLI、Electron 桌面端、远程 Serve 服务、ACP 集成和 SDK。项目源自 DeepSeek-Reasonix，上游作者与许可信息保留在 [NOTICE](NOTICE) 和 [LICENSE](LICENSE)。

本仓库以当前实现作为首版基线，不支持 Reasonix 历史数据格式、旧桌面接口及迁移路径。Deepzora 首版正在准备中，实际发布产物以 [Releases](https://github.com/SivanCola/deepzora/releases) 为准。

## Code signing policy / 代码签名政策

- [代码签名政策](CODE_SIGNING_POLICY.zh-CN.md) · [English](CODE_SIGNING_POLICY.md)
- [隐私政策](PRIVACY.zh-CN.md) · [English](PRIVACY.md)
- [独立 SignPath 审核材料](docs/SIGNPATH_REVIEW.zh-CN.md) · [English](docs/SIGNPATH_REVIEW.md)

独立 SignPath 申请仍待审核，尚未获得签名授权或完成 CI 接入。

## Build / 构建

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

npm 包名为 `@sivancola/deepzora`，请在对应版本实际发布后再安装。

## Documentation / 文档

- [English guide](docs/GUIDE.md) · [中文指南](docs/GUIDE.zh-CN.md)
- [CLI](docs/CLI.md) · [CLI 中文](docs/CLI.zh-CN.md)
- [Remote sessions](docs/REMOTE_SESSIONS.md) · [远程会话](docs/REMOTE_SESSIONS.zh-CN.md)
- [Current baseline / 当前基线](docs/current-version-baseline.md)
- [Supabase 反馈与崩溃报告接收服务](supabase/README.md)
- [Contributing](CONTRIBUTING.md) · [Security](SECURITY.md)
- [Release candidates](docs/RELEASE_CANDIDATES.md) · [发布候选](docs/RELEASE_CANDIDATES.zh-CN.md)

在根目录与 `desktop` 内运行 `go test ./...`，前端运行 `pnpm --dir desktop/frontend test:all`。研究工具和浏览器基准通过显式开发命令运行。
