# CPA Key Chain Router

CLIProxyAPI（CPA）v7 的动态策略路由插件。

它保留 CPA 原生的下游 `api-keys` 认证、usage 与请求监控，在认证完成后，根据 **下游 API Key + Model + Policy/Rule** 选择具体的上游 OAuth / API credential，并提供 failover、轮询、权重、优先级、sticky 路由与可观测性。

当前重点兼容：CLIProxyAPI v7.2.154（schema 5）、Linux amd64 / Debian Bookworm 类环境。

> 版本变更记录请查看 `CHANGELOG.md` 与 GitHub Releases。README 只描述当前版本的用途和使用方式。

## 解决什么问题

CPA 原生可以管理多个 OAuth / API credential，但当你希望不同的下游 API Key 使用不同的上游链路时，需要一个显式、可控、可诊断的调度层。

例如：

```text
OAuth: a b c
API:   x y z

api-key-1: a → b → c → x → y → z
api-key-2: x → y → z → a → b → c
```

Key Chain Router 的目标是：

- 保留 CPA 原生下游认证，不自行接管 API Key 校验。
- 一个下游 API Key 对应一个 Policy。
- 一个 Policy 可按 Model 定义多条 Rule。
- 每条 Rule 独立决定候选 credential、调度策略、Failover 行为和可选 Model Override。
- 精确选择具体 credential，同时不绕过 CPA 当前的候选资格、cooldown 与可用性判断。
- 提供可查询的路由记录、选择原因、统计和可选 SQLite 持久化。

## 路由模型

一个下游 API Key **只能对应一个 Policy**：

```text
API Key
  └─ Policy
      ├─ Rule: gpt-5.6-luna
      ├─ Rule: gpt-5.6-sol
      ├─ Rule: claude-*
      └─ Rule: *
```

Rule 匹配优先级：

```text
精确 model > glob（* / ?）> * catch-all
```

同一个 Policy 内：

- 同一个精确 Model 不能重复。
- 最多只能有一个 `*` Rule。
- 每条 Rule 可使用完全不同的候选链和调度策略。

## Credential 定位与调度

v0.7 起，KCR 明确区分 **持久 identity**、**CPA live Auth.ID** 与 **本次 scheduler eligibility**。

新建或重新保存的 Policy 会在能够精确解析时持久化当前 live `auth_id`，同时保留 `AuthIndex` 作为兼容/迁移信息。旧 Policy 仍可通过历史 identity 算法精确还原当前 CPA live Auth.ID；无法唯一证明同一 credential 时 fail closed，不按 config 数组位置、provider 或 base URL 猜测替代 key。

实际调度路径：

```text
Policy Candidate
  → 精确解析当前 live Auth.ID
  → 如该 credential 已有 CPA prefix 且目标 model 已注册：使用 prefix/model 缩小 provider/credential 范围
  → X-CPA-Key-Chain-Ticket
  → scheduler.pick
  → 验证 exact live Auth.ID 确实存在于 CPA 本次 Candidates
  → 返回 AuthID
```

### CPA priority / cooldown 预过滤与 KCR Priority 的区别

KCR `Priority` 只决定 **KCR Rule 内候选顺序**；CPA credential 自身的 priority、cooldown、disabled/unavailable 状态属于 CPA 的更前置 eligibility 层。

当前 CPA plugin ABI 的 `HostModelExecutionRequest` 没有 per-request `forced_provider` / `auth_id` hard-pin 字段，而且 plugin scheduler 收到的是 CPA 已完成 eligibility 与 credential-priority 预过滤后的 `Candidates`。因此 KCR **不会**通过临时修改全局 CPA credential priority 来绕过这一层，因为那会污染其他并发请求。

v0.7 的处理方式：

- credential 已配置唯一 `prefix` 且该 model 已注册时，KCR 在 nested execution 前自动使用 `prefix/model`，先把 provider/credential 范围缩到目标 credential，再由 ticket 验证 exact Auth.ID。
- failover 到另一 prefix credential 时会替换已有 credential prefix，不生成 `bar/foo/model`。
- 不为未注册 model 伪造 prefix alias。
- credential 无 prefix 时仍使用 exact Auth.ID scheduler pin；如果 CPA 已把目标 credential 预过滤掉，会明确报 `pinned credential is not eligible in the current CPA candidate set` 并按 KCR Failover 规则继续，而不是静默使用另一 credential。
- `scheduler did not claim execution ticket` 表示 KCR 没有取得本次 nested execution 的 scheduler ownership；这类情况直接 fail closed，避免重复请求和错误归因。

诊断页会展示 live `auth_id`、实际 `execution_model`、credential prefix、identity 解析错误与 scope 提示，便于区分 KCR candidate 顺序和 CPA prefilter。

## 调度策略

每条 Rule 可独立选择：

- `ordered-failover`：按候选配置顺序选择。
- `round-robin`：轮询首选候选，失败后继续其他候选。
- `weighted-round-robin`：Smooth Weighted Round Robin。
- `priority-weighted`：先选最高 Priority 组，再在组内按 Weight 平滑分配。
- `sticky`：Weighted Rendezvous Hash，相同 session/header 尽量稳定命中同一 credential。
- `cpa-default`：明确交回 CPA 默认路由，不由 KCR 接管。

候选常用字段：

```text
Priority       数值越大优先级越高
Weight         同策略 / 同优先级内的相对流量权重
Override Model 留空时继承客户端 model；填写时仅该候选覆盖 model/alias
```

UI 会按 Strategy 隐藏无意义字段：

- `ordered-failover` / `round-robin`：不显示 Priority、Weight。
- `weighted-round-robin`：只显示 Weight。
- `priority-weighted` / `sticky`：显示 Priority、Weight。
- `cpa-default`：不显示候选字段。

## Failover

每条 Rule 可分别定义不同错误类型的处理动作：

- network error
- 401 / 403
- 408
- 409
- 429
- 5xx
- 其他错误

可选动作：

- `next`：下一候选。
- `same-priority-first`：先尝试同 Priority 的其他候选，再降级。
- `next-priority`：直接进入下一 Priority。
- `stop`：停止 failover。
- `cpa-default`：转 CPA 默认路由。

候选全部耗尽后也可选择直接失败或转 `cpa-default`。

`max_attempts=0` 表示自动按候选数量决定。

## Sticky 路由

`sticky` 使用 Weighted Rendezvous Hash，不维护额外的 session → provider 持久映射。

默认 `auto` 会尝试从请求中获取：

- metadata 的 `session_id` / `sessionId` / `trace_id`
- `X-Claude-Code-Session-Id`
- `X-Session-Id`
- `Session-Id`
- `X-Request-Id`

也可以指定自定义 Header。

适合希望相同 session 尽量命中同一上游 credential、提高上游缓存命中率的场景。

## 路由决策与可观测性

对于进入 KCR 管理范围的请求，主要决策为：

```text
KCR_HANDLED              KCR 接管并按 Policy / Rule 执行
KCR_FALLBACK_TO_CPA      KCR 接管后按 Failover 规则转 CPA 默认路由
KCR_BYPASS_CPA_DEFAULT   已配置 Policy，但 Rule 未命中或 Rule 明确为 cpa-default
```

**完全未配置 Policy 的 API Key 不生成 KCR 路由事件**：不进入内存记录、不写 SQLite、不写 `kcr routing decision` host log，也不在管理页显示。

路由记录页面支持：

- 紧凑列表，一条请求一行。
- 浏览器本地时间显示，原始时间仍以 UTC RFC3339 保存。
- Model、Policy / Rule、Strategy、最终候选、Provider、状态、耗时、Attempts。
- 全文搜索。
- Decision、成功/失败、Policy、Strategy、Provider、Model、HTTP 状态、时间范围过滤。
- 请求数、成功率、平均耗时、P95、Fallback 数、平均尝试次数统计。
- 点击展开完整候选尝试链与每一步“为什么选到这个候选”的原因。
- 移动端展开详情会补齐桌面表格折叠掉的字段。

选择原因基于**请求实际执行时的 Rule 快照**生成，不会因为请求执行过程中 Policy 被编辑而被事后改写。

## SQLite 持久化

默认使用内存 Ring Buffer；需要跨页面刷新、插件重载或 CPA 重启保留历史时，可开启 SQLite。

数据源规则：

```text
SQLite OFF
  → 查询 Memory

SQLite ON + writer healthy
  → 查询 SQLite

SQLite ON + 打开/查询失败
  → 查询 Memory

SQLite ON + 写入失败或队列丢事件
  → writer 标记 degraded
  → 查询 Memory
  → 直到 SQLite sink 重启后重新恢复
```

这样可以避免数据库发生写失败后，管理页仍持续展示一份“可读但已经停止更新”的陈旧 SQLite 历史。

SQLite 状态页显示：

- Enabled / Active
- 实际数据库绝对路径
- 文件大小
- Events / Attempts 行数
- Journal Mode
- 最后成功写入时间
- 最后错误
- Writer 是否 healthy

一次路由记录查询中的统计、P95、事件列表、Attempts 和 Facets 会在同一个 SQLite 只读事务快照中读取，避免并发写入造成 `matched`、`returned` 与统计数据互相不一致。

SQLite 使用 WAL + busy timeout，由异步 writer goroutine 写入；数据库异常不会阻塞模型请求。

SQLite 表：

- `routing_events`：请求级最终决策、最终结果、selection reasons。
- `routing_attempts`：请求内的每一次候选尝试。

只保存路由元数据，不保存 Prompt、请求/响应正文、完整 API Key、上游 API secret 或 OAuth Token。

## 可观测性默认配置

```text
Memory Ring Buffer  ON   500 条
CPA host.log        ON   info
SQLite              OFF
Response Headers    OFF
```

可选调试响应 Header 默认关闭。开启后只返回：

```text
X-KCR-Decision
X-KCR-Trace-ID
X-KCR-Rule
```

不会返回 API secret / OAuth token。

## 安装

### 通过 CPA Plugin Store

将自定义插件源加入 CPA 配置：

```yaml
plugins:
  enabled: true
  dir: plugins
  store-sources:
    - "https://raw.githubusercontent.com/kitdine/cpa-plugin-key-chain-router/main/registry.json"
```

重载 CPA 后，在 Plugin Store 搜索 **Key Chain Router** 并安装。

### 手工安装

从 GitHub Releases 下载：

```text
key-chain-router-v<version>.so
```

放入：

```text
plugins/linux/amd64/
```

然后重启 CPA。

Release 同时提供 CPA Plugin Store 使用的：

```text
key-chain-router_<version>_linux_amd64.zip
checksums.txt
```

## 配置与使用

安装后进入管理页：

```text
/v0/resource/plugins/key-chain-router/status
```

典型操作流程：

1. 保证下游 API Key 已存在于 CPA 原生 `api-keys`。
2. 在 KCR 中为该 API Key 创建唯一 Policy。
3. 给 Policy 添加按 Model 匹配的 Rule。
4. 为 Rule 添加 OAuth / API credential 候选。
5. 设置 Strategy、Priority / Weight、Failover 和可选 Override Model。
6. 保存后通过诊断页检查 Rule 命中、live Auth.ID、execution model / prefix scope，以及是否存在 CPA prefilter 风险。
7. 在“路由记录”中观察最终候选、Attempts 和选择原因。

配置示例见：

```text
config.example.yaml
```

## 安全边界

- KCR 不接管 CPA 下游认证；API Key 必须仍存在于 CPA 原生 `api-keys`。
- State 只保存下游 Key 的 SHA-256 fingerprint / hint，不保存明文 Key。
- Credential 选择通过一次性内部 ticket 完成，并以本次 scheduler Candidates 中的 exact live Auth.ID 做最终验证。
- 无法唯一解析 live Auth.ID 时 fail closed，不按 slot 或相似配置猜测替代 credential。
- KCR 不通过临时修改 CPA 全局 credential priority 绕过预过滤。
- 管理资源页应只暴露在可信网络。
- Memory / SQLite / logging 等可观测性故障不得影响模型路由。

## 开发与 CI

源码核心位于：

```text
src/
```

构建脚本：

```text
scripts/build.sh
```

标准 CI 使用 `golang:1.26-bookworm`，执行：

```text
go test ./...
go vet ./...
node --check ui.js
c-shared build
ABI smoke
binary inspection
artifact packaging
```

正式 Release 输出：

```text
key-chain-router-vX.Y.Z.so
key-chain-router.so
key-chain-router-vX.Y.Z-linux-amd64.tar.gz
SHA256SUMS
key-chain-router_X.Y.Z_linux_amd64.zip
checksums.txt
```

发布历史与版本变更请查看 `CHANGELOG.md` 和 GitHub Releases。
