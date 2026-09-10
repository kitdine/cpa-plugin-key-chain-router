# Changelog

## v0.5.0 - 2026-09-10

修复 v0.4.0 的 credential 调度错误，并收紧策略配置界面的字段展示。

### 调度修复

- Scheduler 不再把 `Candidates[].id` 当作运行时 `AuthID`。
- KCR ticket 继续保存稳定的 `AuthIndex + Provider`。
- `scheduler.pick` 收到 ticket 后实时调用 `host.auth.list`，通过 `AuthIndex` 找到真实 `files[].id` 并返回该 `AuthID`。
- `AuthIndex` 重复时使用 Provider/Type 消歧；无法唯一解析时 fail closed，避免 CPA 默认 scheduler 静默选到其他 credential。
- ABI smoke 增加回归场景：scheduler candidate ID 故意与真实 AuthID 不一致，确保只能通过 `AuthIndex → host.auth.list → AuthID` 路径成功。

### 管理界面

- `ordered-failover` / `round-robin` 隐藏 Priority 与 Weight。
- `weighted-round-robin` 仅显示 Weight。
- `priority-weighted` / `sticky` 显示 Priority 与 Weight。
- Policy 概览和诊断表同步隐藏当前 Strategy 不使用的字段。

### 发布与测试

- ABI smoke 不再写死 v0.4.x，改为校验当前构建版本。
- `go test ./...`、`go vet`、JavaScript syntax、Linux amd64 build、ABI smoke 与 binary inspection 全部纳入发布验证。

## v0.4.0 - 2026-09-09

引入 Policy / Rule 动态策略引擎。

- 一个下游 API Key 对应一个 Policy，可包含多条模型 Rule。
- 支持精确 model、glob 与 `*` catch-all，并按特异性决定命中规则。
- 新增 ordered failover、round robin、smooth weighted round robin、priority + weight、sticky / weighted rendezvous hash、CPA default 等策略。
- 支持按失败类型配置 failover 动作和最大尝试次数。
- 新增路由决策事件、内存记录、CPA 日志、可选 SQLite 持久化与调试响应 Header。
- v0.3 state 自动迁移为 v0.4 Policy / Rule 结构。

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
