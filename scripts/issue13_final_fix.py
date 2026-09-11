from pathlib import Path
import re


def block(lines):
    return "\n".join(lines) + "\n"


hp = Path("src/v4_health.go")
hs = hp.read_text()
pattern = re.compile(
    r"func recordCandidateSuccessV4\(p \*Policy, r \*PolicyRule, c \*PolicyCandidate\) \{.*?\n\}\n\nfunc releaseCandidateProbeV4",
    re.S,
)
success_body = block(
    [
        "func recordCandidateSuccessV4(p *Policy, r *PolicyRule, c *PolicyCandidate, probeOwned bool) {",
        '\tkey := candidateHealthKeyV4(p, r, c)',
        '\tif key == "" {',
        '\t\treturn',
        '\t}',
        '\tnow := time.Now()',
        '\tv4Runtime.Lock()',
        '\tdefer v4Runtime.Unlock()',
        '\th := candidateHealthStateLockedV4(key)',
        '\th.LastSuccessAt = now',
        '',
        '\twasClosed := h.State == "" || h.State == healthClosed',
        '\townsCurrentProbe := probeOwned && h.State == healthHalfOpen && h.ProbeInFlight',
        '\tif !wasClosed && !ownsCurrentProbe {',
        '\t\t// A stale normal success is telemetry only; it cannot cancel an OPEN',
        "\t\t// recovery cycle or another request's HALF_OPEN probe lease.",
        '\t\treturn',
        '\t}',
        '',
        '\th.State = healthClosed',
        '\th.ProbeInFlight = false',
        '\th.ConsecutiveFailures = 0',
        '\th.BackoffLevel = 0',
        '\th.LastStatus = 0',
        '\th.LastError = ""',
        '\th.OpenedAt = time.Time{}',
        '\th.NextProbeAt = time.Time{}',
        '}',
        '',
    ]
)
hs, n = pattern.subn(success_body + "func releaseCandidateProbeV4", hs, count=1)
if n != 1:
    raise SystemExit(f"recordCandidateSuccessV4 replace count={n}")

old = block(
    [
        '\th.ProbeInFlight = false',
        '\th.State = healthOpen',
        '\th.NextProbeAt = time.Now()',
        '}',
    ]
)
new = block(
    [
        '\th.ProbeInFlight = false',
        '\th.State = healthOpen',
        '\tnow := time.Now()',
        '\tif h.NextProbeAt.IsZero() || h.NextProbeAt.Before(now) {',
        '\t\th.NextProbeAt = now',
        '\t}',
        '}',
    ]
)
if hs.count(old) != 1:
    raise SystemExit(f"release deadline block count={hs.count(old)}")
hs = hs.replace(old, new, 1)
hp.write_text(hs)

ep = Path("src/v4_execution.go")
es = ep.read_text()
old_call = "recordCandidateSuccessV4(p, r, c)"
if es.count(old_call) != 2:
    raise SystemExit(f"production success calls={es.count(old_call)}")
es = es.replace(old_call, "recordCandidateSuccessV4(p, r, c, probe)", 2)

old = block(
    [
        '\tvar lastErr error',
        '\tdefer func() {',
        '\t\tif x := recover(); x != nil {',
        '\t\t\t_ = closeOutputStream(outStreamID, fmt.Sprintf("panic: %v", x))',
        '\t\t}',
        '\t}()',
    ]
)
new = block(
    [
        '\tvar lastErr error',
        '\tvar activeCandidate *PolicyCandidate',
        '\tactiveProbe := false',
        '\tdefer func() {',
        '\t\tif x := recover(); x != nil {',
        '\t\t\t// Never strand a HALF_OPEN lease if the stream worker panics.',
        '\t\t\treleaseCandidateProbeV4(p, r, activeCandidate, activeProbe)',
        '\t\t\t_ = closeOutputStream(outStreamID, fmt.Sprintf("panic: %v", x))',
        '\t\t}',
        '\t}()',
    ]
)
if es.count(old) != 1:
    raise SystemExit(f"panic defer count={es.count(old)}")
es = es.replace(old, new, 1)

old = block(
    [
        '\t\tif c == nil {',
        '\t\t\tbreak',
        '\t\t}',
        '\t\tattempted[c.ID] = true',
        '\t\tcurrentPriority = c.Priority',
        '\t\tmodel := c.OverrideModel',
    ]
)
new = block(
    [
        '\t\tif c == nil {',
        '\t\t\tbreak',
        '\t\t}',
        '\t\tactiveCandidate = c',
        '\t\tactiveProbe = probe',
        '\t\tattempted[c.ID] = true',
        '\t\tcurrentPriority = c.Priority',
        '\t\tmodel := c.OverrideModel',
    ]
)
if es.count(old) != 1:
    raise SystemExit(f"stream acquisition count={es.count(old)}")
es = es.replace(old, new, 1)
ep.write_text(es)

tp = Path("src/issue13_health_test.go")
ts = tp.read_text()
old_test_call = "recordCandidateSuccessV4(p, r, a)"
if ts.count(old_test_call) != 1:
    raise SystemExit(f"test old success calls={ts.count(old_test_call)}")
ts = ts.replace(old_test_call, "recordCandidateSuccessV4(p, r, a, true)", 1)

if "TestIssue13StaleSuccessCannotCancelRecoveryCycle" not in ts:
    ts += r'''

func TestIssue13StaleSuccessCannotCancelRecoveryCycle(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	key := candidateHealthKeyV4(p, r, a)

	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	recordCandidateSuccessV4(p, r, a, false)
	v4Runtime.RLock()
	openState := *v4Runtime.health[key]
	v4Runtime.RUnlock()
	if openState.State != healthOpen || openState.ProbeInFlight {
		t.Fatalf("stale normal success cancelled OPEN state: %#v", openState)
	}

	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()
	got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "a" || !probe {
		t.Fatalf("expected A half-open probe, got=%v probe=%v", got, probe)
	}

	recordCandidateSuccessV4(p, r, a, false)
	v4Runtime.RLock()
	half := *v4Runtime.health[key]
	v4Runtime.RUnlock()
	if half.State != healthHalfOpen || !half.ProbeInFlight {
		t.Fatalf("stale success cancelled HALF_OPEN probe: %#v", half)
	}

	recordCandidateSuccessV4(p, r, a, true)
	v4Runtime.RLock()
	closed := *v4Runtime.health[key]
	v4Runtime.RUnlock()
	if closed.State != healthClosed || closed.ProbeInFlight || closed.BackoffLevel != 0 {
		t.Fatalf("probe owner did not close circuit: %#v", closed)
	}
}
'''

if "TestIssue13ProbeReleasePreservesStrongerDeadline" not in ts:
    ts += r'''

func TestIssue13ProbeReleasePreservesStrongerDeadline(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	key := candidateHealthKeyV4(p, r, a)
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()
	if got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1)); got == nil || !probe {
		t.Fatalf("expected probe, got=%v probe=%v", got, probe)
	}
	recordCandidateFailureV4(p, r, a, 429, nil, http.Header{"Retry-After": []string{"240"}}, false)
	v4Runtime.RLock()
	strong := v4Runtime.health[key].NextProbeAt
	v4Runtime.RUnlock()
	releaseCandidateProbeV4(p, r, a, true)
	v4Runtime.RLock()
	after := v4Runtime.health[key].NextProbeAt
	v4Runtime.RUnlock()
	if after.Before(strong) {
		t.Fatalf("probe release shortened stronger deadline: before=%v after=%v", strong, after)
	}
}
'''

tp.write_text(ts)
