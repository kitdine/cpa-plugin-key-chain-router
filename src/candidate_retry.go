package main

import (
	"errors"
	"net/http"
	"time"
)

const retryBeforeSwitchDelay = 300 * time.Millisecond

// CPA must assert this on the outer host.model.execute[_stream] error only
// when no inference request was sent during the whole nested execution.
// Error text and HTTP status alone cannot prove that a request is replayable.
func confirmedRequestNotSentV4(err error) bool {
	var hostErr *hostCallbackError
	return errors.As(err, &hostErr) && hostErr != nil &&
		hostErr.Code == "host_call_failed" && hostErr.RequestNotSent &&
		(hostErr.HTTPStatus == 0 || hostErr.HTTPStatus == http.StatusInternalServerError) &&
		!isRoutingControlFailureV8(err)
}

// The retry budget belongs to the entire downstream request, not each
// candidate. Keep cooldown/probe accounting with the final attempt.
type candidateRetryV4 struct {
	enabled bool
	used    bool
}

type candidateExecutionV4 func() (hostModelExecutionResponse, attemptResult, error)

func (retry *candidateRetryV4) execute(event *RoutingEvent, p *Policy, r *PolicyRule, c *PolicyCandidate, probe bool, max int, execute candidateExecutionV4) (hostModelExecutionResponse, attemptResult, error) {
	run := func() (hostModelExecutionResponse, attemptResult, error) {
		resp, ar, err := execute()
		if retry.enabled && confirmedRequestNotSentV4(err) {
			// CPA wraps transport errors as 500. A confirmed pre-send
			// failure uses the network action and cooldown, not HTTP 5xx.
			ar.Status = 0
		}
		event.Attempts = append(event.Attempts, ar)
		return resp, ar, err
	}
	resp, ar, err := run()
	if !retry.enabled || retry.used || len(event.Attempts) >= max ||
		!confirmedRequestNotSentV4(err) || failureActionV4(r.Failover, ar.Status, err) == failStop {
		return resp, ar, err
	}

	time.Sleep(retryBeforeSwitchDelay)
	if !candidateStillOwnsRetryV4(p, r, c, probe) {
		return resp, ar, err
	}
	retry.used = true
	event.sameCandidateRetryAt = len(event.Attempts) + 1
	event.healthSkips = append(event.healthSkips, nil)
	return run()
}

func candidateStillOwnsRetryV4(p *Policy, r *PolicyRule, c *PolicyCandidate, probe bool) bool {
	v4Runtime.RLock()
	defer v4Runtime.RUnlock()
	if c.runtimeGeneration != v4Runtime.generation || !candidateHealthConfigCurrentLockedV4(p, r, c) {
		return false
	}
	h := v4Runtime.health[candidateHealthKeyV4(p, r, c)]
	if probe {
		return h != nil && h.State == healthHalfOpen && h.ProbeInFlight
	}
	return h == nil || h.State == healthClosed
}
