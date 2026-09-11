from pathlib import Path

exec_path = Path('src/v4_execution.go')
s = exec_path.read_text()
old = 'c, _ := nextHealthyCandidateV4(p, r, ranked, attempted, failNext, currentPriority)'
if s.count(old) != 2:
    raise SystemExit(f'expected 2 health selections, got {s.count(old)}')
s = s.replace(old, 'c, probe := nextHealthyCandidateV4(p, r, ranked, attempted, failNext, currentPriority)')
repls = {
    'recordCandidateFailureV4(p, r, c, ar.Status, err, resp.Headers)': 'recordCandidateFailureV4(p, r, c, ar.Status, err, resp.Headers, probe)',
    'recordCandidateFailureV4(p, r, c, ar.Status, err, nil)': 'recordCandidateFailureV4(p, r, c, ar.Status, err, nil, probe)',
    'recordCandidateFailureV4(p, r, c, 0, err, nil)': 'recordCandidateFailureV4(p, r, c, 0, err, nil, probe)',
    'recordCandidateFailureV4(p, r, c, sr.StatusCode, nil, sr.Headers)': 'recordCandidateFailureV4(p, r, c, sr.StatusCode, nil, sr.Headers, probe)',
    'recordCandidateFailureV4(p, r, c, 0, lastErr, sr.Headers)': 'recordCandidateFailureV4(p, r, c, 0, lastErr, sr.Headers, probe)',
    'recordCandidateFailureV4(p, r, c, statusFromError(e), e, sr.Headers)': 'recordCandidateFailureV4(p, r, c, statusFromError(e), e, sr.Headers, probe)',
}
for a,b in repls.items():
    if a not in s:
        raise SystemExit(f'missing execution marker: {a}')
    s = s.replace(a,b)
exec_path.write_text(s)

health_path = Path('src/v4_health.go')
h = health_path.read_text()
start = h.index('func recordCandidateFailureV4(')
end = h.index('\nfunc recordCandidateSuccessV4(', start)
new_func = r'''func recordCandidateFailureV4(p *Policy, r *PolicyRule, c *PolicyCandidate, status int, err error, headers http.Header, probeAttempt ...bool) {
	key := candidateHealthKeyV4(p, r, c)
	if key == "" {
		return
	}
	now := time.Now()
	v4Runtime.Lock()
	defer v4Runtime.Unlock()
	h := candidateHealthStateLockedV4(key)
	isProbe := len(probeAttempt) > 0 && probeAttempt[0]
	level := h.BackoffLevel
	if isProbe && level < 30 {
		level++
	}
	cooldown, qualifies := candidateFailureCooldownV4(status, err, headers, level, now)
	if !qualifies {
		// A half-open probe that reaches the upstream and receives a request-specific
		// non-health status proves transport reachability; do not strand the lease.
		if isProbe && (h.ProbeInFlight || h.State == healthHalfOpen) {
			h.State = healthClosed
			h.ProbeInFlight = false
			h.ConsecutiveFailures = 0
			h.BackoffLevel = 0
			h.NextProbeAt = time.Time{}
			h.OpenedAt = time.Time{}
		}
		return
	}

	proposedDeadline := now.Add(cooldown)
	wasClosed := h.State == "" || h.State == healthClosed
	switch {
	case isProbe:
		// Only a failed half-open recovery probe advances exponential backoff.
		h.BackoffLevel = level
		h.State = healthOpen
		h.ProbeInFlight = false
		h.OpenedAt = now
		h.NextProbeAt = proposedDeadline
	case wasClosed:
		// First failure opens the circuit at the base cooldown. Concurrent attempts
		// that were already in flight must not escalate the backoff level.
		h.State = healthOpen
		h.ProbeInFlight = false
		h.OpenedAt = now
		h.NextProbeAt = proposedDeadline
	default:
		// This is a stale normal attempt that started before another request opened
		// the circuit (or while a recovery probe is now running). Do not alter the
		// state, probe lease, or backoff. Only stronger auth/rate-limit deadlines may
		// extend an existing open deadline; never shorten it.
		if status == 401 || status == 403 || status == 429 {
			if h.NextProbeAt.IsZero() || proposedDeadline.After(h.NextProbeAt) {
				h.NextProbeAt = proposedDeadline
			}
		}
	}

	h.ConsecutiveFailures++
	h.LastStatus = status
	h.LastError = ""
	if err != nil {
		h.LastError = err.Error()
	}
	h.LastFailureAt = now
}
'''
health_path.write_text(h[:start] + new_func + h[end:])

test_path = Path('src/issue13_health_test.go')
t = test_path.read_text()
old_probe = '''\tv4Runtime.Lock()
\tv4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
\tv4Runtime.Unlock()
\tif got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1)); got == nil || got.ID != "a" || !probe {
\t\tt.Fatalf("expected half-open A probe, got=%v probe=%v", got, probe)
\t}
\trecordCandidateFailureV4(p, r, a, 503, nil, nil)
'''
new_probe = '''\tv4Runtime.Lock()
\tv4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
\tv4Runtime.Unlock()
\tgot, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
\tif got == nil || got.ID != "a" || !probe {
\t\tt.Fatalf("expected half-open A probe, got=%v probe=%v", got, probe)
\t}
\trecordCandidateFailureV4(p, r, a, 503, nil, nil, probe)
'''
if old_probe not in t:
    raise SystemExit('probe test marker missing')
t = t.replace(old_probe, new_probe, 1)
append = r'''

func TestIssue13ConcurrentClosedFailuresDoNotEscalateBackoff(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.RLock()
	deadline := v4Runtime.health[key].NextProbeAt
	level := v4Runtime.health[key].BackoffLevel
	v4Runtime.RUnlock()
	if level != 0 {
		t.Fatalf("initial normal failure backoff=%d, want 0", level)
	}
	for i := 0; i < 8; i++ {
		recordCandidateFailureV4(p, r, a, 503, nil, nil)
	}
	v4Runtime.RLock()
	after := *v4Runtime.health[key]
	v4Runtime.RUnlock()
	if after.BackoffLevel != 0 {
		t.Fatalf("concurrent normal failures escalated backoff=%d, want 0", after.BackoffLevel)
	}
	if !after.NextProbeAt.Equal(deadline) {
		t.Fatalf("concurrent normal failures moved deadline: before=%v after=%v", deadline, after.NextProbeAt)
	}
}

func TestIssue13LateFailureNeverShortensRetryAfterDeadline(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	recordCandidateFailureV4(p, r, a, 429, nil, http.Header{"Retry-After": []string{"240"}})
	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.RLock()
	longDeadline := v4Runtime.health[key].NextProbeAt
	v4Runtime.RUnlock()
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	v4Runtime.RLock()
	after := v4Runtime.health[key].NextProbeAt
	v4Runtime.RUnlock()
	if !after.Equal(longDeadline) {
		t.Fatalf("late transient failure changed longer Retry-After deadline: before=%v after=%v", longDeadline, after)
	}
}
'''
test_path.write_text(t.rstrip() + append + '\n')
