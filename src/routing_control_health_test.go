package main

import (
	"errors"
	"testing"
	"time"
)

func TestRoutingControlFailureDoesNotOpenClosedCandidate(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]

	recordCandidateExecutionFailureV8(p, r, a, 422, errSchedulerTicketUnclaimed, nil, false)
	view := candidateHealthViewV4(p, r, a)
	if view.State != healthClosed || view.ConsecutiveFailures != 0 || view.ProbeInFlight {
		t.Fatalf("routing control failure mutated closed candidate health: %#v", view)
	}
}

func TestRoutingControlFailureReleasesHalfOpenProbeWithoutClosingCircuit(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]

	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	key := candidateHealthKeyV4(p, r, a)
	v4Runtime.Lock()
	v4Runtime.health[key].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()

	probeCandidate, probe := nextHealthyCandidateV4(p, r, ranked, map[string]bool{}, failNext, int(^uint(0)>>1))
	if probeCandidate == nil || probeCandidate.ID != "a" || !probe {
		t.Fatalf("expected A half-open probe, got=%v probe=%v", probeCandidate, probe)
	}

	recordCandidateExecutionFailureV8(p, r, probeCandidate, 422, errSchedulerTicketUnclaimed, nil, probe)
	view := candidateHealthViewV4(p, r, a)
	if view.State != healthOpen {
		t.Fatalf("control failure closed the circuit without executing the credential: %#v", view)
	}
	if view.ProbeInFlight {
		t.Fatalf("control failure stranded half-open probe ownership: %#v", view)
	}
	if view.ConsecutiveFailures != 1 || view.BackoffLevel != 0 {
		t.Fatalf("control failure changed candidate failure/backoff counters: %#v", view)
	}
}

func TestRealExecutionFailureStillUpdatesCandidateHealth(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	a := ranked[0]

	recordCandidateExecutionFailureV8(p, r, a, 503, errors.New("upstream 503"), nil, false)
	view := candidateHealthViewV4(p, r, a)
	if view.State != healthOpen || view.ConsecutiveFailures != 1 {
		t.Fatalf("real upstream failure did not update health: %#v", view)
	}
}
