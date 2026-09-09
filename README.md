# CPA Key Chain Router v0.4.0

CLIProxyAPI（CPA）v7 动态策略路由插件。保留 CPA 原生下游 `api-keys` 认证、usage 和请求监控，仅在认证后根据下游 API Key、模型和策略选择上游 OAuth / API credential。

当前重点兼容：CLIProxyAPI v7.2.154（schema 5）、Linux amd64 / Debian Bookworm 类环境。

## v0.4.0 核心模型

一个下游 API Key **只能对应一个 Policy**：

```text
API Key
  └─ Policy（唯一）
      ├─ Rule: gpt-5.6-luna
      ├─ Rule: gpt-5.6-sol
      ├─ Rule: claude-*
      └─ Rule: *
```

Rule 匹配优先级固定为：精确 model > glob (`*` / `?`) > `*` catch-all。一个 Policy 内同一个精确 model 不能重复，且最多只能有一个 `*` Rule。

旧 v0.3 state 会自动迁移：同一 API Key 的旧 Route 合并成一个 Policy，每条旧 Route 变成一个 Rule，候选保持 `ordered-failover`。

## 调度策略

每条 Rule 可独立选择：

- `ordered-failover`：按候选顺序执行 A → B → C。
- `round-robin`：轮询首选候选，失败后继续后续候选。
- `weighted-round-robin`：Smooth Weighted Round Robin，长期按 Weight 平滑分配。
- `priority-weighted`：先选择最高 Priority 组，同组按 Weight 平滑分配；失败后可继续同组或降级到下一 Priority。
- `sticky`：Weighted Rendezvous Hash；相同 session/header 尽量稳定命中同一 credential，有利于上游 cache。
- `cpa-default`：明确不由 KCR 接管，交还 CPA 默认 router。

候选字段：

```text
Priority       数值越大优先级越高
Weight         同策略/同优先级内的相对流量权重
Override Model 留空时继承客户端原始 model；填写时仅该候选覆盖 model/alias
```

## Failover 策略

每个 Rule 可分别定义这些失败类别的行为：

- network error
- 401 / 403
- 408
- 409
- 429
- 5xx
- 其他错误

可选动作：

- `next`：下一候选
- `same-priority-first`：先尝试同 Priority 的其他候选，再降级
- `next-priority`：直接跳到更低 Priority
- `stop`：停止 failover
- `cpa-default`：转 CPA 默认路由

候选全部耗尽后还可选择返回错误或 `cpa-default`。`max_attempts=0` 表示自动按候选数决定。

## 明确知道“插件是否生效”

v0.4 把路由决策变成一等事件，每次请求只会显示三种决策之一：

```text
KCR_HANDLED              插件接管并按 Policy/Rule 执行
KCR_FALLBACK_TO_CPA      插件已接管，但按 Failover 规则最终转 CPA 默认路由
KCR_BYPASS_CPA_DEFAULT   未命中 Policy/Rule，或 Rule 明确为 cpa-default
```

管理页“最近路由记录”会显示：

```text
时间 / API Key hint / model / Policy / Rule / Strategy
候选尝试链 / status / latency / 最终 resource / Provider / AuthIndex
```

CPA 日志默认也会写入 `kcr routing decision`，包含 decision、rule、strategy、provider、auth_index、attempts 和 duration_ms。

### 可选调试响应 Header

默认关闭，不污染客户端响应。开启后，仅返回：

```text
X-KCR-Decision
X-KCR-Trace-ID
X-KCR-Rule
```

不会返回 API secret / OAuth token。内部精确 AuthIndex 选择仍使用当前 CPA 能力下的一次性 `X-CPA-Key-Chain-Ticket` 调度票据；票据由 scheduler 单次消费。

## 可观测性

默认：

```text
Memory Ring Buffer  ON   500 条
CPA host.log        ON   info
SQLite              OFF
Response Headers    OFF
```

SQLite 可在中文页面开启。数据库采用 WAL + busy timeout，由异步 writer goroutine 写入；写库队列满时丢弃观测事件并告警，不阻塞模型请求。

SQLite 表：

- `routing_events`：一条请求的最终决策
- `routing_attempts`：该请求的每次候选尝试

只保存路由元数据，不保存 Prompt、请求/响应正文、完整 API Key、上游 API secret 或 OAuth Token。支持保留天数和最大记录数自动清理。

## Sticky

`sticky` 使用 Weighted Rendezvous Hash，不维护 session → provider 持久映射。

默认 `auto` 会尝试：

- metadata 中的 `session_id` / `sessionId` / `trace_id`
- `X-Claude-Code-Session-Id`
- `X-Session-Id`
- `Session-Id`
- `X-Request-Id`

也可指定自定义 Header。

## 安装

### CPA Plugin Store

将自定义源加入 CPA：

```yaml
plugins:
  enabled: true
  dir: plugins
  store-sources:
    - "https://raw.githubusercontent.com/kitdine/cpa-plugin-key-chain-router/main/registry.json"
```

重载 CPA 后，在 Plugin Store 搜索 **Key Chain Router** 并安装。Release 使用 CPA 要求的：

```text
key-chain-router_<version>_linux_amd64.zip
checksums.txt
```

### 手工安装

将 Release 中：

```text
key-chain-router-v0.4.0.so
```

放入：

```text
plugins/linux/amd64/
```

配置示例见 `config.example.yaml`，然后重启 CPA。

管理页：

```text
/v0/resource/plugins/key-chain-router/status
```

## 开发 / CI

GitHub Actions 使用 `golang:1.26-bookworm`，执行：

```text
go test ./...
go vet ./...
node --check ui.js
c-shared build
ABI smoke
binary inspection
artifact packaging
```

正式 Release 自动输出：

```text
key-chain-router-vX.Y.Z.so
key-chain-router.so
key-chain-router-vX.Y.Z-linux-amd64.tar.gz
SHA256SUMS
key-chain-router_X.Y.Z_linux_amd64.zip
checksums.txt
```

## 安全边界

- 插件不接管 CPA 下游认证；API Key 必须仍存在于 CPA 原生 `api-keys`。
- state 只保存下游 Key 的 SHA-256 fingerprint / hint，不保存明文 Key。
- 插件资源页应仅暴露在可信网络。
- SQLite/内存/logging 失败不得影响模型路由。
