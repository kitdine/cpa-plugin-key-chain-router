# v0.4.0 测试清单

## CI

```bash
cd src
go test ./...
go vet ./...
node --check ui.js
cd ..
bash ./scripts/build.sh
cd src
python3 abi_smoke.py ../dist/key-chain-router-v0.4.0.so
```

必须验证 `c-shared` 产物导出：

```text
cliproxy_plugin_init
keyChainRouterPluginCall
keyChainRouterPluginFree
keyChainRouterPluginShutdown
```

## 1. v0.3 → v0.4 迁移

使用旧 state 启动 v0.4：

- 同一 API Key 的多个旧 Route 必须合并成一个 Policy。
- 每个旧 Route 变成一条 Rule。
- Rule 默认 `ordered-failover`。
- 旧候选顺序、AuthIndex 和 override model 保留。
- state 升级为 version 4。

## 2. 唯一 Policy 约束

- 同一个下游 API Key 在 UI 中不能再次“新建 Policy”。
- 同一 Policy 中重复 exact model 应保存失败。
- 最多只能有一个 `*` Rule。
- exact model > glob > `*`。

## 3. 策略验证

### ordered-failover

A 失败 → B → C。

### round-robin

连续请求应看到首选：

```text
A → B → C → A
```

### weighted-round-robin

配置 5:3:2，较大样本下应接近 50% / 30% / 20%，且使用 Smooth WRR 而不是随机突发。

### priority-weighted

Priority 100 组只要有候选可选，首选不能来自 Priority 50。组内按 Weight 分配。

### sticky

相同 `X-Session-Id` 连续请求首选必须稳定；更换 session 应允许映射到其他候选。

### cpa-default

命中后最近记录必须显示：

```text
KCR_BYPASS_CPA_DEFAULT
```

并由 CPA 默认 router 处理。

## 4. Failover 行为

分别模拟：

```text
network
401/403
408
409
429
5xx
```

验证 `next`、`same-priority-first`、`next-priority`、`stop`、`cpa-default`。

候选耗尽配置为 `cpa-default` 时，必须出现：

```text
KCR_FALLBACK_TO_CPA
```

而不是伪装成 `KCR_HANDLED`。

## 5. 可观测性

默认：

```text
memory=true
log=true
sqlite=false
response_headers=false
```

真实请求后页面“最近路由记录”必须能看到：decision、model、Policy、Rule、Strategy、尝试链、最终 Provider/AuthIndex、耗时。

CPA 日志搜索：

```text
kcr routing decision
```

应包含相同决策字段。

## 6. SQLite

开启后确认数据库生成，并启用 WAL。检查：

```sql
SELECT * FROM routing_events ORDER BY at DESC LIMIT 10;
SELECT * FROM routing_attempts ORDER BY id DESC LIMIT 20;
```

确认数据库中不存在：

- 完整 downstream API Key
- upstream API secret
- OAuth token
- Prompt / request body / response body

关闭 SQLite 后模型请求仍应正常；模拟 DB 不可写/queue 满时，也不得阻塞路由。

## 7. 响应 Header

默认请求响应不应出现 `X-KCR-*`。

开启调试响应 Header 后，命中 KCR 的非流式请求应有：

```text
X-KCR-Decision
X-KCR-Trace-ID
X-KCR-Rule
```

不得包含 AuthIndex、API Key、token。

## 8. Streaming

- 首 chunk 前失败允许 failover。
- 已发送首 chunk 后失败必须终止，不允许拼接另一上游内容。
- 最终仍写一条 RoutingEvent。

## 9. CPA 原生监控

最终成功请求继续核对 CPA Usage / Token Usage：

```text
api_key
provider
auth_id / auth_index
model
input/output/cache tokens
```

必须仍由 CPA 原生链记录。
