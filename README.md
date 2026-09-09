# CPA Key Chain Router v0.3.0

面向 CLIProxyAPI（CPA）v7 的动态路由插件。它保留 CPA 原生下游 `api-keys` 认证与 usage / request log 链，只负责按照不同下游 API Key 选择独立的有序上游 failover 链。

当前重点兼容环境：

```text
CLIProxyAPI v7.2.154
commit ba7e558
Linux amd64 / glibc
```

## v0.3.0 解决的问题

### 1. 不再要求客户端使用固定 `model=code`

路由现在有独立的“匹配模型”规则：

```text
*                         所有模型
 gpt-5.6-luna             单个模型
 gpt-5.6-luna,claude-*    多个规则
```

支持 `*` / `?` 通配符和逗号分隔。

例如：

```text
API Key: sk-claude-mem
匹配模型: *
```

那么客户端可以继续发送自己真实的模型名：

```json
{"model":"gpt-5.6-luna"}
```

插件不再要求把客户端模型统一改成 `code`。

### 2. 候选的“覆盖模型 / alias”可以留空

每个候选有两个完全不同的概念：

```text
路由匹配模型
    决定这个请求是否由该路由接管

候选覆盖模型 / alias
    决定实际请求这个候选时，是否改写客户端 model
```

候选覆盖模型留空：

```text
客户端 model = gpt-5.6-luna
       ↓
A 保持 gpt-5.6-luna
       ↓ fail
B 保持 gpt-5.6-luna
       ↓ fail
X 保持 gpt-5.6-luna
```

填写覆盖模型：

```text
X override_model = x/luna
```

只有尝试 X 时才改成：

```text
x/luna
```

这对有 prefix、或同名 alias 在多个 Provider 重复的场景很重要。

### 3. 新增“测试路由 / 诊断”

每条路由现在有“测试路由”按钮，可以静态检查：

- 输入模型是否匹配该路由
- 每个候选是否仍存在于当前 CPA 环境
- Provider
- AuthIndex
- 实际有效模型（继承原模型或覆盖模型）
- 当前预计第一候选
- 可复制的真实测试 `curl`

插件资源页**不会主动发送真实模型请求**。原因是 `/v0/resource/plugins/...` 是无需 Management Secret 的资源入口，如果在这里提供“一键真实请求”，任何能访问该页面的人都可能消耗你的模型额度。

实际验证流程：

1. 点“测试路由”
2. 复制页面生成的 curl
3. 在可信终端执行
4. 回到“测试路由”刷新
5. 页面会显示该路由最近一次真实非流式请求的尝试轨迹，例如：

```text
1. A | codex | authIndex-A | gpt-5.6-luna | 429
2. B | codex | authIndex-B | gpt-5.6-luna | 503
3. C | codex | authIndex-C | gpt-5.6-luna | 200

最终：C
```

再结合 CPA Token Usage / Request Log 核对最终 provider/auth_index，即可完整验证路由。

## 目标场景

```text
OAuth: A, B, C
API Provider: X, Y, Z

API Key 1: A -> B -> C -> X -> Y -> Z
API Key 2: X -> Y -> Z -> A -> B -> C
```

客户端仍使用 CPA 原生：

```yaml
api-keys:
  - sk-key-1
  - sk-key-2
```

插件不创建第二套 API Key。

## 当前环境自动发现

管理页面不需要 Management Secret。

插件直接读取当前 CPA `config.yaml`，自动发现：

- 下游 `api-keys`
- `codex-api-key`
- `claude-api-key`
- `xai-api-key`
- `gemini-api-key`
- `interactions-api-key`
- `vertex-api-key`
- `openai-compatibility`

OAuth / auth-file credential 通过 CPA 官方：

```text
host.auth.list
```

API Key 和上游 provider key 只向页面返回脱敏值。插件 state 不保存这些明文 secret。

## AuthIndex 精确路由

候选优先保存 CPA 的稳定 `AuthIndex`。

执行时：

```text
候选 AuthIndex
      ↓
一次性 ticket
      ↓
host.model.execute
      ↓
CPA scheduler
      ↓
Key Chain Router scheduler.pick
      ↓
在当前 Candidates 中找到同一个 AuthIndex
      ↓
返回该候选的实际 AuthID
```

这样可以精确到 OAuth A/B/C 或某一条 API Provider credential，而不是只选择整个 Provider 池。

## Failover

默认继续下一候选：

```text
401 403 408 409 429 500 502 503 504
以及 host/network execution error
```

默认停止：

```text
400 404 422
```

Streaming：

- 首 chunk 前失败：允许切下一候选
- 已有输出后失败：停止当前 stream，不拼接另一上游

## 安装 / 升级

建议插件目录：

```text
plugins/linux/amd64/
```

先移走旧版本：

```bash
mkdir -p plugins/backup
mv plugins/linux/amd64/key-chain-router-v0.2.0.so plugins/backup/ 2>/dev/null || true
mv plugins/linux/amd64/key-chain-router.so plugins/backup/ 2>/dev/null || true
```

放入新版：

```bash
cp key-chain-router-v0.3.0.so plugins/linux/amd64/
chmod 755 plugins/linux/amd64/key-chain-router-v0.3.0.so
```

CPA 配置：

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    key-chain-router:
      enabled: true
      priority: 100
      state_file: key-chain-router-state.json
      ticket_ttl_seconds: 30
      fallback_on_status: [401, 403, 408, 409, 429, 500, 502, 503, 504]
      no_fallback_on_status: [400, 404, 422]
```

然后重启 CPA：

```bash
docker compose restart cli-proxy-api
```

访问：

```text
http://CPA:8317/v0/resource/plugins/key-chain-router/status
```

### v0.2.0 -> v0.3.0 数据迁移

旧 state 中的 `client_models` / `code` 不再控制路由匹配。

每条已有 route 自动迁移为：

```text
match_models = ["*"]
```

旧候选中已经填写的 `model` 会保守迁移成 `override_model`，确保升级不会突然改变现有请求行为。

如果希望所有候选继承客户端实际模型，在编辑页面点击：

```text
全部改为继承原模型
```

保存即可。

## 安全边界

插件是 CPA 进程内可信动态库，拥有与 CPA 进程相同权限。

管理资源 `/v0/resource/plugins/key-chain-router/status` 无 Management Secret，因此仅建议在可信局域网使用，与 CPA 管理面本身保持相同的网络隔离策略。

页面不会显示完整 downstream / upstream API Key，也不会主动发模型测试请求。

## 构建

源码目录：

```bash
cd src
```

测试：

```bash
go test ./...
go vet ./...
node --check ui.js
```

构建 Linux amd64：

```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -buildmode=c-shared \
  -trimpath -ldflags='-s -w' \
  -o ../dist/key-chain-router.so .
```

ABI smoke：

```bash
python3 abi_smoke.py ../dist/key-chain-router.so
```

## 当前测试状态

```text
go test ./...                  PASS
go vet ./...                   PASS
node --check ui.js             PASS
c-shared build                 PASS
ABI smoke schema 5             PASS
ABI smoke schema 6             PASS
legacy schema 1                PASS
model wildcard matching        PASS
empty override keeps model     PASS
AuthIndex exact scheduling     PASS
ticket one-shot                PASS
stream bridge ABI              PASS
```
