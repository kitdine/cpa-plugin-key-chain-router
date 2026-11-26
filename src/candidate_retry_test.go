package main

import (
	"errors"
	"testing"
	"time"
)

func TestCandidateRetryPreservesHalfOpenProbe(t *testing.T) {
	resetIssue13Health(t)
	p, r, ranked := issue13Fixture()
	r.Failover.RetryBeforeSwitch = true
	a := ranked[0]
	recordCandidateFailureV4(p, r, a, 503, nil, nil)
	v4Runtime.Lock()
	v4Runtime.health[candidateHealthKeyV4(p, r, a)].NextProbeAt = time.Now().Add(-time.Millisecond)
	v4Runtime.Unlock()

	c, probe := nextHealthyCandidateV4(p, r, ranked, nil, failNext, 0)
	if c == nil || c.ID != a.ID || !probe {
		t.Fatal("expected recovery probe on A")
	}
	ev := RoutingEvent{}
	retry := candidateRetryV4{enabled: true}
	calls := 0
	resp, ar, err := retry.execute(&ev, p, r, c, probe, 3, func() (hostModelExecutionResponse, attemptResult, error) {
		calls++
		view := candidateHealthViewV4(p, r, c)
		if view.State != healthHalfOpen || !view.ProbeInFlight || view.BackoffLevel != 0 {
			t.Fatalf("retry invalidated the existing probe: %#v", view)
		}
		if calls == 1 {
			err := &hostCallbackError{Code: "host_call_failed", HTTPStatus: 500, RequestNotSent: true}
			return hostModelExecutionResponse{}, attemptResult{Status: 500}, err
		}
		return hostModelExecutionResponse{StatusCode: 200}, attemptResult{Status: 200}, nil
	})
	if err != nil || ar.Status != 200 || resp.StatusCode != 200 || calls != 2 || len(ev.Attempts) != 2 || ev.Attempts[0].Status != 0 {
		t.Fatalf("expected one then successful retry, got status %d and %d attempts: %v", ar.Status, calls, err)
	}
	recordCandidateSuccessV4(p, r, c, probe)
	view := candidateHealthViewV4(p, r, c)
	if view.State != healthClosed || view.ProbeInFlight || view.ConsecutiveFailures != 0 {
		t.Fatalf("successful retry did not close circuit: %#v", view)
	}
}

func TestCandidateRetryRejectsNonTransportOrUnprovedErrors(t *testing.T) {
	for _, err := range []error{
		errors.New("request_not_sent: true"),
		&hostCallbackError{Code: "host_call_failed", HTTPStatus: 500, Message: "dial tcp: connection refused"},
		&hostCallbackError{Code: "host_call_failed", HTTPStatus: 429, RequestNotSent: true},
		&hostCallbackError{Code: "host_call_failed", HTTPStatus: 401, RequestNotSent: true},
		&hostCallbackError{Code: "host_call_failed", HTTPStatus: 500, RequestNotSent: true, Message: "kcr ticket is invalid or expired"},
		errSchedulerTicketUnclaimed,
	} {
		if confirmedRequestNotSentV4(err) {
			t.Errorf("accepted unproved or ineligible error %v", err)
		}
	}
}
