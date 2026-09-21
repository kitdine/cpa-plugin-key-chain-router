# Changelog

## v0.8.1 - 2026-09-21

修正 v0.8.0 与已确认 Client Affinity 设计稿不一致的问题。v0.8.0 已从 Plugin Store 撤回，本版本重做管理界面并补齐 Client Type / Client Affinity 的数据模型分离。

### 管理界面重构

- 重建 KCR 管理台信息架构：仪表盘、Policy、上游资源、使用统计、系统设置五个一级页面。
- 新建 / 编辑 Policy 改为独立页面，不再使用旧版弹窗。
- Policy 基础信息明确拆分“客户端类型”和“Client Affinity”：客户端类型始终保存；Affinity 仅决定是否启用严格资源过滤。
- Client Affinity 关闭时，全部资源均可选择；Strict 开启时，仅匹配客户端类型的原生 Provider 资源 + 全部 OAuth 资源可选。
- 被 Affinity 过滤的资源仍保留在候选资源表中，并显示过滤状态与原因，不再从 UI 中消失。
- 候选资源选择改为完整资源表；已选候选使用独立有序表格，支持拖拽排序、Priority、Weight、模型重写和状态控制。
- 新增一级“上游资源”页面，统一展示 API / OAuth 资源、Provider、Endpoint / 账号、Prefix、客户端适配和健康状态，并提供资源详情抽屉。
- 路由记录与可观测性功能分别迁移到“使用统计”和“系统设置”，保留现有查询、统计和 SQLite 能力。

### Client Type 兼容迁移

- 新增持久化字段 `client_type`，不再把客户端身份和 Affinity Provider 混为同一个字段。
- v0.8.0 Strict Policy 自动从历史 `client_provider` 迁移到 `client_type`。
- Affinity Off 时 `client_type` 继续保留，`client_provider` 仅作为 Strict 运行时兼容字段。
- 未知 / 未来 Affinity 模式继续 fail closed。

### 测试

- PR #34 通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke 与 binary inspection。
- 新增 Client Type 持久化和 v0.8.0 Strict Policy 迁移回归测试。

关联：Issue #33、PR #34。

## v0.8.0 - 2026-09-21（已撤回）

新增 Client Affinity v1，使 Policy 可以显式约束原生 API Provider，同时继续允许 OAuth 资源参与候选链。

### Client Affinity

- 新增 `off` / `strict` 两种模式；现有 Policy 默认保持 `off`，不改变既有路由行为。
- `strict` 模式绑定一个客户端 Provider：只允许该 Provider 的原生 API 资源，同时允许全部 OAuth credential。
- UI 根据 Affinity 动态过滤可选资源，并保留暂时不可见或当前不允许的已有候选，避免编辑时静默丢失 failover chain。
- OAuth-only 部署也可以选择对应 Provider；已有 Provider 暂时从 live resources 消失时仍保留原配置，不会自动切换。
- `cpa-default` Rule 不参与 Affinity candidate 校验，因为该 Rule 不执行候选链。

### Fail-closed 与兼容性

- 保存时拒绝未知/预留的非空 Affinity 模式，避免拼写错误或未来值被静默降级为 `off`。
- 运行时对未知 persisted Affinity 模式 fail closed，不允许 candidate execution，也不会回退到 CPA unrestricted default routing。
- 管理页显式展示当前版本不支持的 Affinity 值；用户必须明确选择受支持模式后才能迁移配置。
- 旧 state 中缺失的 Affinity 字段仍归一化为 `off`，保持向后兼容。

### 测试与发布

- 新增 Client Affinity strict / off / unknown-mode 回归测试。
- PR #32 通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 与 artifact packaging。

关联：PR #32。

## v0.7.1 - 2026-09-21

恢复基于 CPA 原生 request-scoped exact AuthID 的 credential 执行，不再依赖 prefix / 同一 CPA credential priority 才能完成 ordered failover。

### Native exact AuthID pinning

- 每个 KCR candidate 在调用 `host.model.execute[_stream]` 前解析 exact live `Auth.ID`，并通过 `auth_id` 与 `forced_provider` 传给 CPA。
- execution ticket 绑定到与 host request 相同的 live Auth.ID，继续验证 KCR scheduler ownership，避免并发 credential reload 时二次 identity 解析发生漂移。
- lower-priority fallback credential 由 CPA 的 request-scoped pin 在 priority tier 过滤前收窄；不修改全局 priority、prefix、OAuth token 或 CPA config。
- 无效、disabled、不可用或 model-ineligible credential 继续 fail closed，不允许 CPA 静默换绑其它 credential。
- ABI smoke 同时验证 non-stream / stream host callback 都携带 exact `auth_id` 与 `forced_provider`。
- 推荐 CLIProxyAPI v7.3.10+；旧 CPA 仍保留 ticket/prefix 兼容路径，但可能受旧 priority pre-filter 限制。
- CPA 在 `scheduler.pick` 前因 exact pin 不可执行而返回的明确选择失败会保留原始错误与 HTTP status，使 FailoverPolicy 可以继续下一个候选。
- 其它未 claim 的 host success/error 继续按 scheduler ownership failure 终止，避免旧 CPA 或其它 scheduler 已发送请求后产生重复计费/副作用。
- credential identity / exact Auth.ID 解析失败改为 typed routing-control failure，不再错误污染 candidate circuit health。

关联：#21、#25、#28；CPA upstream #5814、#5815。

## v0.6.7 - 2026-09-11

修复 v0.6.6 Health-aware Failover 在 `plugin.reconfigure` / credential rotation 场景下的 runtime generation 竞态，避免旧 credential 的在途请求在重载后污染新 runtime 的 circuit health。

### Runtime generation 隔离

- 每次 `configureV4` / `plugin.reconfigure` 都推进 runtime generation，即使序列化后的 Policy、Provider、AuthIndex 等字段完全不变，也能识别新旧 runtime。
- candidate acquisition 在持有 runtime lock 时原子捕获 generation，并将其绑定到本次 attempt 的私有 candidate clone，不修改共享 Policy candidate。
- success、failure 与 probe-release 三类 health 回写统一校验 attempt generation；旧 generation 的迟到结果直接丢弃。
- 保留既有 candidate 结构比较，用于识别 `save_policy` 后被替换的 candidate 配置；generation 与结构校验共同构成 health 写回门禁。

### 回归测试与 Review

- 新增同内容 Policy 重载场景：旧 generation 请求迟到返回 503 时，不得重新创建 OPEN；新 generation 请求仍可正常更新 circuit state。
- 新增旧 HALF_OPEN probe 场景：reconfigure 后 stale probe-release / success / failure 均不能修改新 generation 的 probe/circuit state。
- PR #16 已通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 与 artifact packaging。
- PR #14 原 Codex P2 generation finding 已由 #16 修复并 resolve。

关联：PR #14、PR #16。

## v0.6.6 - 2026-09-11

新增 Health-aware Failover 与自动 Failback，使首选候选 A 发生持续故障后，后续请求不会反复先撞 A 再切到 B；A 恢复后通过受控探测自动回到原策略排序。

### Circuit Breaker 与自动恢复

- 候选健康状态跨请求维护 `CLOSED / OPEN / HALF_OPEN` 三态。
- 首选候选故障进入 OPEN；cooldown 内后续请求直接跳过它并使用健康 fallback 候选。
- cooldown 到期后只允许一个请求持有 HALF_OPEN probe lease；其他并发请求继续使用 fallback，避免 probe thundering herd。
- probe 成功后关闭 circuit，恢复原策略排序并自然自动 failback；probe 失败才推进 30s → 60s → 120s → …、最高 5 分钟的指数退避。
- 429 使用 60 秒基线并尊重更长的 `Retry-After`；401/403 使用 5 分钟 cooldown，后续失败不会缩短已有恢复期限。

### 并发与配置正确性

- stale normal success 不能取消 OPEN / HALF_OPEN recovery cycle；只有真正持有当前 probe lease 的成功才能关闭 circuit。
- stream panic / 本地输出失败按 ownership 释放自己的 probe，不会误释放其他请求后来取得的 probe。
- `next` / `same-priority-first` / `next-priority` 在健康过滤下继续保持 FailoverPolicy 语义。
- Candidate 的执行相关配置变化会立即清理旧 health；旧配置下仍在途的请求结果被识别为 superseded 并丢弃，不能污染新配置状态。
- Streaming 首包/读流错误统一提取实际 HTTP status，避免 400/404 被误判为 network failure，429 进入正确的 RateLimit health/failover 语义。

### 可观测性与诊断

- Management snapshot 暴露 candidate health、backoff、next probe、last failure/success 与 probe-in-flight。
- 路由记录明确说明 OPEN / HALF_OPEN probe-in-flight 导致的 candidate skip，以及健康过滤后真正执行的候选。
- 诊断 UI 新增健康状态和“当前路由”列，区分静态 rank 与当前真正可选/首选候选。

### 测试与 Review

- Non-streaming 与 streaming 共用同一健康状态与恢复语义。
- 新增 cooldown skip、单 probe 并发、自动 failback、指数退避、Retry-After、stale success、probe ownership、配置变更、superseded result 和 health-aware selection reason 等回归测试。
- PR #14 通过 Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 与 artifact packaging，并完成最终 Codex Review。

关联：Issue #13、PR #14。

## v0.6.5 - 2026-09-11

修复一个严重的 KCR 路由正确性问题：单次 KCR candidate attempt 进入 `host.model.execute` 后，CPA 不再能够在该 attempt 内部静默切换到其他 credential，并把其他 credential 的成功错误归因给原候选。

### 单次 Attempt 严格锁定一个 Credential

- KCR ticket 从“首次 `scheduler.pick` 即消费”的 one-shot ticket 改为 candidate execution 生命周期内的 call-scoped guard。
- 同一个 ticket 只允许第一次 credential selection；如果 CPA 在同一次 nested execution 中再次触发 `scheduler.pick`，KCR 会 fail closed，把控制权返回给 KCR 自己的 Failover Policy，而不是让 CPA 默认 scheduler 接管。
- nested `host.model.execute` / `host.model.execute_stream` 返回后显式 revoke ticket，避免 ticket 泄漏或被后续请求复用。
- ticket 无效/过期、运行时 AuthID 解析失败、或 pinned credential 已不在当前可用 Candidates 中时均 fail closed；只有 Policy 明确选择 `cpa-default` 时才允许 CPA 默认路由。

### Scheduler 兼容性

- 保留 v0.5 的稳定 `AuthIndex + Provider` 资格判断路径，同时兼容当前 CPA 以 runtime `AuthID` 暴露 scheduler candidate 的路径。
- `AuthIndex -> host.auth.list -> AuthID` 仍是运行时 AuthID 的权威解析方式，不重新把 `Candidates[].id` 当作稳定持久化身份。
- ABI smoke 恢复 `wrong-candidate-id + matching auth_index` 场景，防止修复 Issue #10 时引入 v0.5 行为回退。

### 路由记录与测试

- 一个 KCR Attempt 现在与一个实际 credential upstream execution 保持一致；CPA 内部不会再把 A 失败、B 成功折叠成“KCR A 成功 / Attempts=1”。
- Non-streaming 与 streaming bootstrap 路径使用同一 ticket guard 语义。
- 新增 ticket claim/revoke、重复 scheduler pick fail-closed、runtime AuthID / stable AuthIndex candidate membership 等回归测试。
- PR #11 通过完整 CI：Go test、Go vet、JavaScript syntax、Linux amd64 c-shared build、ABI smoke、binary inspection 与 artifact packaging；最新 Codex Review 完成且无未解决 finding。

关联：Issue #10、PR #11。

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