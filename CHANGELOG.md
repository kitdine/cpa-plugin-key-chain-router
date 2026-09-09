# Changelog

## v0.3.0 - 2026-09-09

首个正式发布版本。

### 路由能力

- 保留 CPA 原生下游 `api-keys` 认证与 usage / request log 归属。
- 每个下游 API Key 可配置独立的有序 failover 链。
- 候选可混合 OAuth credential 与 API Provider credential。
- 通过 AuthIndex 精确锁定具体 credential。
- 支持 401/403/408/409/429/5xx 等状态自动 failover。
- Streaming 仅在首 chunk 之前允许切换候选。

### 模型规则

- 路由匹配模型支持 `*`、`?` 与逗号分隔多个规则。
- 不再要求客户端固定使用 `model=code`。
- 候选覆盖模型/alias 可留空；留空时继承客户端原始模型。

### 管理界面

- 中文管理界面。
- 自动读取当前 CPA config 中的下游 API Key 与 API Provider。
- OAuth/Auth 通过 `host.auth.list` 获取。
- 无需在页面填写 Management Secret。
- 提供路由静态诊断、可复制 curl，以及最近一次真实请求的候选尝试轨迹。

### 构建与安装

- GitHub Actions 使用 Go 1.26 + Debian Bookworm 构建 Linux amd64 c-shared 插件。
- 发布普通 `.so`、完整 tar.gz 包、SHA256SUMS。
- 同时发布 CPA Plugin Store 所需的 `key-chain-router_0.3.0_linux_amd64.zip` 与 `checksums.txt`。
