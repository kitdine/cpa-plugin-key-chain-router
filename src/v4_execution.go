package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func runNonStreamPolicyV4(trace string, p *Policy, r *PolicyRule, ranked []*PolicyCandidate, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID string, started time.Time) (hostModelExecutionResponse, RoutingEvent, error) {
	event := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: false, ruleSnapshot: cloneRuleV4(r)}
	attempted := map[string]bool{}
	currentPriority := math.MaxInt
	nextAction := failNext
	var lastErr error
	max := r.Failover.MaxAttempts
	if max <= 0 {
		max = len(ranked) + 1
	}
	for len(event.Attempts) < max {
		c, probe, healthSkips := nextHealthyCandidateWithSkipsV4(p, r, ranked, attempted, nextAction, currentPriority)
		nextAction = failNext
		if c == nil {
			break
		}
		event.healthSkips = append(event.healthSkips, healthSkips)
		attempted[c.ID] = true
		currentPriority = c.Priority
		resp, ar, err := executeCandidateV4(c, source, clientModel, body, headers, query, alt, callbackID, false)
		event.Attempts = append(event.Attempts, ar)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
			recordCandidateSuccessV4(p, r, c, probe)
			event.Final = c.Name
			event.Provider = c.Provider
			event.AuthIndex = c.AuthIndex
			event.Status = resp.StatusCode
			event.Success = true
			event.DurationMs = time.Since(started).Milliseconds()
			return resp, event, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("upstream status %d", resp.StatusCode)
		}
		recordCandidateFailureV4(p, r, c, ar.Status, err, resp.Headers, probe)
		action := failureActionV4(r.Failover, ar.Status, err)
		if action == failCPADefault {
			return executeCPADefaultV4(event, source, clientModel, body, headers, query, alt, callbackID, started)
		}
		if action == failStop {
			break
		}
		nextAction = action
	}
	if r.Failover.Exhausted == failCPADefault {
		return executeCPADefaultV4(event, source, clientModel, body, headers, query, alt, callbackID, started)
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用候选")
	}
	event.Success = false
	event.DurationMs = time.Since(started).Milliseconds()
	event.Reason = "candidates_exhausted"
	event.Error = lastErr.Error()
	return hostModelExecutionResponse{}, event, lastErr
}

func nextCandidateV4(ranked []*PolicyCandidate, attempted map[string]bool, action string, currentPriority int) *PolicyCandidate {
	if action == failNextPriority {
		for _, c := range ranked {
			if !attempted[c.ID] && c.Priority < currentPriority {
				return c
			}
		}
		return nil
	}
	if action == failSamePriorityFirst {
		for _, c := range ranked {
			if !attempted[c.ID] && c.Priority == currentPriority {
				return c
			}
		}
		for _, c := range ranked {
			if !attempted[c.ID] && c.Priority < currentPriority {
				return c
			}
		}
		return nil
	}
	for _, c := range ranked {
		if !attempted[c.ID] {
			return c
		}
	}
	return nil
}

func moveCandidateFirstV4(xs []*PolicyCandidate, id string) []*PolicyCandidate {
	out := make([]*PolicyCandidate, 0, len(xs))
	for _, c := range xs {
		if c.ID == id {
			out = append(out, c)
			break
		}
	}
	for _, c := range xs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return out
}

func executeCandidateV4(c *PolicyCandidate, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID string, stream bool) (hostModelExecutionResponse, attemptResult, error) {
	model := strings.TrimSpace(c.OverrideModel)
	if model == "" {
		model = clientModel
	}
	h := cloneHeader(headers)
	h.Del(ticketHeader)
	ticket := ""
	if c.AuthIndex != "" {
		ticket = issueTicket(c.AuthIndex, c.Provider)
		h.Set(ticketHeader, ticket)
	}
	started := time.Now()
	method := methodHostModelExecute
	if stream {
		method = methodHostModelExecuteStream
	}
	raw, err := callHost(method, map[string]any{"entry_protocol": source, "exit_protocol": source, "model": model, "stream": stream, "body": rewriteBodyModel(body, model), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
	claimed := finishExecutionTicket(ticket)
	ar := attemptResult{Candidate: c.Name, Provider: c.Provider, AuthIndex: c.AuthIndex, Model: model, Duration: time.Since(started)}
	ar.DurationMs = ar.Duration.Milliseconds()
	if err == nil && ticket != "" && !claimed {
		err = errSchedulerTicketUnclaimed
	}
	if err != nil {
		ar.Error = err.Error()
		ar.Status = statusFromError(err)
		return hostModelExecutionResponse{}, ar, err
	}
	if stream {
		return hostModelExecutionResponse{Body: raw}, ar, nil
	}
	var resp hostModelExecutionResponse
	if e := json.Unmarshal(raw, &resp); e != nil {
		ar.Error = e.Error()
		return resp, ar, e
	}
	ar.Status = resp.StatusCode
	return resp, ar, nil
}

func executeCPADefaultV4(event RoutingEvent, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID string, started time.Time) (hostModelExecutionResponse, RoutingEvent, error) {
	h := cloneHeader(headers)
	h.Del(ticketHeader)
	t := time.Now()
	raw, err := callHost(methodHostModelExecute, map[string]any{"entry_protocol": source, "exit_protocol": source, "model": clientModel, "stream": false, "body": rewriteBodyModel(body, clientModel), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
	ar := attemptResult{Candidate: "CPA Default", Model: clientModel, Duration: time.Since(t)}
	ar.DurationMs = ar.Duration.Milliseconds()
	event.Decision = decisionFallbackToCPA
	event.Reason = "policy_fallback_to_cpa"
	event.Attempts = append(event.Attempts, ar)
	event.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		ar.Error = err.Error()
		ar.Status = statusFromError(err)
		event.Attempts[len(event.Attempts)-1] = ar
		event.Success = false
		event.Error = err.Error()
		return hostModelExecutionResponse{}, event, err
	}
	var resp hostModelExecutionResponse
	if e := json.Unmarshal(raw, &resp); e != nil {
		event.Success = false
		event.Error = e.Error()
		return resp, event, e
	}
	event.Attempts[len(event.Attempts)-1].Status = resp.StatusCode
	event.Status = resp.StatusCode
	event.Final = "CPA Default"
	event.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	return resp, event, nil
}

func failureActionV4(f FailoverPolicy, status int, err error) string {
	f = normalizeFailover(f)
	if status == 0 && err != nil {
		return f.Network
	}
	switch status {
	case 401, 403:
		return f.Unauthorized
	case 408:
		return f.Timeout
	case 409:
		return f.Conflict
	case 429:
		return f.RateLimit
	}
	if status >= 500 && status <= 599 {
		return f.ServerError
	}
	return f.Other
}

func runStreamPolicyV4(trace string, p *Policy, r *PolicyRule, ranked []*PolicyCandidate, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID, outStreamID string, started time.Time) {
	event := RoutingEvent{TraceID: trace, At: nowV4(), Decision: decisionHandled, PolicyName: p.Name, KeyFingerprint: p.KeyFingerprint, KeyHint: p.KeyHint, RuleID: r.ID, RuleName: r.Name, Strategy: r.Strategy, Model: clientModel, Stream: true, ruleSnapshot: cloneRuleV4(r)}
	attempted := map[string]bool{}
	currentPriority := math.MaxInt
	nextAction := failNext
	max := r.Failover.MaxAttempts
	if max <= 0 {
		max = len(ranked)
	}
	var lastErr error
	var activeCandidate *PolicyCandidate
	activeProbe := false
	defer func() {
		if x := recover(); x != nil {
			// Never strand a HALF_OPEN lease if the stream worker panics.
			releaseCandidateProbeV4(p, r, activeCandidate, activeProbe)
			_ = closeOutputStream(outStreamID, fmt.Sprintf("panic: %v", x))
		}
	}()
	for len(event.Attempts) < max {
		c, probe, healthSkips := nextHealthyCandidateWithSkipsV4(p, r, ranked, attempted, nextAction, currentPriority)
		nextAction = failNext
		if c == nil {
			break
		}
		event.healthSkips = append(event.healthSkips, healthSkips)
		activeCandidate = c
		activeProbe = probe
		attempted[c.ID] = true
		currentPriority = c.Priority
		model := c.OverrideModel
		if model == "" {
			model = clientModel
		}
		h := cloneHeader(headers)
		h.Del(ticketHeader)
		ticket := ""
		if c.AuthIndex != "" {
			ticket = issueTicket(c.AuthIndex, c.Provider)
			h.Set(ticketHeader, ticket)
		}
		t := time.Now()
		raw, err := callHost(methodHostModelExecuteStream, map[string]any{"entry_protocol": source, "exit_protocol": source, "model": model, "stream": true, "body": rewriteBodyModel(body, model), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
		claimed := finishExecutionTicket(ticket)
		ar := attemptResult{Candidate: c.Name, Provider: c.Provider, AuthIndex: c.AuthIndex, Model: model, Duration: time.Since(t)}
		ar.DurationMs = ar.Duration.Milliseconds()
		if err == nil && ticket != "" && !claimed {
			var leaked hostModelStreamResponse
			if json.Unmarshal(raw, &leaked) == nil && leaked.StreamID != "" {
				_ = closeHostStream(leaked.StreamID)
			}
			err = errSchedulerTicketUnclaimed
		}
		if err != nil {
			ar.Error = err.Error()
			ar.Status = statusFromError(err)
			event.Attempts = append(event.Attempts, ar)
			lastErr = err
			recordCandidateFailureV4(p, r, c, ar.Status, err, nil, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, ar.Status, err)
			if action == failCPADefault {
				runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
				return
			}
			if action == failStop {
				break
			}
			nextAction = action
			continue
		}
		var sr hostModelStreamResponse
		if err = json.Unmarshal(raw, &sr); err != nil {
			ar.Error = err.Error()
			event.Attempts = append(event.Attempts, ar)
			lastErr = err
			recordCandidateFailureV4(p, r, c, 0, err, nil, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, 0, err)
			if action == failCPADefault {
				runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
				return
			}
			if action == failStop {
				break
			}
			nextAction = action
			continue
		}
		ar.Status = sr.StatusCode
		event.Attempts = append(event.Attempts, ar)
		if sr.StatusCode < 200 || sr.StatusCode >= 400 {
			if sr.StreamID != "" {
				_ = closeHostStream(sr.StreamID)
			}
			lastErr = fmt.Errorf("upstream status %d", sr.StatusCode)
			event.Attempts[len(event.Attempts)-1].Error = lastErr.Error()
			recordCandidateFailureV4(p, r, c, sr.StatusCode, nil, sr.Headers, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, sr.StatusCode, nil)
			if action == failCPADefault {
				runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
				return
			}
			if action == failStop {
				break
			}
			nextAction = action
			continue
		}
		if sr.StreamID == "" {
			lastErr = errors.New("host stream id 为空")
			event.Attempts[len(event.Attempts)-1].Error = lastErr.Error()
			recordCandidateFailureV4(p, r, c, 0, lastErr, sr.Headers, probe)
			clearProbeOwnershipV4(&activeProbe)
			action := failureActionV4(r.Failover, 0, lastErr)
			if action == failCPADefault {
				runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
				return
			}
			if action == failStop {
				break
			}
			nextAction = action
			continue
		}
		first := true
		stopAfterFirstReadFailure := false
		for {
			rr, e := readHostStream(sr.StreamID)
			if e != nil {
				_ = closeHostStream(sr.StreamID)
				lastErr = e
				recordCandidateFailureV4(p, r, c, statusFromError(e), e, sr.Headers, probe)
				clearProbeOwnershipV4(&activeProbe)
				if first {
					event.Attempts[len(event.Attempts)-1].Error = e.Error()
					event.Attempts[len(event.Attempts)-1].Status = statusFromError(e)
					action := failureActionV4(r.Failover, statusFromError(e), e)
					if action == failCPADefault {
						runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
						return
					}
					if action == failStop {
						stopAfterFirstReadFailure = true
					} else {
						nextAction = action
					}
					break
				}
				event.Success = false
				event.Error = e.Error()
				event.DurationMs = time.Since(started).Milliseconds()
				observeV4(event, callbackID)
				_ = closeOutputStream(outStreamID, e.Error())
				return
			}
			if rr.Error != "" {
				_ = closeHostStream(sr.StreamID)
				lastErr = errors.New(rr.Error)
				streamStatus := statusFromError(lastErr)
				recordCandidateFailureV4(p, r, c, streamStatus, lastErr, sr.Headers, probe)
				clearProbeOwnershipV4(&activeProbe)
				if first {
					event.Attempts[len(event.Attempts)-1].Error = rr.Error
					event.Attempts[len(event.Attempts)-1].Status = streamStatus
					action := failureActionV4(r.Failover, streamStatus, lastErr)
					if action == failCPADefault {
						runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
						return
					}
					if action == failStop {
						stopAfterFirstReadFailure = true
					} else {
						nextAction = action
					}
					break
				}
				event.Success = false
				event.Error = rr.Error
				event.DurationMs = time.Since(started).Milliseconds()
				observeV4(event, callbackID)
				_ = closeOutputStream(outStreamID, rr.Error)
				return
			}
			if len(rr.Payload) > 0 {
				first = false
				if e = emitOutput(outStreamID, rr.Payload); e != nil {
					_ = closeHostStream(sr.StreamID)
					releaseCandidateProbeV4(p, r, c, probe)
					clearProbeOwnershipV4(&activeProbe)
					_ = closeOutputStream(outStreamID, e.Error())
					return
				}
			}
			if rr.Done {
				_ = closeHostStream(sr.StreamID)
				recordCandidateSuccessV4(p, r, c, probe)
				clearProbeOwnershipV4(&activeProbe)
				event.Final = c.Name
				event.Provider = c.Provider
				event.AuthIndex = c.AuthIndex
				event.Status = sr.StatusCode
				event.Success = true
				event.DurationMs = time.Since(started).Milliseconds()
				observeV4(event, callbackID)
				_ = closeOutputStream(outStreamID, "")
				return
			}
		}
		if stopAfterFirstReadFailure {
			break
		}
	}
	if r.Failover.Exhausted == failCPADefault {
		runCPADefaultStreamV4(event, source, clientModel, body, headers, query, alt, callbackID, outStreamID, started)
		return
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用候选")
	}
	event.Success = false
	event.Reason = "candidates_exhausted"
	event.Error = lastErr.Error()
	event.DurationMs = time.Since(started).Milliseconds()
	observeV4(event, callbackID)
	_ = closeOutputStream(outStreamID, lastErr.Error())
}

func runCPADefaultStreamV4(event RoutingEvent, source, clientModel string, body []byte, headers http.Header, query url.Values, alt, callbackID, outStreamID string, started time.Time) {
	h := cloneHeader(headers)
	h.Del(ticketHeader)
	t := time.Now()
	raw, err := callHost(methodHostModelExecuteStream, map[string]any{"entry_protocol": source, "exit_protocol": source, "model": clientModel, "stream": true, "body": rewriteBodyModel(body, clientModel), "headers": h, "query": query, "alt": alt, "host_callback_id": callbackID})
	ar := attemptResult{Candidate: "CPA Default", Model: clientModel, Duration: time.Since(t)}
	ar.DurationMs = ar.Duration.Milliseconds()
	event.Decision = decisionFallbackToCPA
	event.Reason = "policy_fallback_to_cpa"
	event.Attempts = append(event.Attempts, ar)
	if err != nil {
		event.Attempts[len(event.Attempts)-1].Error = err.Error()
		event.Attempts[len(event.Attempts)-1].Status = statusFromError(err)
		event.Error = err.Error()
		event.DurationMs = time.Since(started).Milliseconds()
		observeV4(event, callbackID)
		_ = closeOutputStream(outStreamID, err.Error())
		return
	}
	var sr hostModelStreamResponse
	if err = json.Unmarshal(raw, &sr); err != nil || sr.StreamID == "" {
		if err == nil {
			err = errors.New("host stream id 为空")
		}
		event.Error = err.Error()
		event.DurationMs = time.Since(started).Milliseconds()
		observeV4(event, callbackID)
		_ = closeOutputStream(outStreamID, err.Error())
		return
	}
	event.Attempts[len(event.Attempts)-1].Status = sr.StatusCode
	for {
		rr, e := readHostStream(sr.StreamID)
		if e != nil {
			_ = closeHostStream(sr.StreamID)
			event.Error = e.Error()
			event.DurationMs = time.Since(started).Milliseconds()
			observeV4(event, callbackID)
			_ = closeOutputStream(outStreamID, e.Error())
			return
		}
		if rr.Error != "" {
			_ = closeHostStream(sr.StreamID)
			event.Error = rr.Error
			event.DurationMs = time.Since(started).Milliseconds()
			observeV4(event, callbackID)
			_ = closeOutputStream(outStreamID, rr.Error)
			return
		}
		if len(rr.Payload) > 0 {
			if e = emitOutput(outStreamID, rr.Payload); e != nil {
				_ = closeHostStream(sr.StreamID)
				return
			}
		}
		if rr.Done {
			_ = closeHostStream(sr.StreamID)
			event.Final = "CPA Default"
			event.Status = sr.StatusCode
			event.Success = true
			event.DurationMs = time.Since(started).Milliseconds()
			observeV4(event, callbackID)
			_ = closeOutputStream(outStreamID, "")
			return
		}
	}
}
