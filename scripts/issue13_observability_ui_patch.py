from pathlib import Path


def once(text, old, new, label):
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"{label}: expected one match, got {n}")
    return text.replace(old, new, 1)

# v4_types.go: keep per-attempt health skips internally until observation enriches reasons.
p = Path("src/v4_types.go")
s = p.read_text()
old = '''\tError            string          `json:"error,omitempty"`
\truleSnapshot     *PolicyRule
}'''
new = '''\tError            string          `json:"error,omitempty"`
\truleSnapshot     *PolicyRule
\thealthSkips      [][]string
}'''
if old in s:
    s = s.replace(old, new, 1)
elif new not in s:
    raise SystemExit("v4_types RoutingEvent marker mismatch")
p.write_text(s)

# v4_health.go: return precise skip reasons from the same atomic acquisition decision.
p = Path("src/v4_health.go")
s = p.read_text()
if '"fmt"' not in s:
    s = once(s, 'import (\n\t"net/http"', 'import (\n\t"fmt"\n\t"net/http"', 'v4_health fmt import')

old = '''func acquireCandidateHealthV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) (bool, bool) {
\tkey := candidateHealthKeyV4(p, r, c)
\tif key == "" {
\t\treturn true, false
\t}
\tv4Runtime.Lock()
\tdefer v4Runtime.Unlock()
\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {
\t\treturn true, false
\t}
\th := candidateHealthStateLockedV4(key)
\tif h.State == healthClosed {
\t\treturn true, false
\t}
\tif h.ProbeInFlight {
\t\treturn false, false
\t}
\tif h.State == healthOpen && !h.NextProbeAt.IsZero() && now.Before(h.NextProbeAt) {
\t\treturn false, false
\t}
\th.State = healthHalfOpen
\th.ProbeInFlight = true
\treturn true, true
}
'''
new = '''func acquireCandidateHealthWithReasonV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) (bool, bool, string) {
\tkey := candidateHealthKeyV4(p, r, c)
\tif key == "" {
\t\treturn true, false, ""
\t}
\tv4Runtime.Lock()
\tdefer v4Runtime.Unlock()
\tif !candidateHealthConfigCurrentLockedV4(p, r, c) {
\t\treturn true, false, ""
\t}
\th := candidateHealthStateLockedV4(key)
\tif h.State == healthClosed {
\t\treturn true, false, ""
\t}
\tname := strings.TrimSpace(c.Name)
\tif name == "" {
\t\tname = c.ID
\t}
\tif h.ProbeInFlight {
\t\treturn false, false, fmt.Sprintf("候选 %s 健康状态=%s，已有恢复探测正在执行，跳过", name, h.State)
\t}
\tif h.State == healthOpen && !h.NextProbeAt.IsZero() && now.Before(h.NextProbeAt) {
\t\tretryMs := h.NextProbeAt.Sub(now).Milliseconds()
\t\tif retryMs < 0 {
\t\t\tretryMs = 0
\t\t}
\t\treturn false, false, fmt.Sprintf("候选 %s 健康状态=OPEN，约 %dms 后允许恢复探测，跳过", name, retryMs)
\t}
\th.State = healthHalfOpen
\th.ProbeInFlight = true
\treturn true, true, ""
}

func acquireCandidateHealthV4(p *Policy, r *PolicyRule, c *PolicyCandidate, now time.Time) (bool, bool) {
\tok, probe, _ := acquireCandidateHealthWithReasonV4(p, r, c, now)
\treturn ok, probe
}
'''
s = once(s, old, new, 'acquire health detailed')

old = '''func chooseHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int, acquire bool) (*PolicyCandidate, bool) {
\tphases := 1
\tif action == failSamePriorityFirst {
\t\tphases = 2
\t}
\tnow := time.Now()
\tfor phase := 0; phase < phases; phase++ {
\t\tfor _, c := range ranked {
\t\t\tif c == nil || attempted[c.ID] || !candidateMatchesFailoverActionV4(c, action, currentPriority, phase) {
\t\t\t\tcontinue
\t\t\t}
\t\t\tif acquire {
\t\t\t\tif ok, probe := acquireCandidateHealthV4(p, r, c, now); ok {
\t\t\t\t\treturn c, probe
\t\t\t\t}
\t\t\t} else if candidateWouldBeSelectableV4(p, r, c, now) {
\t\t\t\treturn c, false
\t\t\t}
\t\t}
\t}
\treturn nil, false
}

func nextHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) (*PolicyCandidate, bool) {
\treturn chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, true)
}

func peekHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) *PolicyCandidate {
\tc, _ := chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, false)
\treturn c
}
'''
new = '''func chooseHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int, acquire bool, healthSkips *[]string) (*PolicyCandidate, bool) {
\tphases := 1
\tif action == failSamePriorityFirst {
\t\tphases = 2
\t}
\tnow := time.Now()
\tfor phase := 0; phase < phases; phase++ {
\t\tfor _, c := range ranked {
\t\t\tif c == nil || attempted[c.ID] || !candidateMatchesFailoverActionV4(c, action, currentPriority, phase) {
\t\t\t\tcontinue
\t\t\t}
\t\t\tif acquire {
\t\t\t\tok, probe, skipReason := acquireCandidateHealthWithReasonV4(p, r, c, now)
\t\t\t\tif ok {
\t\t\t\t\treturn c, probe
\t\t\t\t}
\t\t\t\tif healthSkips != nil && skipReason != "" {
\t\t\t\t\t*healthSkips = append(*healthSkips, skipReason)
\t\t\t\t}
\t\t\t} else if candidateWouldBeSelectableV4(p, r, c, now) {
\t\t\t\treturn c, false
\t\t\t}
\t\t}
\t}
\treturn nil, false
}

func nextHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) (*PolicyCandidate, bool) {
\treturn chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, true, nil)
}

func nextHealthyCandidateWithSkipsV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) (*PolicyCandidate, bool, []string) {
\tskips := []string{}
\tc, probe := chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, true, &skips)
\treturn c, probe, skips
}

func peekHealthyCandidateV4(p *Policy, r *PolicyRule, ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) *PolicyCandidate {
\tc, _ := chooseHealthyCandidateV4(p, r, ranked, attempted, action, currentPriority, false, nil)
\treturn c
}
'''
s = once(s, old, new, 'choose health with skips')
p.write_text(s)

# v4_execution.go: capture skips aligned with each actual attempt.
p = Path("src/v4_execution.go")
s = p.read_text()
old = '''\t\tc, probe := nextHealthyCandidateV4(p, r, ranked, attempted, nextAction, currentPriority)
\t\tnextAction = failNext
\t\tif c == nil {
\t\t\tbreak
\t\t}'''
new = '''\t\tc, probe, healthSkips := nextHealthyCandidateWithSkipsV4(p, r, ranked, attempted, nextAction, currentPriority)
\t\tnextAction = failNext
\t\tif c == nil {
\t\t\tbreak
\t\t}
\t\tevent.healthSkips = append(event.healthSkips, healthSkips)'''
count = s.count(old)
if count != 2:
    raise SystemExit(f"execution selection blocks: expected 2, got {count}")
s = s.replace(old, new)
p.write_text(s)

# v4_observability.go: replace static strategy explanation when health actually skipped candidates.
p = Path("src/v4_observability.go")
s = p.read_text()
old = '''\treasons := make([]string, len(ev.Attempts))
\tfor i := range ev.Attempts {
\t\tif i == 0 {
\t\t\treasons[i] = initialSelectionReasonV6(rule, ev.Attempts[i])
\t\t\tcontinue
\t\t}
\t\treasons[i] = failoverSelectionReasonV6(ev, rule, i)
\t}
\tev.SelectionReasons = reasons
}
'''
new = '''\treasons := make([]string, len(ev.Attempts))
\tfor i := range ev.Attempts {
\t\tbase := ""
\t\tif i == 0 {
\t\t\tbase = initialSelectionReasonV6(rule, ev.Attempts[i])
\t\t} else {
\t\t\tbase = failoverSelectionReasonV6(ev, rule, i)
\t\t}
\t\tif i < len(ev.healthSkips) && len(ev.healthSkips[i]) > 0 {
\t\t\tbase = healthAwareSelectionReasonV6(ev.healthSkips[i], ev.Attempts[i])
\t\t}
\t\treasons[i] = base
\t}
\tev.SelectionReasons = reasons
}

func healthAwareSelectionReasonV6(skips []string, a attemptResult) string {
\tname := strings.TrimSpace(a.Candidate)
\tif name == "" {
\t\tname = "当前候选"
\t}
\treturn "策略排序后，" + strings.Join(skips, "；") + "；健康过滤后实际执行候选 " + name
}
'''
s = once(s, old, new, 'observability health reason')
p.write_text(s)

# v4_management.go: expose current selectability/effective candidate for diagnostics.
p = Path("src/v4_management.go")
s = p.read_text()
old = '''\tranked := rankCandidatesV4(p, rule, http.Header{}, map[string]any{})
\titems := []map[string]any{}
\tfor i, c := range ranked {
\t\titems = append(items, map[string]any{"order": i + 1, "name": c.Name, "provider": c.Provider, "auth_index": c.AuthIndex, "priority": c.Priority, "weight": c.Weight, "override_model": c.OverrideModel, "health": candidateHealthViewV4(p, rule, c)})
\t}'''
new = '''\tranked := rankCandidatesV4(p, rule, http.Header{}, map[string]any{})
\titems := []map[string]any{}
\tnow := time.Now()
\teffectiveFound := false
\tfor i, c := range ranked {
\t\tselectable := candidateWouldBeSelectableV4(p, rule, c, now)
\t\teffective := selectable && !effectiveFound
\t\tif effective {
\t\t\teffectiveFound = true
\t\t}
\t\titems = append(items, map[string]any{"order": i + 1, "name": c.Name, "provider": c.Provider, "auth_index": c.AuthIndex, "priority": c.Priority, "weight": c.Weight, "override_model": c.OverrideModel, "health": candidateHealthViewV4(p, rule, c), "selectable": selectable, "effective": effective})
\t}'''
s = once(s, old, new, 'diagnose health selectability')
p.write_text(s)

# ui.js: show health state, retry/probe information and actual effective candidate.
p = Path("src/ui.js")
s = p.read_text()
old = '''    '<th align=left>#</th><th align=left>候选</th><th align=left>Provider</th>' +
    (f.priority ? '<th align=left>Priority</th>' : '') + (f.weight ? '<th align=left>Weight</th>' : '') +
    '<th align=left>AuthIndex</th></tr>';
  for (const c of d.ranked_candidates || []) {
    h += '<tr><td>' + c.order + '</td><td>' + esc(c.name) + '</td><td>' + esc(c.provider) + '</td>' +
      (f.priority ? '<td>' + c.priority + '</td>' : '') + (f.weight ? '<td>' + c.weight + '</td>' : '') +
      '<td class="mono">' + esc(c.auth_index) + '</td></tr>';
  }'''
new = '''    '<th align=left>#</th><th align=left>候选</th><th align=left>Provider</th>' +
    (f.priority ? '<th align=left>Priority</th>' : '') + (f.weight ? '<th align=left>Weight</th>' : '') +
    '<th align=left>AuthIndex</th><th align=left>健康</th><th align=left>当前路由</th></tr>';
  for (const c of d.ranked_candidates || []) {
    const health = c.health || {};
    const state = String(health.state || 'closed').toUpperCase();
    let healthText = state;
    if (health.probe_in_flight) healthText += ' · probe 进行中';
    else if (Number(health.retry_in_ms || 0) > 0) healthText += ' · ' + Number(health.retry_in_ms) + 'ms 后探测';
    const routeText = c.effective ? '✓ 当前首选' : (c.selectable ? '可选' : '跳过');
    h += '<tr><td>' + c.order + '</td><td>' + esc(c.name) + '</td><td>' + esc(c.provider) + '</td>' +
      (f.priority ? '<td>' + c.priority + '</td>' : '') + (f.weight ? '<td>' + c.weight + '</td>' : '') +
      '<td class="mono">' + esc(c.auth_index) + '</td><td>' + esc(healthText) + '</td><td>' + esc(routeText) + '</td></tr>';
  }'''
s = once(s, old, new, 'diagnostic UI health columns')
p.write_text(s)

# Regression coverage.
p = Path("src/issue13_health_test.go")
s = p.read_text()
if "TestIssue13HealthSkipReasonExplainsActualSelection" not in s:
    s += '''

func TestIssue13HealthSkipReasonExplainsActualSelection(t *testing.T) {
\tresetIssue13Health(t)
\tp, r, ranked := issue13Fixture()
\trecordCandidateFailureV4(p, r, ranked[0], 503, nil, nil)
\tc, probe, skips := nextHealthyCandidateWithSkipsV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
\tif c == nil || c.ID != "b" || probe {
\t\tt.Fatalf("got candidate=%v probe=%v, want healthy B", c, probe)
\t}
\tif len(skips) != 1 || !strings.Contains(skips[0], "A") || !strings.Contains(skips[0], "OPEN") {
\t\tt.Fatalf("health skips=%#v, want A OPEN explanation", skips)
\t}
\tev := RoutingEvent{Attempts: []attemptResult{{Candidate: c.Name, Provider: c.Provider, AuthIndex: c.AuthIndex}}, ruleSnapshot: cloneRuleV4(r), healthSkips: [][]string{skips}}
\tenrichRoutingSelectionReasonsV6(&ev)
\tif len(ev.SelectionReasons) != 1 || !strings.Contains(ev.SelectionReasons[0], "健康过滤") || !strings.Contains(ev.SelectionReasons[0], "A") || !strings.Contains(ev.SelectionReasons[0], "B") {
\t\tt.Fatalf("selection reason=%#v, want health-aware A->B explanation", ev.SelectionReasons)
\t}
}
'''
p.write_text(s)
