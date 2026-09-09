# Key Chain Router v0.3.0 测试清单

## 1. 插件加载

确认：

```text
plugin loaded ... key-chain-router ... 0.3.0
plugin registered ... key-chain-router ... 0.3.0
```

## 2. 页面

打开：

```text
/v0/resource/plugins/key-chain-router/status
```

确认：

- 无 Management Secret
- 能自动读取下游 CPA API Key（仅脱敏）
- OAuth credential 正常显示
- API Provider credential 正常显示
- API Key/Provider secret 没有明文出现在页面

## 3. 模型匹配

路由设置：

```text
匹配模型: *
```

客户端分别发送：

```text
gpt-5.6-luna
gpt-5.6-sol
```

两者都应命中同一个 Key route。

再改为：

```text
匹配模型: gpt-5.6-luna
```

则：

```text
gpt-5.6-luna -> 命中
gpt-5.6-sol  -> CPA 默认路由，不由该 Key route 接管
```

## 4. 原模型继承

候选 A/B/C 的覆盖模型全部留空。

客户端请求：

```json
{"model":"gpt-5.6-luna"}
```

诊断/真实请求轨迹中 A/B/C 的 effective model 都应为：

```text
gpt-5.6-luna
```

不应该出现 `code`。

## 5. 单候选覆盖

只给 X 填：

```text
x/gpt-5.6-luna
```

预期：

```text
A -> gpt-5.6-luna
B -> gpt-5.6-luna
C -> gpt-5.6-luna
X -> x/gpt-5.6-luna
```

## 6. 测试路由 / 静态诊断

点击“测试路由”，输入模型。

应显示：

- 模型是否匹配
- 候选顺序
- provider
- auth_index
- effective model
- 当前资源是否存在
- 预计第一候选
- curl

## 7. 真实执行轨迹

复制诊断页 curl，在可信终端执行。

然后回诊断页刷新。

正常请求应出现：

```text
最近一次真实执行
1. A ... 200
最终 A
```

模拟 failover：

```text
A -> 429
B -> 503
C -> 200
```

诊断中应显示：

```text
1. A ... 429
2. B ... 503
3. C ... 200
最终 C
```

同时在 CPA Token Usage / Request Log 检查最终：

```text
api_key    = 当前下游 CPA 原生 Key
provider   = C 的 provider
auth_index = C 的 AuthIndex
model      = effective model
```

## 8. 两把 Key 独立链

```text
Key1: A -> B -> C -> X -> Y -> Z
Key2: X -> Y -> Z -> A -> B -> C
```

分别请求，确认顺序不会串线。

## 9. 不 fallback

首候选返回：

```text
400 / 404 / 422
```

不得进入下一候选。

## 10. Streaming

- 上游在第一 chunk 前失败 -> 允许下一候选
- 已输出第一 chunk 后失败 -> 终止，不拼接另一个候选

## 11. State 安全

检查：

```bash
cat plugins/linux/amd64/key-chain-router-state.json
```

应：

- 权限 0600
- 无下游 API Key 明文
- 无 API Provider key 明文
- 有 fingerprint / key_hint / provider / AuthIndex / match_models / override_model
