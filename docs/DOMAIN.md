# deepzora.com

Deepzora's canonical homepage is `https://deepzora.com`. The domain is managed
through Tencent Cloud DNSPod. Owning the domain does not yet provide a website
or download server.

DNS 在腾讯云 DNSPod 管理。选定网站托管平台后，在该平台绑定 `deepzora.com`
和 `www.deepzora.com`，再把平台提供的 A/AAAA 或 CNAME 记录填入 DNSPod。
根域名的记录类型以托管平台的要求为准；不要凭空填写 IP。完成域名验证后，
在托管平台启用 HTTPS，并将 `www` 重定向到根域名。

当前决定暂不建站，仓库没有独立网站源码，DNS 无需配置。应用下载、发布和更新指针
继续使用 `SivanCola/deepzora` 的 GitHub Releases，Homebrew 使用
`SivanCola/homebrew-deepzora`，避免依赖尚未接入的下载域名。未来接入下载镜像
时须同时验证 HTTPS、更新签名和旧版本的更新路径。

GitHub/npm/Homebrew 凭据继续由原有 Secrets 提供；仓库更名不需要重新生成
MINISIGN 密钥。不要把 DNSPod 的 SecretId/SecretKey 写入源码或聊天。
