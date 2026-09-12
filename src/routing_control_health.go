package main

import (
	"net/http"
	"strings"
)

// isRoutingControlFailureV8 identifies failures in KCR/CPA routing ownership or
// credential identity resolution. They are not evidence that the configured
// candidate itself is unhealthy because KCR did not prove that credential was
// selected and executed.
func isRoutingControlFailureV8(err error) bool {
	if err == nil {
		return false
	}
	if err == errSchedulerTicketUnclaimed || err == errSchedulerTicketIssue {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"kcr ticket is invalid or expired",
		"kcr ticket was already claimed",
		"kcr auth resolution failed",
		"kcr pinned credential is not eligible in the current cpa candidate set",
		"kcr scheduler did not claim execution ticket",
		"kcr failed to issue execution ticket",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// recordCandidateExecutionFailureV8 separates execution-control failures from
// real upstream candidate failures. If a HALF_OPEN probe owned the attempt, a
// control failure releases that lease back to OPEN without changing backoff or
// marking the candidate healthy/unhealthy.
func recordCandidateExecutionFailureV8(p *Policy, r *PolicyRule, c *PolicyCandidate, status int, err error, headers http.Header, probe bool) {
	if isRoutingControlFailureV8(err) {
		releaseCandidateProbeV4(p, r, c, probe)
		return
	}
	recordCandidateFailureV4(p, r, c, status, err, headers, probe)
}
