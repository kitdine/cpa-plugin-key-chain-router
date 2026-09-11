from pathlib import Path

version = Path("VERSION")
current = version.read_text().strip()
if current != "0.6.4":
    raise SystemExit(f"expected VERSION 0.6.4, got {current!r}")
version.write_text("0.6.5\n")

changelog = Path("CHANGELOG.md")
text = changelog.read_text()
marker = "# Changelog\n\n"
if not text.startswith(marker):
    raise SystemExit("unexpected CHANGELOG header")
entry = '''## v0.6.5 - 2026-09-11

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

'''
changelog.write_text(marker + entry + text[len(marker):])
