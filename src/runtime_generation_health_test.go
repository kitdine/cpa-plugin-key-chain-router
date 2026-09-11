package main

import (
	"testing"
	"time"
)

func setRuntimeGenerationForTestV4(t *testing.T, generation uint64) {
	t.Helper()
	v4Runtime.Lock()
	old := v4Runtime.generation
	v4Runtime.generation = generation
	v4Runtime.Unlock()
	t.Cleanup(func() {
		v4Runtime.Lock()
		v4Runtime.generation = old
		v4Runtime.Unlock()
	})
}

func TestRuntimeGenerationStaleFailureCannotRecreateHealth(t *testing.T) {
	resetIssue13Health(t)
	setRuntimeGenerationForTestV4(t, 41)
	p, r, ranked := issue13Fixture()

	stale, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if stale == nil || stale.ID != "a" || probe {
		t.Fatalf("initial acquisition got=(%v, probe=%v), want A non-probe", stale, probe)
	}
	if stale.runtimeGeneration != 41 {
		t.Fatalf("attempt generation=%d, want 41", stale.runtimeGeneration)
	}

	key := candidateHealthKeyV4(p, r, stale)
	v4Runtime.Lock()
	// Simulate plugin.reconfigure loading the same serialized policy after the CPA
	// credential behind AuthIndex changed: policy identity is unchanged, health is
	// reset, but the runtime generation advances.
	v4Runtime.generation = 42
	v4Runtime.health = map[string]*candidateHealthState{}
	v4Runtime.Unlock()

	recordCandidateFailureV4(p, r, stale, 503, nil, nil, probe)
	v4Runtime.RLock()
	_, recreated := v4Runtime.health[key]
	v4Runtime.RUnlock()
	if recreated {
		t.Fatal("stale generation recreated health after reconfigure")
	}

	fresh, freshProbe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if fresh == nil || fresh.ID != "a" || freshProbe {
		t.Fatalf("fresh acquisition got=(%v, probe=%v), want A non-probe", fresh, freshProbe)
	}
	if fresh.runtimeGeneration != 42 {
		t.Fatalf("fresh attempt generation=%d, want 42", fresh.runtimeGeneration)
	}
	recordCandidateFailureV4(p, r, fresh, 503, nil, nil, freshProbe)
	v4Runtime.RLock()
	h := v4Runtime.health[key]
	v4Runtime.RUnlock()
	if h == nil || h.State != healthOpen {
		t.Fatalf("current generation failure must still open circuit: %#v", h)
	}
}

func TestRuntimeGenerationBlocksAllStaleHealthWrites(t *testing.T) {
	resetIssue13Health(t)
	setRuntimeGenerationForTestV4(t, 70)
	p, r, ranked := issue13Fixture()
	a := ranked[0]
	key := candidateHealthKeyV4(p, r, a)

	// Produce an attempt that owns a HALF_OPEN probe in generation 70.
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()
	stale, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if stale == nil || stale.ID != "a" || !probe || stale.runtimeGeneration != 70 {
		t.Fatalf("stale probe acquisition got=(%v, probe=%v, generation=%d)", stale, probe, stale.runtimeGeneration)
	}

	deadline := time.Now().Add(4 * time.Minute)
	baseline := candidateHealthState{
		State:               healthHalfOpen,
		ConsecutiveFailures: 4,
		BackoffLevel:        3,
		LastStatus:          503,
		LastError:           "new-generation-probe",
		OpenedAt:            time.Now().Add(-time.Minute),
		NextProbeAt:         deadline,
		ProbeInFlight:       true,
	}
	v4Runtime.Lock()
	v4Runtime.generation = 71
	v4Runtime.health = map[string]*candidateHealthState{key: &baseline}
	v4Runtime.Unlock()

	// Every health write path from generation 70 must be ignored. In particular,
	// an old probe owner must not release or close the generation-71 probe.
	releaseCandidateProbeV4(p, r, stale, true)
	recordCandidateSuccessV4(p, r, stale, true)
	recordCandidateFailureV4(p, r, stale, 503, nil, nil, true)

	v4Runtime.RLock()
	got := *v4Runtime.health[key]
	v4Runtime.RUnlock()
	if got != baseline {
		t.Fatalf("stale generation mutated current health:\n got=%#v\nwant=%#v", got, baseline)
	}
}
