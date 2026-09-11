# Changelog

## v0.6.4 - 2026-09-11

集中清理此前 Codex Review 遗留的问题，并把“CI + Codex Review 零未解决 finding”作为合并门槛。

### Scheduler 正确性

- `scheduler.pick` 在通过 `AuthIndex → host.auth.list → AuthID` 解析运行时 ID 前，重新确认目标 credential 仍存在于 CPA 本次 `Candidates` 中，避免绕过 cooldown、不可用或 provider/model 资格筛选。
- 同一 `AuthIndex + Provider` 如果仍映射到多个不同 AuthID，改为 fail closed，不再依赖 `host.auth.list` 返回顺序任意选择。

### 路由可观测性

- 路由事件保留请求实际执行时使用的 Rule 快照；`selection_reasons` 不再因为长请求执行期间 Policy 被编辑/删除而被事后改写。
- 移动端展开详情补齐桌面表格在窄屏隐藏的时间、Model、Policy、Rule、Strategy、最终候选、Provider、状态、耗时和 Attempts。

### SQLite

- Schema migration 改为先检查列是否已存在；真实 `ALTER TABLE` 或历史数据清理失败会显式返回，不再静默吞掉。
- SQLite 写入失败或异步队列发生丢事件后，writer 会进入 sticky degraded 状态；在 sink 重启前路由记录查询自动回退到 Memory，避免持续展示“仍可读但已停止更新”的陈旧数据库历史。
- 同一次路由记录查询中的统计、P95、事件列表、Attempts 与 Facets 统一在一个 SQLite 只读事务快照中读取，避免并发写入导致 `matched` / `returned` / 统计互相不一致。

### README / 发布流程

- README 不再承担 release notes 职责，改为只描述项目用途、路由模型、安装配置、使用方式、可观测性、SQLite、安全边界和开发流程。
- 版本历史统一放在 `CHANGELOG.md` 与 GitHub Releases。
- 后续 PR 必须同时满足：标准 CI 全绿、Codex Review 已完成、unresolved findings = 0，才允许合并。

### 测试

- 新增 scheduler candidate eligibility、重复 AuthID 消歧、请求 Rule 快照、SQLite migration、writer degraded fallback 与一致性事务查询等回归测试。
- PR #7 已通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 和 artifact packaging，并完成 Codex Review 且无新 finding。

## v0.6.3 - 2026-09-10

修正路由记录列表的时间展示。后端与 SQLite 继续统一保存 UTC RFC3339 时间，前端列表改为按浏览器本地时区转换显示。

### 时间显示

- 路由记录列表由直接截取 UTC 字符串改为使用浏览器本地时区。
- 显示格式保持紧凑的 `MM-DD HH:mm:ss`，表头明确标记为“时间（本地）”。
- 鼠标悬停仍保留原始 RFC3339 UTC 时间，方便精确排查和跨时区对照。
- 无效或非标准时间字符串不会报错，直接回退显示原始值。
- 数据库存储、时间筛选和保留策略继续使用 UTC，不改变持久化语义。

### 测试

- 增加 Asia/Shanghai 与 America/Los_Angeles 两个时区的确定性转换检查。
- 保留 Go test、JavaScript syntax、Linux amd64 build、ABI smoke 等标准 CI。

## v0.6.2 - 2026-09-10

修复 SQLite “只写不读”的持久化缺口，使路由记录在页面刷新、插件重载和 CPA 重启后仍可查询。

### SQLite 持久化查询

- SQLite 开启且 active 时，管理页 `events` 查询直接读取 SQLite，而不是只读取进程内 `v4Runtime.recent`。
- 数据库查询完整支持现有全文搜索、Decision、成功/失败、Policy、Strategy、Provider、Model、HTTP 状态、时间范围和返回上限。
- 统计卡、P95 和筛选 facets 在 SQLite 模式下同样基于持久化数据计算。
- `routing_events` 新增 `error` 字段并自动迁移旧数据库，最终错误信息也可跨重启查看。

### SQLite 健康状态

- 管理页显示 SQLite 是否运行、实际绝对路径、文件大小、Events / Attempts 行数、journal mode、最后写入/最后持久化时间和最后错误。
- writer INSERT 错误不再静默吞掉，会记录到健康状态并写 CPA host error log。
- SQLite 打开失败不再导致 plugin register/reconfigure 失败；路由继续工作并自动回退到 Memory 查询，同时暴露数据库错误。

### 测试

- 新增持久化回归测试：Memory 为空但 SQLite 中存在事件时，路由记录仍能返回 event、attempts 和 selection reasons。
- PR CI 已通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 和 artifact packaging。

## v0.6.1 - 2026-09-10

优化 v0.6.0 路由记录管理页的信息密度，不改变路由、Failover 或日志存储语义。

### 路由记录列表

- 将逐条纵向 Card 改为紧凑表格，一行显示一个路由请求。
- 主列表直接展示时间、Model、Policy / Rule、Strategy、最终候选、Provider、状态、耗时和 Attempts。
- 正常 `KCR_HANDLED` 不再每行重复显示长决策字符串，改为紧凑的绿色成功状态；Fallback、Bypass 和失败分别使用独立状态样式。
- Attempts 大于 1 时使用醒目标记，便于快速发现发生过 Failover 的请求。
- 点击任意请求行即可展开完整详情，包括 Decision、Trace、最终 AuthIndex、候选尝试链和每次选择原因。
- 桌面端使用紧凑表格与 sticky header；窄屏自动退化为摘要行 + 点击展开详情。
- 保留 v0.6.0 的统计卡、全文搜索、多条件筛选和 no-policy 丢弃行为。

### 测试

- PR CI 已通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 和 artifact packaging。

## v0.6.0 - 2026-09-10

升级路由可观测性与管理页查询能力，并停止记录与 KCR Policy 无关的请求。

### 路由记录

- 未配置 KCR Policy 的 API Key 请求不再进入内存 ring buffer、不写 SQLite、不写 `kcr routing decision` host log，也不在管理页显示。
- SQLite 启动时自动清理历史 `reason=no_policy` 事件及对应 attempts。
- 已配置但停用 Policy、已配置 Policy 但 model 未命中 Rule、显式 `cpa-default` 等有诊断价值的情况继续保留。
- 每个 routing event 新增 `selection_reasons`，与 attempts 一一对应，解释每次为什么选择该候选。
- 首选原因覆盖 ordered failover、round robin、Smooth Weighted Round Robin、Priority Weighted 和 Sticky Weighted Rendezvous Hash。
- 后续候选说明上一候选状态以及 `next`、`same-priority-first`、`next-priority`、`cpa-default` 等 Failover 动作。

### 查询与统计

- 新增路由事件查询 API，支持全文搜索。
- 支持按 Decision、成功/失败、Policy、Strategy、Provider、Model、HTTP 状态分组和时间窗口筛选。
- 支持 100 / 200 / 500 / 1000 条返回上限。
- 新增统计信息：匹配请求数、成功率、平均耗时、P95、Fallback 数、平均尝试次数。
- 管理页将原“最近路由记录”升级为“路由记录”，增加筛选器、统计卡、结果计数和逐 attempt 详情。

### SQLite 兼容

- `routing_events` 增加 `selection_reasons` JSON 字段。
- v0.4/v0.5 已存在的 SQLite 数据库通过启动迁移自动增加字段，无需重建数据库。

### 测试

- 新增 no-policy 丢弃、disabled Policy 保留、selection reasons 和日志筛选/统计单测。
- PR CI 已通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 和 artifact packaging。

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
