package main

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func issue13Fixture() (*Policy, *PolicyRule, []*PolicyCandidate) {
	a := &PolicyCandidate{ID: "a", Name: "A", Provider: "codex", AuthIndex: "idx-a", Enabled: true, Priority: 100, Weight: 1}
	b := &PolicyCandidate{ID: "b", Name: "B", Provider: "codex", AuthIndex: "idx-b", Enabled: true, Priority: 90, Weight: 1}
	r := &PolicyRule{ID: "r1", Name: "rule", Strategy: strategyOrdered, Candidates: []*PolicyCandidate{a, b}, Failover: defaultFailover()}
	p := &Policy{Name: "policy", KeyFingerprint: "fp", Enabled: true, Rules: []*PolicyRule{r}}
	return p, r, []*PolicyCandidate{a, b}
}

func resetIssue13Health(t *testing.T) {
	t.Helper()
	v4Runtime.Lock()
	old := v4Runtime.health
	v4Runtime.health = map[string]*candidateHealthState{}
	v4Runtime.Unlock()
	t.Cleanup(func() {
		v4Runtime.Lock()
		v4Runtime.health = old
		v4Runtime.Unlock()
	})
}

func TestIssue13FailureSkipsCandidateUntilSingleHalfOpenProbe(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]

	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	attempted := map[string]bool{}
	got, probe := nextHealthyCandidateV4(p, r, ranked, attempted, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "b" || probe {
		t.Fatalf("during cooldown got=(%v, probe=%v), want B non-probe", got, probe)
	}

	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()

	got, probe = nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "a" || !probe {
		t.Fatalf("after cooldown got=(%v, probe=%v), want A half-open probe", got, probe)
	}

	got, probe = nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "b" || probe {
		t.Fatalf("concurrent probe got=(%v, probe=%v), want B while A probe is in flight", got, probe)
	}
}

func TestIssue13ProbeSuccessFailsBackToPreferredCandidate(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()
	got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "a" || !probe {
		t.Fatalf("expected A probe, got=%v probe=%v", got, probe)
	}

	recordCandidateSuccessV4(p, r, a)
	got, probe = nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "a" || probe {
		t.Fatalf("after successful probe got=(%v, probe=%v), want normal A", got, probe)
	}
	view := candidateHealthViewV4(p, r, a)
	if view.State != healthClosed || view.BackoffLevel != 0 || view.ConsecutiveFailures != 0 {
		t.Fatalf("health after recovery = %#v", view)
	}
}

func TestIssue13ProbeFailureUsesExponentialBackoff(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.RLock()
	first := v4Runtime.health[key].NextProbeAt.Sub(v4Runtime.health[key].LastFailureAt)
	v4Runtime.RUnlock()
	if first != 30*time.Second {
		t.Fatalf("first cooldown=%v, want 30s", first)
	}

	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()
	got, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if got == nil || got.ID != "a" || !probe {
		t.Fatalf("expected half-open A probe, got=%v probe=%v", got, probe)
	}
	recordCandidateFailureV4(p, r, a, 503, nil, nil, probe)
	v4Runtime.RLock()
	second := v4Runtime.health[key].NextProbeAt.Sub(v4Runtime.health[key].LastFailureAt)
	v4Runtime.RUnlock()
	if second != 60*time.Second {
		t.Fatalf("second cooldown=%v, want 60s", second)
	}
}

func TestIssue13RateLimitHonorsRetryAfter(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	h := http.Header{"Retry-After": []string{"120"}}
	recordCandidateFailureV4(p, r, a, 429, nil, h)
	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.RLock()
	cooldown := v4Runtime.health[key].NextProbeAt.Sub(v4Runtime.health[key].LastFailureAt)
	v4Runtime.RUnlock()
	if cooldown < 120*time.Second {
		t.Fatalf("429 cooldown=%v, want >=120s", cooldown)
	}
}

func TestIssue13NonHealthFailureDoesNotOpenClosedCandidate(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	recordCandidateFailureV4(p, r, a, 400, errors.New("bad request"), nil)
	view := candidateHealthViewV4(p, r, a)
	if view.State != healthClosed {
		t.Fatalf("400 must not open circuit: %#v", view)
	}
}

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
