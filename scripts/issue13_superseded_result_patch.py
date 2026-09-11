from pathlib import Path


def patch_func(text, signature, needle, addition):
    start = text.index(signature)
    end = text.find("\nfunc ", start + len(signature))
    if end < 0:
        end = len(text)
    seg = text[start:end]
    if addition.strip() in seg:
        return text
    if seg.count(needle) != 1:
        raise SystemExit(f"{signature}: expected one needle, got {seg.count(needle)}")
    seg = seg.replace(needle, needle + addition, 1)
    return text[:start] + seg + text[end:]


hp = Path("src/v4_health.go")
hs = hp.read_text()
helper = '''func candidateHealthConfigCurrentLockedV4(p *Policy, r *PolicyRule, c *PolicyCandidate) bool {
\tif p == nil || r == nil || c == nil {
\t\treturn false
\t}
\tactivePolicy := v4Runtime.state.Policies[strings.TrimSpace(p.KeyFingerprint)]
\tif activePolicy == nil {
\t\treturn false
\t}
\tfor _, activeRule := range activePolicy.Rules {
\t\tif activeRule == nil || activeRule.ID != r.ID {
\t\t\tcontinue
\t\t}
\t\tfor _, activeCandidate := range activeRule.Candidates {
\t\t\tif activeCandidate != nil && activeCandidate.ID == c.ID {
\t\t\t\treturn candidateHealthConfigEqualV4(activeCandidate, c)
\t\t\t}
\t\t}
\t\treturn false
\t}
\treturn false
}

'''
marker = "func resetChangedCandidateHealthLockedV4("
if "func candidateHealthConfigCurrentLockedV4(" not in hs:
    if marker not in hs:
        raise SystemExit("resetChangedCandidateHealthLockedV4 marker missing")
    hs = hs.replace(marker, helper + marker, 1)

hs = patch_func(
    hs,
    "func candidateWouldBeSelectableV4(",
    "\tv4Runtime.RLock()\n",
    "\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {\n\t\tv4Runtime.RUnlock()\n\t\treturn true\n\t}\n",
)
hs = patch_func(
    hs,
    "func acquireCandidateHealthV4(",
    "\tv4Runtime.Lock()\n\tdefer v4Runtime.Unlock()\n",
    "\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {\n\t\treturn true, false\n\t}\n",
)
hs = patch_func(
    hs,
    "func recordCandidateFailureV4(",
    "\tv4Runtime.Lock()\n\tdefer v4Runtime.Unlock()\n",
    "\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {\n\t\treturn\n\t}\n",
)
hs = patch_func(
    hs,
    "func recordCandidateSuccessV4(",
    "\tv4Runtime.Lock()\n\tdefer v4Runtime.Unlock()\n",
    "\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {\n\t\treturn\n\t}\n",
)
hs = patch_func(
    hs,
    "func releaseCandidateProbeV4(",
    "\tv4Runtime.Lock()\n\tdefer v4Runtime.Unlock()\n",
    "\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {\n\t\treturn\n\t}\n",
)
hp.write_text(hs)


tp = Path("src/issue13_health_test.go")
ts = tp.read_text()
old_fixture = '''func issue13Fixture() (*Policy, *PolicyRule, []*PolicyCandidate) {
\ta := &PolicyCandidate{ID: "a", Name: "A", Provider: "codex", AuthIndex: "idx-a", Enabled: true, Priority: 100, Weight: 1}
\tb := &PolicyCandidate{ID: "b", Name: "B", Provider: "codex", AuthIndex: "idx-b", Enabled: true, Priority: 90, Weight: 1}
\tr := &PolicyRule{ID: "r1", Name: "rule", Strategy: strategyOrdered, Candidates: []*PolicyCandidate{a, b}, Failover: defaultFailover()}
\tp := &Policy{Name: "policy", KeyFingerprint: "fp", Enabled: true, Rules: []*PolicyRule{r}}
\treturn p, r, []*PolicyCandidate{a, b}
}
'''
new_fixture = '''func issue13Fixture() (*Policy, *PolicyRule, []*PolicyCandidate) {
\ta := &PolicyCandidate{ID: "a", Name: "A", Provider: "codex", AuthIndex: "idx-a", Enabled: true, Priority: 100, Weight: 1}
\tb := &PolicyCandidate{ID: "b", Name: "B", Provider: "codex", AuthIndex: "idx-b", Enabled: true, Priority: 90, Weight: 1}
\tr := &PolicyRule{ID: "r1", Name: "rule", Strategy: strategyOrdered, Candidates: []*PolicyCandidate{a, b}, Failover: defaultFailover()}
\tp := &Policy{Name: "policy", KeyFingerprint: "fp", Enabled: true, Rules: []*PolicyRule{r}}
\tv4Runtime.Lock()
\tif v4Runtime.state.Policies == nil {
\t\tv4Runtime.state.Policies = map[string]*Policy{}
\t}
\tv4Runtime.state.Policies[p.KeyFingerprint] = clonePolicyV4(p)
\tv4Runtime.Unlock()
\treturn p, r, []*PolicyCandidate{a, b}
}
'''
if old_fixture in ts:
    ts = ts.replace(old_fixture, new_fixture, 1)
elif new_fixture not in ts:
    raise SystemExit("issue13Fixture block mismatch")

old_reset = '''func resetIssue13Health(t *testing.T) {
\tt.Helper()
\tv4Runtime.Lock()
\told := v4Runtime.health
\tv4Runtime.health = map[string]*candidateHealthState{}
\tv4Runtime.Unlock()
\tt.Cleanup(func() {
\t\tv4Runtime.Lock()
\t\tv4Runtime.health = old
\t\tv4Runtime.Unlock()
\t})
}
'''
new_reset = '''func resetIssue13Health(t *testing.T) {
\tt.Helper()
\tv4Runtime.Lock()
\told := v4Runtime.health
\toldState := cloneV4State(v4Runtime.state)
\tv4Runtime.health = map[string]*candidateHealthState{}
\tv4Runtime.Unlock()
\tt.Cleanup(func() {
\t\tv4Runtime.Lock()
\t\tv4Runtime.health = old
\t\tv4Runtime.state = oldState
\t\tv4Runtime.Unlock()
\t})
}
'''
if old_reset in ts:
    ts = ts.replace(old_reset, new_reset, 1)
elif new_reset not in ts:
    raise SystemExit("resetIssue13Health block mismatch")

if "TestIssue13SupersededCandidateResultsCannotRecreateHealth" not in ts:
    ts += r'''

func TestIssue13SupersededCandidateResultsCannotRecreateHealth(t *testing.T) {
\tresetIssue13Health(t)
\tp, r, ranked := issue13Fixture()
\toldCandidate := ranked[0]
\tkey := candidateHealthKeyV4(p, r, oldCandidate)

\trecordCandidateFailureV4(p, r, oldCandidate, 503, nil, nil)
\tchanged := clonePolicyV4(p)
\tchanged.Rules[0].Candidates[0].OverrideModel = "fixed-model"
\tv4Runtime.Lock()
\tresetChangedCandidateHealthLockedV4(v4Runtime.state.Policies[p.KeyFingerprint], changed)
\tv4Runtime.state.Policies[p.KeyFingerprint] = clonePolicyV4(changed)
\t_, presentAfterSave := v4Runtime.health[key]
\tv4Runtime.Unlock()
\tif presentAfterSave {
\t\tt.Fatal("material config save retained old candidate health")
\t}

\trecordCandidateFailureV4(p, r, oldCandidate, 503, nil, nil)
\trecordCandidateSuccessV4(p, r, oldCandidate, false)
\treleaseCandidateProbeV4(p, r, oldCandidate, true)
\tv4Runtime.RLock()
\t_, recreated := v4Runtime.health[key]
\tv4Runtime.RUnlock()
\tif recreated {
\t\tt.Fatal("superseded candidate result recreated health for replacement config")
\t}

\tcurrentRule := changed.Rules[0]
\tcurrentCandidate := currentRule.Candidates[0]
\trecordCandidateFailureV4(changed, currentRule, currentCandidate, 503, nil, nil)
\tv4Runtime.RLock()
\tcurrent := v4Runtime.health[key]
\tv4Runtime.RUnlock()
\tif current == nil || current.State != healthOpen {
\t\tt.Fatalf("current replacement candidate failure was ignored: %#v", current)
\t}
}
'''

tp.write_text(ts)
